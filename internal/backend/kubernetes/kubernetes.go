package kubernetes

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	securityv1alpha1 "secuity.rancher.io/network-enforcer/api/v1alpha1"
)

type Backend struct{}

func (b *Backend) Name() string { return "kubernetes" }

func (b *Backend) AddToScheme(_ *runtime.Scheme) error { return nil }

func (b *Backend) Empty() client.Object {
	return &networkingv1.NetworkPolicy{}
}

func (b *Backend) UpdateSpec(existing, desired client.Object) {
	existing.(*networkingv1.NetworkPolicy).Spec = desired.(*networkingv1.NetworkPolicy).Spec
}

func (b *Backend) Build(name, namespace string, podSelector map[string]string, proposal *securityv1alpha1.NetworkPolicyProposal) client.Object {
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: podSelector,
			},
		},
	}

	for _, rule := range proposal.Spec.Ingress {
		policy.Spec.Ingress = append(policy.Spec.Ingress, toIngressRule(rule))
	}
	for _, rule := range proposal.Spec.Egress {
		policy.Spec.Egress = append(policy.Spec.Egress, toEgressRule(rule))
	}

	if len(policy.Spec.Ingress) > 0 || len(policy.Spec.Egress) == 0 {
		policy.Spec.PolicyTypes = append(policy.Spec.PolicyTypes, networkingv1.PolicyTypeIngress)
	}
	if len(policy.Spec.Egress) > 0 {
		policy.Spec.PolicyTypes = append(policy.Spec.PolicyTypes, networkingv1.PolicyTypeEgress)
	}

	return policy
}

func toIngressRule(rule securityv1alpha1.ProposedRule) networkingv1.NetworkPolicyIngressRule {
	ir := networkingv1.NetworkPolicyIngressRule{}
	for _, peer := range rule.Peers {
		ir.From = append(ir.From, toPeer(peer))
	}
	for _, port := range rule.Ports {
		ir.Ports = append(ir.Ports, toPort(port))
	}
	return ir
}

func toEgressRule(rule securityv1alpha1.ProposedRule) networkingv1.NetworkPolicyEgressRule {
	er := networkingv1.NetworkPolicyEgressRule{}
	for _, peer := range rule.Peers {
		er.To = append(er.To, toPeer(peer))
	}
	for _, port := range rule.Ports {
		er.Ports = append(er.Ports, toPort(port))
	}
	return er
}

func toPeer(peer securityv1alpha1.PolicyPeer) networkingv1.NetworkPolicyPeer {
	npp := networkingv1.NetworkPolicyPeer{}
	if peer.CIDR != "" {
		npp.IPBlock = &networkingv1.IPBlock{CIDR: peer.CIDR}
	} else if peer.Workload != nil && peer.Namespace != "" {
		npp.NamespaceSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"kubernetes.io/metadata.name": peer.Namespace,
			},
		}
	}
	return npp
}

func toPort(port securityv1alpha1.PortRule) networkingv1.NetworkPolicyPort {
	proto := protocolFromString(port.Protocol)
	portVal := intstr.FromInt32(port.Port)
	return networkingv1.NetworkPolicyPort{
		Protocol: &proto,
		Port:     &portVal,
	}
}

func protocolFromString(s string) corev1.Protocol {
	switch s {
	case "UDP":
		return corev1.ProtocolUDP
	default:
		return corev1.ProtocolTCP
	}
}
