/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkloadNetworkPolicyMode selects how a WorkloadNetworkPolicy is interpreted
// at runtime.
// +kubebuilder:validation:Enum=monitor;protect
type WorkloadNetworkPolicyMode string

const (
	// WorkloadNetworkPolicyModeMonitor records observed traffic against the
	// policy without enforcing it (dry-run).
	WorkloadNetworkPolicyModeMonitor WorkloadNetworkPolicyMode = "monitor"

	// WorkloadNetworkPolicyModeProtect enforces the policy on the cluster.
	WorkloadNetworkPolicyModeProtect WorkloadNetworkPolicyMode = "protect"
)

// WorkloadNetworkPolicySpec defines the desired state of a WorkloadNetworkPolicy.
type WorkloadNetworkPolicySpec struct {
	// Mode controls whether the policy is observed (monitor) or actively
	// enforced (protect). Defaults to monitor.
	// +kubebuilder:default=monitor
	// +optional
	Mode WorkloadNetworkPolicyMode `json:"mode,omitempty"`

	// PolicyTemplate is the embedded networking.k8s.io NetworkPolicySpec that
	// this resource represents at runtime. The semantics of the policy are
	// selected by Mode; the spec itself is identical to a NetworkPolicySpec.
	// +required
	PolicyTemplate networkingv1.NetworkPolicySpec `json:"policyTemplate"`
}

// WorkloadNetworkPolicy is the schema for the runtime network policy API.
// It wraps a standard networkingv1.NetworkPolicySpec and selects a mode
// (monitor or protect). The resource is intentionally namespaced and uses
// the `security.rancher.io` group to avoid colliding with the upstream
// `networking.k8s.io/NetworkPolicy` kind.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=wnp
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type WorkloadNetworkPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec WorkloadNetworkPolicySpec `json:"spec"`
}

// WorkloadNetworkPolicyList is a list of WorkloadNetworkPolicy.
// +kubebuilder:object:root=true
type WorkloadNetworkPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`

	Items []WorkloadNetworkPolicy `json:"items"`
}
