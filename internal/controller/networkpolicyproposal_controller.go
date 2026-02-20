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

package controller

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	securityv1alpha1 "secuity.rancher.io/network-enforcer/api/v1alpha1"
	"secuity.rancher.io/network-enforcer/internal/fingerprint"
	"secuity.rancher.io/network-enforcer/internal/topology"
)

type NetworkPolicyProposalReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Store  *topology.Store
}

// +kubebuilder:rbac:groups=security.security.rancher.io,resources=networkpolicyproposals,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=security.security.rancher.io,resources=networkpolicyproposals/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=security.security.rancher.io,resources=networkpolicyproposals/finalizers,verbs=update

func (r *NetworkPolicyProposalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var proposal securityv1alpha1.NetworkPolicyProposal
	if err := r.Get(ctx, req.NamespacedName, &proposal); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	wk := topology.WorkloadKey{
		Namespace: proposal.Namespace,
		OwnerKind: proposal.Spec.WorkloadRef.Kind,
		OwnerName: proposal.Spec.WorkloadRef.Name,
	}

	flows := r.Store.FlowsForWorkload(wk)
	if len(flows) == 0 {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	ingress, egress := fingerprint.Generate(wk, flows)

	proposal.Spec.Ingress = ingress
	proposal.Spec.Egress = egress

	if err := r.Update(ctx, &proposal); err != nil {
		log.Error(err, "update proposal spec")
		return ctrl.Result{}, err
	}

	now := metav1.Now()
	if proposal.Status.FirstObserved == nil {
		proposal.Status.FirstObserved = &now
	}
	proposal.Status.LastObserved = &now

	if err := r.Status().Update(ctx, &proposal); err != nil {
		log.Error(err, "update proposal status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *NetworkPolicyProposalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&securityv1alpha1.NetworkPolicyProposal{}).
		Named("networkpolicyproposal").
		Complete(r)
}
