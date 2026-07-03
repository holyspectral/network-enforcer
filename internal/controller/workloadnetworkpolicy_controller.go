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

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// WorkloadNetworkPolicyReconciler reconciles WorkloadNetworkPolicy resources.
//
// In this first iteration the type, scheme registration and RBAC are wired
// up; the actual reconciliation loop (mode-aware enforcement, status
// reporting) is intentionally deferred to a follow-up change.
type WorkloadNetworkPolicyReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=security.rancher.io,resources=workloadnetworkpolicies,verbs=get;list;watch;create;update;patch;delete

// Reconcile is a placeholder. The real reconciliation logic — selecting
// between monitor and protect mode, building the resulting
// networkingv1.NetworkPolicy, and reporting status — will be implemented in
// a follow-up change.
func (r *WorkloadNetworkPolicyReconciler) Reconcile(_ context.Context, _ reconcile.Request) (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

// SetupWithManager is a placeholder. It will be filled in once the
// reconciliation logic for WorkloadNetworkPolicy lands.
func (r *WorkloadNetworkPolicyReconciler) SetupWithManager(_ manager.Manager) error {
	return nil
}
