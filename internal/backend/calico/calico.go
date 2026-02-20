package calico

import (
	"fmt"
	"strings"

	calicov3 "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	"github.com/projectcalico/api/pkg/lib/numorstring"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	securityv1alpha1 "secuity.rancher.io/network-enforcer/api/v1alpha1"
)

type Backend struct{}

func (b *Backend) Name() string { return "calico" }

func (b *Backend) AddToScheme(s *runtime.Scheme) error {
	return calicov3.AddToScheme(s)
}

func (b *Backend) Empty() client.Object {
	return &calicov3.NetworkPolicy{}
}

func (b *Backend) UpdateSpec(existing, desired client.Object) {
	existing.(*calicov3.NetworkPolicy).Spec = desired.(*calicov3.NetworkPolicy).Spec
}

func (b *Backend) Build(name, namespace string, podSelector map[string]string, proposal *securityv1alpha1.NetworkPolicyProposal) client.Object {
	policy := &calicov3.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: calicov3.NetworkPolicySpec{
			Selector: labelsToSelector(podSelector),
		},
	}

	for _, rule := range proposal.Spec.Ingress {
		policy.Spec.Ingress = append(policy.Spec.Ingress, toCalicoRule(rule))
	}
	for _, rule := range proposal.Spec.Egress {
		policy.Spec.Egress = append(policy.Spec.Egress, toCalicoRule(rule))
	}

	var policyTypes []calicov3.PolicyType
	if len(policy.Spec.Ingress) > 0 || len(policy.Spec.Egress) == 0 {
		policyTypes = append(policyTypes, calicov3.PolicyTypeIngress)
	}
	if len(policy.Spec.Egress) > 0 {
		policyTypes = append(policyTypes, calicov3.PolicyTypeEgress)
	}
	policy.Spec.Types = policyTypes

	return policy
}

func labelsToSelector(labels map[string]string) string {
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, fmt.Sprintf("%s == '%s'", k, v))
	}
	return strings.Join(parts, " && ")
}

func toCalicoRule(rule securityv1alpha1.ProposedRule) calicov3.Rule {
	cr := calicov3.Rule{
		Action: calicov3.Allow,
	}

	for _, peer := range rule.Peers {
		if peer.CIDR != "" {
			cr.Source.Nets = append(cr.Source.Nets, peer.CIDR)
		} else if peer.Workload != nil {
			if peer.Namespace != "" {
				cr.Source.NamespaceSelector = fmt.Sprintf("projectcalico.org/name == '%s'", peer.Namespace)
			}
		}
	}

	for _, port := range rule.Ports {
		proto := numorstring.ProtocolFromString(port.Protocol)
		cr.Protocol = &proto
		cr.Destination.Ports = append(cr.Destination.Ports, numorstring.Port{
			MinPort: uint16(port.Port),
			MaxPort: uint16(port.Port),
		})
	}

	return cr
}
