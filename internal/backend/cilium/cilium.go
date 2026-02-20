package cilium

import (
	"fmt"
	"maps"

	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	slimmetav1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/meta/v1"
	"github.com/cilium/cilium/pkg/policy/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	securityv1alpha1 "secuity.rancher.io/network-enforcer/api/v1alpha1"
)

type Backend struct{}

func (b *Backend) Name() string { return "cilium" }

func (b *Backend) AddToScheme(s *runtime.Scheme) error {
	return ciliumv2.AddToScheme(s)
}

func (b *Backend) Empty() client.Object {
	return &ciliumv2.CiliumNetworkPolicy{}
}

func (b *Backend) UpdateSpec(existing, desired client.Object) {
	e := existing.(*ciliumv2.CiliumNetworkPolicy)
	d := desired.(*ciliumv2.CiliumNetworkPolicy)
	e.Spec = d.Spec
	e.Specs = d.Specs
}

func (b *Backend) Build(name, namespace string, podSelector map[string]string, proposal *securityv1alpha1.NetworkPolicyProposal) client.Object {
	policy := &ciliumv2.CiliumNetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	spec := &api.Rule{
		EndpointSelector: api.EndpointSelector{
			LabelSelector: &slimmetav1.LabelSelector{
				MatchLabels: toSlimLabels(podSelector),
			},
		},
	}

	for _, rule := range proposal.Spec.Ingress {
		spec.Ingress = append(spec.Ingress, toIngressRule(rule))
	}
	for _, rule := range proposal.Spec.Egress {
		spec.Egress = append(spec.Egress, toEgressRule(rule))
	}

	policy.Spec = spec

	return policy
}

func toSlimLabels(labels map[string]string) map[string]slimmetav1.MatchLabelsValue {
	result := make(map[string]slimmetav1.MatchLabelsValue, len(labels))
	maps.Copy(result, labels)
	return result
}

func toIngressRule(rule securityv1alpha1.ProposedRule) api.IngressRule {
	ir := api.IngressRule{}

	for _, peer := range rule.Peers {
		if peer.CIDR != "" {
			ir.FromCIDR = append(ir.FromCIDR, api.CIDR(peer.CIDR))
		} else if peer.Workload != nil && peer.Namespace != "" {
			ir.FromEndpoints = append(ir.FromEndpoints,
				api.EndpointSelector{
					LabelSelector: &slimmetav1.LabelSelector{
						MatchLabels: map[string]slimmetav1.MatchLabelsValue{
							"io.kubernetes.pod.namespace": peer.Namespace,
						},
					},
				},
			)
		}
	}

	if len(rule.Ports) > 0 {
		ir.ToPorts = toPorts(rule.Ports)
	}

	return ir
}

func toEgressRule(rule securityv1alpha1.ProposedRule) api.EgressRule {
	er := api.EgressRule{}

	for _, peer := range rule.Peers {
		if peer.CIDR != "" {
			er.ToCIDR = append(er.ToCIDR, api.CIDR(peer.CIDR))
		} else if peer.Workload != nil && peer.Namespace != "" {
			er.ToEndpoints = append(er.ToEndpoints,
				api.EndpointSelector{
					LabelSelector: &slimmetav1.LabelSelector{
						MatchLabels: map[string]slimmetav1.MatchLabelsValue{
							"io.kubernetes.pod.namespace": peer.Namespace,
						},
					},
				},
			)
		}
	}

	if len(rule.Ports) > 0 {
		er.ToPorts = toPorts(rule.Ports)
	}

	return er
}

func toPorts(ports []securityv1alpha1.PortRule) api.PortRules {
	portRule := api.PortRule{}
	for _, p := range ports {
		portRule.Ports = append(portRule.Ports, api.PortProtocol{
			Port:     fmt.Sprintf("%d", p.Port),
			Protocol: api.L4Proto(p.Protocol),
		})
	}
	return api.PortRules{portRule}
}
