package e2e_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	securityv1alpha1 "secuity.rancher.io/network-enforcer/api/v1alpha1"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
)

type key string

func setupSharedK8sClient(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	t.Log("setup shared k8s client")

	r, err := resources.New(config.Client().RESTConfig())
	require.NoError(t, err, "failed to create controller runtime client")

	err = securityv1alpha1.AddToScheme(r.GetScheme())
	require.NoError(t, err)

	return context.WithValue(ctx, key("client"), r)
}

func getClient(ctx context.Context) *resources.Resources {
	return ctx.Value(key("client")).(*resources.Resources)
}

func getNamespace(ctx context.Context) string {
	return ctx.Value(key("namespace")).(string)
}

func setupTestNamespace(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
	t.Helper()
	// RandomName already adds a `-` so we need to trim it from our prefix
	testNamespace := envconf.RandomName(defaultNamespacePref, 32)
	t.Logf("creating test namespace: %q", testNamespace)
	err := getClient(ctx).Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: testNamespace,
	}})
	require.NoError(t, err, "failed to create test namespace %q", testNamespace)
	return context.WithValue(ctx, key("namespace"), testNamespace)
}

func teardownTestNamespace(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
	t.Helper()
	namespace := getNamespace(ctx)

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	err := getClient(ctx).Delete(ctx, ns)
	if err != nil && !apierrors.IsNotFound(err) {
		require.NoError(t, err, "failed to delete namespace %q", namespace)
	}

	err = wait.For(
		conditions.New(getClient(ctx)).ResourceDeleted(ns),
		wait.WithTimeout(defaultOperationTimeout),
	)
	require.NoError(t, err, "wait namespace deletion")

	return ctx
}

func requireEqualNetworkPolicyProposal(
	t *testing.T,
	expected, actual securityv1alpha1.WorkloadNetworkPolicyProposal,
) {
	// Metadata
	require.Equal(t, expected.Name, actual.Name, "network policy proposal name does not match expected")
	require.Equal(t, expected.Namespace, actual.Namespace, "network policy proposal namespace does not match expected")

	// Spec
	require.ElementsMatch(
		t,
		expected.Spec.PolicyTypes,
		actual.Spec.PolicyTypes,
		"network policy proposal policy types do not match expected",
	)
	require.Equal(
		t,
		expected.Spec.PodSelector,
		actual.Spec.PodSelector,
		"network policy proposal pod selector does not match expected",
	)
	require.ElementsMatch(
		t,
		expected.Spec.Ingress,
		actual.Spec.Ingress,
		"network policy proposal ingress rules do not match expected",
	)
	require.ElementsMatch(
		t,
		expected.Spec.Egress,
		actual.Spec.Egress,
		"network policy proposal egress rules do not match expected",
	)
}
