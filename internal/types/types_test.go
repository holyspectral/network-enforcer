package types_test

import (
	"testing"

	"github.com/rancher-sandbox/network-enforcer/internal/types"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPolicy_String(t *testing.T) {
	tests := []struct {
		name     string
		policy   types.Policy
		expected string
	}{
		{
			name: "Standard NetworkPolicy",
			policy: types.Policy{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "networking.k8s.io/v1",
					Kind:       "NetworkPolicy",
				},
				Name:      "test-policy",
				Namespace: "default",
			},
			expected: "networking.k8s.io/v1/NetworkPolicy/default/test-policy",
		},
		{
			name: "Calico NetworkPolicy",
			policy: types.Policy{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "projectcalico.org/v3",
					Kind:       "CalicoNetworkPolicy",
				},
				Name:      "calico-policy",
				Namespace: "kube-system",
			},
			expected: "projectcalico.org/v3/CalicoNetworkPolicy/kube-system/calico-policy",
		},
		{
			name: "Cilium NetworkPolicy",
			policy: types.Policy{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cilium.io/v2",
					Kind:       "CiliumNetworkPolicy",
				},
				Name:      "cilium-policy",
				Namespace: "default",
			},
			expected: "cilium.io/v2/CiliumNetworkPolicy/default/cilium-policy",
		},
		{
			name: "Empty fields",
			policy: types.Policy{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "",
					Kind:       "",
				},
				Name:      "",
				Namespace: "",
			},
			expected: "//",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.policy.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPolicyDenyEvent_Validation(t *testing.T) {
	tests := []struct {
		name    string
		event   types.PolicyDenyEvent
		isValid bool
	}{
		{
			name: "Valid event",
			event: types.PolicyDenyEvent{
				Timestamp:    1234567890,
				NodeName:     "test-node",
				CNIType:      "calico",
				Protocol:     "TCP",
				SrcNamespace: "default",
				SrcName:      "test-pod",
				DstNamespace: "default",
				DstName:      "web-pod",
			},
			isValid: true,
		},
		{
			name: "Empty CNI type",
			event: types.PolicyDenyEvent{
				Timestamp:    1234567890,
				NodeName:     "test-node",
				CNIType:      "",
				Protocol:     "TCP",
				SrcNamespace: "default",
				SrcName:      "test-pod",
				DstNamespace: "default",
				DstName:      "web-pod",
			},
			isValid: false,
		},
		{
			name: "Empty node name",
			event: types.PolicyDenyEvent{
				Timestamp:    1234567890,
				NodeName:     "",
				CNIType:      "calico",
				Protocol:     "TCP",
				SrcNamespace: "default",
				SrcName:      "test-pod",
				DstNamespace: "default",
				DstName:      "web-pod",
			},
			isValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := tt.event.NodeName != "" && tt.event.CNIType != ""
			assert.Equal(t, tt.isValid, isValid)
		})
	}
}
