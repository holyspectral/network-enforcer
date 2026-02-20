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
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	securityv1alpha1 "secuity.rancher.io/network-enforcer/api/v1alpha1"
	"secuity.rancher.io/network-enforcer/internal/backend"
)

const enforceLabelKey = "security.rancher.io/enforce"

type EnforcementReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Backend backend.PolicyBackend
}

// +kubebuilder:rbac:groups=security.security.rancher.io,resources=networkpolicyproposals,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=security.security.rancher.io,resources=networkpolicyproposals/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets;daemonsets,verbs=get;list;watch

func (r *EnforcementReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var proposal securityv1alpha1.NetworkPolicyProposal
	if err := r.Get(ctx, req.NamespacedName, &proposal); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	enforced := proposal.Labels[enforceLabelKey] == "true"
	policyName := fmt.Sprintf("npp-%s", proposal.Name)

	if enforced {
		podSelector, err := r.lookupPodSelector(ctx, proposal.Namespace, proposal.Spec.WorkloadRef)
		if err != nil {
			return ctrl.Result{}, err
		}

		desired := r.Backend.Build(policyName, proposal.Namespace, podSelector, &proposal)
		if err := ctrl.SetControllerReference(&proposal, desired, r.Scheme); err != nil {
			return ctrl.Result{}, fmt.Errorf("setting owner ref: %w", err)
		}

		existing := r.Backend.Empty()
		err = r.Get(ctx, types.NamespacedName{Name: policyName, Namespace: proposal.Namespace}, existing)
		if apierrors.IsNotFound(err) {
			if err := r.Create(ctx, desired); err != nil {
				return ctrl.Result{}, err
			}
			log.Info("created policy", "backend", r.Backend.Name(), "name", policyName)
		} else if err != nil {
			return ctrl.Result{}, err
		} else {
			r.Backend.UpdateSpec(existing, desired)
			if err := r.Update(ctx, existing); err != nil {
				return ctrl.Result{}, err
			}
			log.Info("updated policy", "backend", r.Backend.Name(), "name", policyName)
		}

		proposal.Status.GeneratedPolicyName = policyName
		if err := r.Status().Update(ctx, &proposal); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		existing := r.Backend.Empty()
		err := r.Get(ctx, types.NamespacedName{Name: policyName, Namespace: proposal.Namespace}, existing)
		if err == nil {
			if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			log.Info("deleted policy", "backend", r.Backend.Name(), "name", policyName)
		} else if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		if proposal.Status.GeneratedPolicyName != "" {
			proposal.Status.GeneratedPolicyName = ""
			if err := r.Status().Update(ctx, &proposal); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *EnforcementReconciler) lookupPodSelector(ctx context.Context, namespace string, ref securityv1alpha1.WorkloadReference) (map[string]string, error) {
	var labels map[string]string

	switch ref.Kind {
	case "Deployment":
		var deploy appsv1.Deployment
		if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: namespace}, &deploy); err != nil {
			return nil, fmt.Errorf("looking up Deployment %s/%s: %w", namespace, ref.Name, err)
		}
		labels = deploy.Spec.Selector.MatchLabels
	case "StatefulSet":
		var sts appsv1.StatefulSet
		if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: namespace}, &sts); err != nil {
			return nil, fmt.Errorf("looking up StatefulSet %s/%s: %w", namespace, ref.Name, err)
		}
		labels = sts.Spec.Selector.MatchLabels
	case "DaemonSet":
		var ds appsv1.DaemonSet
		if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: namespace}, &ds); err != nil {
			return nil, fmt.Errorf("looking up DaemonSet %s/%s: %w", namespace, ref.Name, err)
		}
		labels = ds.Spec.Selector.MatchLabels
	default:
		return nil, fmt.Errorf("unsupported workload kind: %s", ref.Kind)
	}

	return labels, nil
}

func (r *EnforcementReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&securityv1alpha1.NetworkPolicyProposal{}).
		WithEventFilter(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldLabels := e.ObjectOld.GetLabels()
				newLabels := e.ObjectNew.GetLabels()
				return oldLabels[enforceLabelKey] != newLabels[enforceLabelKey]
			},
			CreateFunc:  func(e event.CreateEvent) bool { return true },
			DeleteFunc:  func(e event.DeleteEvent) bool { return false },
			GenericFunc: func(e event.GenericEvent) bool { return false },
		}).
		Named("enforcement").
		Complete(r)
}
