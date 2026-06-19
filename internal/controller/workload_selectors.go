package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"secuity.rancher.io/network-enforcer/internal/ownerkind"
	"secuity.rancher.io/network-enforcer/internal/topology"
)

func lookupPodSelectorForWorkload(
	ctx context.Context,
	c client.Client,
	namespace string,
	kind ownerkind.Kind,
	name string,
) (metav1.LabelSelector, error) {
	switch kind { //nolint:exhaustive // we don't support all workload kinds
	case ownerkind.KindDeployment:
		var deploy appsv1.Deployment
		if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &deploy); err != nil {
			return metav1.LabelSelector{}, fmt.Errorf("looking up Deployment %s/%s: %w", namespace, name, err)
		}
		return *deploy.Spec.Selector, nil
	case ownerkind.KindStatefulSet:
		var sts appsv1.StatefulSet
		if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &sts); err != nil {
			return metav1.LabelSelector{}, fmt.Errorf("looking up StatefulSet %s/%s: %w", namespace, name, err)
		}
		return *sts.Spec.Selector, nil
	case ownerkind.KindDaemonSet:
		var ds appsv1.DaemonSet
		if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &ds); err != nil {
			return metav1.LabelSelector{}, fmt.Errorf("looking up DaemonSet %s/%s: %w", namespace, name, err)
		}
		return *ds.Spec.Selector, nil
	default:
		return metav1.LabelSelector{}, fmt.Errorf("unsupported workload kind: %s", string(kind))
	}
}

func selectorFromWorkloadKey(
	ctx context.Context,
	c client.Client,
	wk topology.WorkloadKey,
) (metav1.LabelSelector, error) {
	return lookupPodSelectorForWorkload(ctx, c, wk.Namespace, wk.OwnerKind, wk.OwnerName)
}
