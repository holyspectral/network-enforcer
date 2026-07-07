package kubernetes

import (
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	securityv1alpha1 "github.com/rancher-sandbox/network-enforcer/api/v1alpha1"
)

type Backend struct{}

func (b *Backend) Name() string { return "kubernetes" }

func (b *Backend) AddToScheme(_ *runtime.Scheme) error { return nil }

func (b *Backend) Empty() client.Object {
	return &networkingv1.NetworkPolicy{}
}

func (b *Backend) Build(proposal *securityv1alpha1.WorkloadNetworkPolicyProposal) client.Object {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proposal.Name,
			Namespace: proposal.Namespace,
		},
		Spec: proposal.Spec,
	}
}
