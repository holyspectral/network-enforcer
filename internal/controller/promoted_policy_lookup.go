package controller

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	securityv1alpha1 "secuity.rancher.io/network-enforcer/api/v1alpha1"
)

func hasPromotedPolicy(
	ctx context.Context,
	c client.Client,
	namespace string,
	proposalName string,
) (bool, error) {
	var policies securityv1alpha1.WorkloadNetworkPolicyList
	if err := c.List(
		ctx,
		&policies,
		client.InNamespace(namespace),
		client.MatchingLabels{securityv1alpha1.PolicyPromotedFromLabelKey: proposalName},
	); err != nil {
		return false, err
	}

	return len(policies.Items) > 0, nil
}
