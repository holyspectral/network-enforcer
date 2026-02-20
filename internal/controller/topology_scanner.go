package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	securityv1alpha1 "secuity.rancher.io/network-enforcer/api/v1alpha1"
	"secuity.rancher.io/network-enforcer/internal/topology"
)

type TopologyScanner struct {
	client   client.Client
	store    *topology.Store
	log      logr.Logger
	interval time.Duration
}

func NewTopologyScanner(c client.Client, store *topology.Store, log logr.Logger) *TopologyScanner {
	return &TopologyScanner{
		client:   c,
		store:    store,
		log:      log.WithName("topology-scanner"),
		interval: 30 * time.Second,
	}
}

func (ts *TopologyScanner) Start(ctx context.Context) error {
	ts.log.Info("starting", "interval", ts.interval)

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

func (ts *TopologyScanner) scan(ctx context.Context) {
	workloads := ts.store.Workloads()
	for _, wk := range workloads {
		name := proposalName(wk)
		ns := wk.Namespace
		if ns == "" {
			continue
		}

		existing := &securityv1alpha1.NetworkPolicyProposal{}
		err := ts.client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, existing)
		if err == nil {
			continue
		}
		if !apierrors.IsNotFound(err) {
			ts.log.Error(err, "get proposal", "name", name, "namespace", ns)
			continue
		}

		proposal := &securityv1alpha1.NetworkPolicyProposal{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
			Spec: securityv1alpha1.NetworkPolicyProposalSpec{
				WorkloadRef: securityv1alpha1.WorkloadReference{
					Kind: wk.OwnerKind,
					Name: wk.OwnerName,
				},
			},
		}

		if err := ts.client.Create(ctx, proposal); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				ts.log.Error(err, "create proposal", "name", name, "namespace", ns)
			}
			continue
		}
		ts.log.Info("created proposal", "name", name, "namespace", ns)
	}
}

func proposalName(wk topology.WorkloadKey) string {
	return fmt.Sprintf("%s-%s", strings.ToLower(wk.OwnerKind), wk.OwnerName)
}
