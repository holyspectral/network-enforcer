package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	securityv1alpha1 "secuity.rancher.io/network-enforcer/api/v1alpha1"
	"secuity.rancher.io/network-enforcer/internal/topology"
)

const (
	namespaceLabelKey = "kubernetes.io/metadata.name"
)

type TopologyScanner struct {
	client   client.Client
	store    *topology.Store
	log      *slog.Logger
	interval time.Duration
}

func NewTopologyScanner(
	c client.Client,
	store *topology.Store,
	logger *slog.Logger,
	drainInterval time.Duration,
) *TopologyScanner {
	return &TopologyScanner{
		client:   c,
		store:    store,
		log:      logger.With("component", "topology-scanner"),
		interval: drainInterval,
	}
}

func (ts *TopologyScanner) Start(ctx context.Context) error {
	ts.log.InfoContext(ctx, "starting", "drain interval", ts.interval.String())

	ticker := time.NewTicker(ts.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			ts.scan(ctx)
		}
	}
}

func getProposalName(workload topology.WorkloadKey, direction networkingv1.PolicyType) string {
	return fmt.Sprintf(
		"%s-%s-%s",
		strings.ToLower(string(workload.OwnerKind)),
		workload.OwnerName,
		strings.ToLower(string(direction)),
	)
}

func getProposalMetadata(
	workload topology.WorkloadKey,
	direction networkingv1.PolicyType,
) *securityv1alpha1.WorkloadNetworkPolicyProposal {
	return &securityv1alpha1.WorkloadNetworkPolicyProposal{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getProposalName(workload, direction),
			Namespace: workload.Namespace,
		},
	}
}

func (ts *TopologyScanner) scan(ctx context.Context) {
	// todo!: it would be nice to drain only connections that are correctly reconciled.
	// at the moment we just log a warning and we drop the connection.
	// we should implement some strategies to handle this more gracefully.
	connections := ts.store.DrainFlows()
	ts.log.InfoContext(
		ctx,
		"Drain flows",
		"egress policies",
		len(connections.Egress),
		"ingress policies",
		len(connections.Ingress),
	)
	for workload, peers := range connections.Egress {
		ts.log.InfoContext(
			ctx,
			"Reconciling egress proposal",
			"namespace",
			workload.Namespace,
			"kind",
			workload.OwnerKind,
			"name",
			workload.OwnerName,
			"peers",
			len(peers),
		)
		if err := ts.reconcileProposal(ctx, workload, networkingv1.PolicyTypeEgress, peers); err != nil {
			ts.log.WarnContext(
				ctx,
				"Could not reconcile egress proposal",
				"namespace",
				workload.Namespace,
				"kind",
				workload.OwnerKind,
				"name",
				workload.OwnerName,
				"error",
				err,
			)
		}
	}

	for workload, peers := range connections.Ingress {
		ts.log.InfoContext(
			ctx,
			"Reconciling ingress proposal",
			"namespace",
			workload.Namespace,
			"kind",
			workload.OwnerKind,
			"name",
			workload.OwnerName,
			"peers",
			len(peers),
		)
		if err := ts.reconcileProposal(ctx, workload, networkingv1.PolicyTypeIngress, peers); err != nil {
			ts.log.WarnContext(
				ctx,
				"Could not reconcile ingress proposal",
				"namespace",
				workload.Namespace,
				"kind",
				workload.OwnerKind,
				"name",
				workload.OwnerName,
				"error",
				err,
			)
		}
	}
}

func (ts *TopologyScanner) reconcileProposal(
	ctx context.Context,
	workload topology.WorkloadKey,
	direction networkingv1.PolicyType,
	deltaPeers sets.Set[topology.Peer],
) error {
	if deltaPeers == nil || deltaPeers.Len() == 0 {
		return errors.New("no peers associated to the workload")
	}
	proposal := getProposalMetadata(workload, direction)

	alreadyPromoted, err := hasPromotedPolicy(ctx, ts.client, proposal.Namespace, proposal.Name)
	if err != nil {
		return fmt.Errorf("checking promoted policy for proposal %s/%s: %w", proposal.Namespace, proposal.Name, err)
	}
	if alreadyPromoted {
		return nil
	}

	if _, err = controllerutil.CreateOrUpdate(ctx, ts.client, proposal, func() error {
		// we recompute the selector only if we are creating the resource the first time.
		// we could continuously recompute the selector if we want to keep track of updates.
		// the policyTypes should be empty only when the resource is new.
		if len(proposal.Spec.PolicyTypes) == 0 {
			var workloadSelector metav1.LabelSelector
			workloadSelector, err = selectorFromWorkloadKey(ctx, ts.client, workload)
			if err != nil {
				return fmt.Errorf("resolving workload selector: %w", err)
			}
			proposal.Spec.PodSelector = workloadSelector
			proposal.Spec.PolicyTypes = []networkingv1.PolicyType{direction}
		}
		return ts.buildSpec(ctx, direction, &proposal.Spec, deltaPeers)
	}); err != nil {
		return fmt.Errorf("create or update proposal %s/%s: %w", proposal.Namespace, proposal.Name, err)
	}

	return nil
}

func containsRule[T any](newRule T, existing []T, equalFn func(T, T) bool) bool {
	for _, rule := range existing {
		if equalFn(newRule, rule) {
			return true
		}
	}
	return false
}

