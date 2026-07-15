package v1alpha1

import (
	"reflect"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NamespaceLabelKey is the label that records the namespace name.
const NamespaceLabelKey = "kubernetes.io/metadata.name"

// SelectorEqual compares two label selectors for equality by formatting them.
func SelectorEqual(a, b *metav1.LabelSelector) bool {
	return metav1.FormatLabelSelector(a) == metav1.FormatLabelSelector(b)
}

// IPBlockEqual compares two IPBlock pointers for equality.
func IPBlockEqual(a, b *networkingv1.IPBlock) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.CIDR != b.CIDR {
		return false
	}
	return EqualUnordered(a.Except, b.Except, func(left, right string) bool {
		return left == right
	})
}

// PolicyPeerEqual compares two NetworkPolicyPeer for equality.
func PolicyPeerEqual(a, b networkingv1.NetworkPolicyPeer) bool {
	return SelectorEqual(a.NamespaceSelector, b.NamespaceSelector) &&
		SelectorEqual(a.PodSelector, b.PodSelector) &&
		IPBlockEqual(a.IPBlock, b.IPBlock)
}

// PolicyPortEqual compares two NetworkPolicyPort for equality.
func PolicyPortEqual(a, b networkingv1.NetworkPolicyPort) bool {
	return reflect.DeepEqual(a, b)
}

// EqualUnordered checks if two slices contain the same elements regardless of order.
func EqualUnordered[T any](left, right []T, eq func(T, T) bool) bool {
	if len(left) != len(right) {
		return false
	}
	free := make([]bool, len(right))
	for i := range free {
		free[i] = true
	}
	for _, l := range left {
		match := -1
		for j, r := range right {
			if free[j] && eq(l, r) {
				match = j
				break
			}
		}
		if match < 0 {
			return false
		}
		free[match] = false
	}
	return true
}

// EgressRuleEqual compares two NetworkPolicyEgressRule for equality (order-independent).
func EgressRuleEqual(a, b networkingv1.NetworkPolicyEgressRule) bool {
	return EqualUnordered(a.To, b.To, PolicyPeerEqual) &&
		EqualUnordered(a.Ports, b.Ports, PolicyPortEqual)
}

// IngressRuleEqual compares two NetworkPolicyIngressRule for equality (order-independent).
func IngressRuleEqual(a, b networkingv1.NetworkPolicyIngressRule) bool {
	return EqualUnordered(a.From, b.From, PolicyPeerEqual) &&
		EqualUnordered(a.Ports, b.Ports, PolicyPortEqual)
}
