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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	securityv1alpha1 "secuity.rancher.io/network-enforcer/api/v1alpha1"
	"secuity.rancher.io/network-enforcer/internal/topology"
)

var _ = Describe("NetworkPolicyProposal Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		networkpolicyproposal := &securityv1alpha1.NetworkPolicyProposal{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind NetworkPolicyProposal")
			err := k8sClient.Get(ctx, typeNamespacedName, networkpolicyproposal)
			if err != nil && errors.IsNotFound(err) {
				resource := &securityv1alpha1.NetworkPolicyProposal{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: securityv1alpha1.NetworkPolicyProposalSpec{
						WorkloadRef: securityv1alpha1.WorkloadReference{
							Kind: "Deployment",
							Name: "test-app",
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &securityv1alpha1.NetworkPolicyProposal{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance NetworkPolicyProposal")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile with no flows", func() {
			store := topology.NewStore()
			controllerReconciler := &NetworkPolicyProposalReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Store:  store,
			}

			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(30 * time.Second))
		})

		It("should update proposal spec when flows exist", func() {
			store := topology.NewStore()
			now := time.Now()

			wk := topology.WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "test-app"}
			store.Record(topology.FlowRecord{
				Source:    topology.WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "client"},
				Dest:      wk,
				DstPort:   8080,
				Protocol:  "TCP",
				FirstSeen: now,
				LastSeen:  now,
				ByteCount: 100,
			})

			controllerReconciler := &NetworkPolicyProposalReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Store:  store,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify the proposal was updated
			updated := &securityv1alpha1.NetworkPolicyProposal{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Spec.Ingress).To(HaveLen(1))
			Expect(updated.Spec.Ingress[0].Ports[0].Port).To(Equal(int32(8080)))
		})
	})
})