func selectorEqual(a, b *metav1.LabelSelector) bool {
	return metav1.FormatLabelSelector(a) == metav1.FormatLabelSelector(b)
}

func ipBlockEqual(a, b *networkingv1.IPBlock) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.CIDR != b.CIDR {
		return false
	}
	return equalUnordered(a.Except, b.Except, func(left, right string) bool {
		return left == right
	})
}

func policyPeerEqual(a, b networkingv1.NetworkPolicyPeer) bool {
	return selectorEqual(a.NamespaceSelector, b.NamespaceSelector) &&
		selectorEqual(a.PodSelector, b.PodSelector) &&
		ipBlockEqual(a.IPBlock, b.IPBlock)
}

func policyPortEqual(a, b networkingv1.NetworkPolicyPort) bool {
	return reflect.DeepEqual(a, b)
}

func equalUnordered[T any](left, right []T, equalFn func(T, T) bool) bool {
	if len(left) != len(right) {
		return false
	}

	used := make([]bool, len(right))
	for _, leftItem := range left {
		matched := false
		for i, rightItem := range right {
			if used[i] {
				continue
			}
			if equalFn(leftItem, rightItem) {
				used[i] = true
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

func egressRuleEqual(a, b networkingv1.NetworkPolicyEgressRule) bool {
	return equalUnordered(a.To, b.To, policyPeerEqual) &&
		equalUnordered(a.Ports, b.Ports, policyPortEqual)
}

func ingressRuleEqual(a, b networkingv1.NetworkPolicyIngressRule) bool {
	return equalUnordered(a.From, b.From, policyPeerEqual) &&
		equalUnordered(a.Ports, b.Ports, policyPortEqual)
}

func (ts *TopologyScanner) buildSpec(
	ctx context.Context,
	direction networkingv1.PolicyType,
	spec *networkingv1.NetworkPolicySpec,
	deltaPeers sets.Set[topology.Peer],
) error {
	switch direction {
	case networkingv1.PolicyTypeEgress:
		deltaRules, err := ts.buildEgressRules(ctx, deltaPeers)
		if err != nil {
			return err
		}
		for _, rule := range deltaRules {
			if containsRule(rule, spec.Egress, egressRuleEqual) {
				continue
			}
			spec.Egress = append(spec.Egress, rule)
		}
	case networkingv1.PolicyTypeIngress:
		deltaRules, err := ts.buildIngressRules(ctx, deltaPeers)
		if err != nil {
			return err
		}

		for _, rule := range deltaRules {
			if containsRule(rule, spec.Ingress, ingressRuleEqual) {
				continue
			}
			spec.Ingress = append(spec.Ingress, rule)
		}
	default:
		return fmt.Errorf("unknown direction: %s", direction)
	}

	return nil
}

func (ts *TopologyScanner) buildEgressRules(
	ctx context.Context,
	peers sets.Set[topology.Peer],
) ([]networkingv1.NetworkPolicyEgressRule, error) {
	peerList := peers.UnsortedList()

	rules := make([]networkingv1.NetworkPolicyEgressRule, 0, len(peerList))
	for _, peer := range peerList {
		policyPeer, policyPort, err := ts.buildPeerRuleParts(ctx, peer)
		if err != nil {
			return nil, fmt.Errorf("resolving egress peer selector: %w", err)
		}

		rules = append(rules, networkingv1.NetworkPolicyEgressRule{
			To:    []networkingv1.NetworkPolicyPeer{policyPeer},
			Ports: []networkingv1.NetworkPolicyPort{policyPort},
		})
	}

	return rules, nil
}

func (ts *TopologyScanner) buildIngressRules(
	ctx context.Context,
	peers sets.Set[topology.Peer],
) ([]networkingv1.NetworkPolicyIngressRule, error) {
	peerList := peers.UnsortedList()

	rules := make([]networkingv1.NetworkPolicyIngressRule, 0, len(peerList))
	for _, peer := range peerList {
		policyPeer, policyPort, err := ts.buildPeerRuleParts(ctx, peer)
		if err != nil {
			return nil, fmt.Errorf("resolving ingress peer selector: %w", err)
		}

		rules = append(rules, networkingv1.NetworkPolicyIngressRule{
			From:  []networkingv1.NetworkPolicyPeer{policyPeer},
			Ports: []networkingv1.NetworkPolicyPort{policyPort},
		})
	}

	return rules, nil
}

func (ts *TopologyScanner) buildPeerRuleParts(
	ctx context.Context,
	peer topology.Peer,
) (networkingv1.NetworkPolicyPeer, networkingv1.NetworkPolicyPort, error) {
	peerSelector, err := selectorFromWorkloadKey(ctx, ts.client, peer.WorkloadKey)
	if err != nil {
		return networkingv1.NetworkPolicyPeer{}, networkingv1.NetworkPolicyPort{}, err
	}

	port := intstr.FromInt32(peer.DstPort)

	policyPeer := networkingv1.NetworkPolicyPeer{
		NamespaceSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				namespaceLabelKey: peer.Namespace,
			},
		},
		PodSelector: &peerSelector,
	}
	policyPort := networkingv1.NetworkPolicyPort{
		Protocol: &peer.Protocol,
		Port:     &port,
	}

	return policyPeer, policyPort, nil
}
