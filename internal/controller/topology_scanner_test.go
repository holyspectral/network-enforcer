package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func protocolPtr(p corev1.Protocol) *corev1.Protocol {
	return &p
}

func int32Ptr(v int32) *int32 {
	return &v
}

func portPtr(v int32) *intstr.IntOrString {
	port := intstr.FromInt32(v)
	return &port
}

func labelSelector(labels map[string]string) *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: labels}
}

func TestContainsRuleEgress(t *testing.T) {
	tcp := protocolPtr(corev1.ProtocolTCP)
	udp := protocolPtr(corev1.ProtocolUDP)

	frontend := networkingv1.NetworkPolicyPeer{
		NamespaceSelector: labelSelector(map[string]string{namespaceLabelKey: "team-a"}),
		PodSelector:       labelSelector(map[string]string{"app": "frontend"}),
	}
	backend := networkingv1.NetworkPolicyPeer{
		NamespaceSelector: labelSelector(map[string]string{namespaceLabelKey: "team-b"}),
		PodSelector:       labelSelector(map[string]string{"app": "backend"}),
	}

	ruleAB := networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{frontend, backend},
		Ports: []networkingv1.NetworkPolicyPort{
			{Protocol: tcp, Port: portPtr(443)},
			{Protocol: udp, Port: portPtr(53)},
		},
	}
	ruleBA := networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{backend, frontend},
		Ports: []networkingv1.NetworkPolicyPort{
			{Protocol: udp, Port: portPtr(53)},
			{Protocol: tcp, Port: portPtr(443)},
		},
	}

	tests := []struct {
		name     string
		newRule  networkingv1.NetworkPolicyEgressRule
		existing []networkingv1.NetworkPolicyEgressRule
		want     bool
	}{
		{
			name:     "matches same order",
			newRule:  ruleAB,
			existing: []networkingv1.NetworkPolicyEgressRule{ruleAB},
			want:     true,
		},
		{
			name:     "matches different peers and ports order",
			newRule:  ruleAB,
			existing: []networkingv1.NetworkPolicyEgressRule{ruleBA},
			want:     true,
		},
		{
			name: "does not match with different port",
			newRule: networkingv1.NetworkPolicyEgressRule{
				To: ruleAB.To,
				Ports: []networkingv1.NetworkPolicyPort{
					{Protocol: tcp, Port: portPtr(8443)},
					{Protocol: udp, Port: portPtr(53)},
				},
			},
			existing: []networkingv1.NetworkPolicyEgressRule{ruleAB},
			want:     false,
		},
		{
			name: "does not match with different protocol",
			newRule: networkingv1.NetworkPolicyEgressRule{
				To: ruleAB.To,
				Ports: []networkingv1.NetworkPolicyPort{
					{Protocol: udp, Port: portPtr(443)},
					{Protocol: udp, Port: portPtr(53)},
				},
			},
			existing: []networkingv1.NetworkPolicyEgressRule{ruleAB},
			want:     false,
		},
		{
			name: "does not match with different selector",
			newRule: networkingv1.NetworkPolicyEgressRule{
				To: []networkingv1.NetworkPolicyPeer{{
					NamespaceSelector: labelSelector(map[string]string{namespaceLabelKey: "team-a"}),
					PodSelector:       labelSelector(map[string]string{"app": "frontend-v2"}),
				}},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: tcp, Port: portPtr(443)}},
			},
			existing: []networkingv1.NetworkPolicyEgressRule{{
				To:    []networkingv1.NetworkPolicyPeer{frontend},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: tcp, Port: portPtr(443)}},
			}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsRule(tt.newRule, tt.existing, egressRuleEqual)
			if got != tt.want {
				t.Fatalf("rule exists = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContainsRuleIngress(t *testing.T) {
	tcp := protocolPtr(corev1.ProtocolTCP)

	clientPeer := networkingv1.NetworkPolicyPeer{
		NamespaceSelector: labelSelector(map[string]string{namespaceLabelKey: "client-ns"}),
		PodSelector:       labelSelector(map[string]string{"app": "client"}),
	}
	proxyPeer := networkingv1.NetworkPolicyPeer{
		NamespaceSelector: labelSelector(map[string]string{namespaceLabelKey: "proxy-ns"}),
		PodSelector:       labelSelector(map[string]string{"app": "proxy"}),
	}

	ruleAB := networkingv1.NetworkPolicyIngressRule{
		From: []networkingv1.NetworkPolicyPeer{clientPeer, proxyPeer},
		Ports: []networkingv1.NetworkPolicyPort{
			{Protocol: tcp, Port: portPtr(80)},
			{Protocol: tcp, Port: portPtr(443), EndPort: int32Ptr(445)},
		},
	}
	ruleBA := networkingv1.NetworkPolicyIngressRule{
		From: []networkingv1.NetworkPolicyPeer{proxyPeer, clientPeer},
		Ports: []networkingv1.NetworkPolicyPort{
			{Protocol: tcp, Port: portPtr(443), EndPort: int32Ptr(445)},
			{Protocol: tcp, Port: portPtr(80)},
		},
	}

	tests := []struct {
		name     string
		newRule  networkingv1.NetworkPolicyIngressRule
		existing []networkingv1.NetworkPolicyIngressRule
		want     bool
	}{
		{
			name:     "matches same order",
			newRule:  ruleAB,
			existing: []networkingv1.NetworkPolicyIngressRule{ruleAB},
			want:     true,
		},
		{
			name:     "matches different from and ports order",
			newRule:  ruleAB,
			existing: []networkingv1.NetworkPolicyIngressRule{ruleBA},
			want:     true,
		},
		{
			name: "does not match with different end port",
			newRule: networkingv1.NetworkPolicyIngressRule{
				From: ruleAB.From,
				Ports: []networkingv1.NetworkPolicyPort{
					{Protocol: tcp, Port: portPtr(80)},
					{Protocol: tcp, Port: portPtr(443), EndPort: int32Ptr(446)},
				},
			},
			existing: []networkingv1.NetworkPolicyIngressRule{ruleAB},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsRule(tt.newRule, tt.existing, ingressRuleEqual)
			if got != tt.want {
				t.Fatalf("rule exists = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeSelector(t *testing.T) {
	tests := []struct {
		name string
		a    *metav1.LabelSelector
		b    *metav1.LabelSelector
		want bool
	}{
		{
			name: "matches with expression value order changes",
			a: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "frontend"},
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "tier",
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{"web", "api"},
				}},
			},
			b: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "frontend"},
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "tier",
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{"api", "web"},
				}},
			},
			want: true,
		},
		{
			name: "does not match with different labels",
			a:    labelSelector(map[string]string{"app": "frontend"}),
			b:    labelSelector(map[string]string{"app": "backend"}),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectorEqual(tt.a, tt.b)
			if got != tt.want {
				t.Fatalf("selector equality = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIPBlockEqual(t *testing.T) {
	tests := []struct {
		name string
		a    *networkingv1.IPBlock
		b    *networkingv1.IPBlock
		want bool
	}{
		{
			name: "matches nil values",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "does not match nil and non nil",
			a:    &networkingv1.IPBlock{CIDR: "10.0.0.0/24"},
			b:    nil,
			want: false,
		},
		{
			name: "matches same cidr and except with different order",
			a: &networkingv1.IPBlock{
				CIDR:   "10.0.0.0/24",
				Except: []string{"10.0.0.8/32", "10.0.0.9/32"},
			},
			b: &networkingv1.IPBlock{
				CIDR:   "10.0.0.0/24",
				Except: []string{"10.0.0.9/32", "10.0.0.8/32"},
			},
			want: true,
		},
		{
			name: "does not match different cidr",
			a: &networkingv1.IPBlock{
				CIDR:   "10.0.0.0/24",
				Except: []string{"10.0.0.8/32"},
			},
			b: &networkingv1.IPBlock{
				CIDR:   "10.0.1.0/24",
				Except: []string{"10.0.0.8/32"},
			},
			want: false,
		},
		{
			name: "does not match different except values",
			a: &networkingv1.IPBlock{
				CIDR:   "10.0.0.0/24",
				Except: []string{"10.0.0.8/32"},
			},
			b: &networkingv1.IPBlock{
				CIDR:   "10.0.0.0/24",
				Except: []string{"10.0.0.9/32"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ipBlockEqual(tt.a, tt.b)
			if got != tt.want {
				t.Fatalf("ipBlockEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPolicyPeerEqual(t *testing.T) {
	tests := []struct {
		name string
		a    networkingv1.NetworkPolicyPeer
		b    networkingv1.NetworkPolicyPeer
		want bool
	}{
		{
			name: "matches selectors with expression order differences",
			a: networkingv1.NetworkPolicyPeer{
				NamespaceSelector: labelSelector(map[string]string{namespaceLabelKey: "ns-a"}),
				PodSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "frontend"},
					MatchExpressions: []metav1.LabelSelectorRequirement{{
						Key:      "tier",
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{"web", "api"},
					}},
				},
				IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.0/24", Except: []string{"10.0.0.8/32", "10.0.0.9/32"}},
			},
			b: networkingv1.NetworkPolicyPeer{
				NamespaceSelector: labelSelector(map[string]string{namespaceLabelKey: "ns-a"}),
				PodSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "frontend"},
					MatchExpressions: []metav1.LabelSelectorRequirement{{
						Key:      "tier",
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{"api", "web"},
					}},
				},
				IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.0/24", Except: []string{"10.0.0.9/32", "10.0.0.8/32"}},
			},
			want: true,
		},
		{
			name: "does not match different namespace selector",
			a: networkingv1.NetworkPolicyPeer{
				NamespaceSelector: labelSelector(map[string]string{namespaceLabelKey: "ns-a"}),
			},
			b: networkingv1.NetworkPolicyPeer{
				NamespaceSelector: labelSelector(map[string]string{namespaceLabelKey: "ns-b"}),
			},
			want: false,
		},
		{
			name: "does not match different ip block",
			a: networkingv1.NetworkPolicyPeer{
				IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.0/24"},
			},
			b: networkingv1.NetworkPolicyPeer{
				IPBlock: &networkingv1.IPBlock{CIDR: "10.0.1.0/24"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := policyPeerEqual(tt.a, tt.b)
			if got != tt.want {
				t.Fatalf("policyPeerEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPolicyPortEqual(t *testing.T) {
	tcp := protocolPtr(corev1.ProtocolTCP)
	udp := protocolPtr(corev1.ProtocolUDP)

	tests := []struct {
		name string
		a    networkingv1.NetworkPolicyPort
		b    networkingv1.NetworkPolicyPort
		want bool
	}{
		{
			name: "matches same fields",
			a: networkingv1.NetworkPolicyPort{
				Protocol: tcp,
				Port:     portPtr(443),
			},
			b: networkingv1.NetworkPolicyPort{
				Protocol: tcp,
				Port:     portPtr(443),
			},
			want: true,
		},
		{
			name: "does not match different protocol",
			a: networkingv1.NetworkPolicyPort{
				Protocol: tcp,
				Port:     portPtr(443),
			},
			b: networkingv1.NetworkPolicyPort{
				Protocol: udp,
				Port:     portPtr(443),
			},
			want: false,
		},
		{
			name: "does not match different end port",
			a: networkingv1.NetworkPolicyPort{
				Protocol: tcp,
				Port:     portPtr(443),
				EndPort:  int32Ptr(445),
			},
			b: networkingv1.NetworkPolicyPort{
				Protocol: tcp,
				Port:     portPtr(443),
				EndPort:  int32Ptr(446),
			},
			want: false,
		},
		{
			name: "matches named port",
			a: networkingv1.NetworkPolicyPort{
				Protocol: tcp,
				Port:     &intstr.IntOrString{Type: intstr.String, StrVal: "http"},
			},
			b: networkingv1.NetworkPolicyPort{
				Protocol: tcp,
				Port:     &intstr.IntOrString{Type: intstr.String, StrVal: "http"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, policyPortEqual(tt.a, tt.b))
		})
	}
}
