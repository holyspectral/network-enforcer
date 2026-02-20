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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type WorkloadReference struct {
	// kind is the workload kind (Deployment, StatefulSet, or DaemonSet).
	// +kubebuilder:validation:Enum=Deployment;StatefulSet;DaemonSet
	Kind string `json:"kind"`

	// name is the workload name.
	Name string `json:"name"`
}

type PolicyPeer struct {
	// workload identifies a Kubernetes workload peer.
	// +optional
	Workload *WorkloadReference `json:"workload,omitempty"`

	// namespace is the namespace of the peer workload.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// cidr identifies an external peer by IP range (e.g. "10.0.0.1/32").
	// +optional
	CIDR string `json:"cidr,omitempty"`
}

type PortRule struct {
	// protocol is the transport protocol (TCP or UDP).
	// +kubebuilder:validation:Enum=TCP;UDP
	Protocol string `json:"protocol"`

	// port is the destination port number.
	Port int32 `json:"port"`
}

type ProposedRule struct {
	// peers is the list of network peers for this rule.
	Peers []PolicyPeer `json:"peers"`

	// ports is the list of allowed port/protocol combinations.
	Ports []PortRule `json:"ports"`
}

type NetworkPolicyProposalSpec struct {
	// workloadRef identifies the workload this proposal covers.
	WorkloadRef WorkloadReference `json:"workloadRef"`

	// ingress is the list of proposed ingress rules.
	// +optional
	Ingress []ProposedRule `json:"ingress,omitempty"`

	// egress is the list of proposed egress rules.
	// +optional
	Egress []ProposedRule `json:"egress,omitempty"`
}

type NetworkPolicyProposalStatus struct {
	// conditions represent the current state of the proposal.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// generatedPolicyName is the name of the network policy created from this proposal.
	// +optional
	GeneratedPolicyName string `json:"generatedPolicyName,omitempty"`

	// firstObserved is when flows for this workload were first seen.
	// +optional
	FirstObserved *metav1.Time `json:"firstObserved,omitempty"`

	// lastObserved is when flows for this workload were last seen.
	// +optional
	LastObserved *metav1.Time `json:"lastObserved,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=npp
// +kubebuilder:printcolumn:name="Workload",type=string,JSONPath=`.spec.workloadRef.name`
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=`.spec.workloadRef.kind`
// +kubebuilder:printcolumn:name="Enforced",type=string,JSONPath=`.metadata.labels.security\.rancher\.io/enforce`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

type NetworkPolicyProposal struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec NetworkPolicyProposalSpec `json:"spec"`

	// +optional
	Status NetworkPolicyProposalStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

type NetworkPolicyProposalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []NetworkPolicyProposal `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NetworkPolicyProposal{}, &NetworkPolicyProposalList{})
}
