package e2e_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	securityv1alpha1 "github.com/kubewarden/network-enforcer/api/v1alpha1"
)

// TestExternalEndpointViolations validates that egress to non-workload
// (External) destinations which are not part of the learned policy is blocked
// in protect mode and recorded as a violation with an External endpoint. It
// only runs for the CNI-based providers (cilium/calico), which classify such
// peers as reserved identities / host endpoints.
func TestExternalEndpointViolations(t *testing.T) {
	if !loadSuiteConfig().IsKubernetesProvider() {
		t.Skip("Skipping external endpoint test: selected provider is not cilium or calico")
	}

	feature := features.New("External endpoint protect-mode violations").
		Setup(setupTestNamespace).
		Setup(setupSimpleAppWorkload).
		Assess("API server egress is blocked and reported as External",
			assessAPIServerEgressViolationReported).
		Assess("Generic external egress is blocked and reported as External",
			assessGenericExternalEgressViolationReported).
		Teardown(teardownSimpleAppWorkload).
		Teardown(teardownTestNamespace).
		Feature()

	testEnv.Test(t, feature)
}

// curlBlockedFromClient tries to reach dest:443 over TLS from the client
// deployment and asserts the connection is blocked. Since neither the API
// server nor any node/host destination is part of the learned egress
// policy, protect mode must already reject these connections regardless of
// the CNI in use; curl either exits non-zero or returns no HTTP status code.
func curlBlockedFromClient(ctx context.Context, t *testing.T, dest string) {
	t.Helper()

	cmd := []string{
		"sh", "-c",
		fmt.Sprintf("curl -sk -m 3 -o /dev/null -w '%%{http_code}' https://%s:443/", dest),
	}
	require.Eventually(t, func() bool {
		stdout, _, err := execInSimpleClientDeploymentRaw(ctx, cmd)
		if err == nil && stdout != "" && stdout != "000" {
			t.Logf("egress to %q still succeeding (http status %q)", dest, stdout)
			return false
		}
		return true
	}, defaultOperationTimeout, 1*time.Second, "egress to %q should be blocked", dest)
}

// hasExternalProtectViolation reports whether any WorkloadNetworkPolicy in
// namespace has a protect-mode violation whose source or dest is an
// External (non-workload) endpoint.
func hasExternalProtectViolation(ctx context.Context, namespace string) (bool, error) {
	var policies securityv1alpha1.WorkloadNetworkPolicyList
	if err := getSecurityV1Alpha1Client(ctx).WithNamespace(namespace).List(ctx, &policies); err != nil {
		return false, err
	}
	for _, policy := range policies.Items {
		for _, v := range policy.Status.Violations {
			if v.Action != securityv1alpha1.WorkloadNetworkPolicyModeProtect {
				continue
			}
			if v.Source.External || v.Dest.External {
				return true, nil
			}
		}
	}
	return false, nil
}

// assessAPIServerEgressViolationReported exercises the live-debugging
// scenario: a workload whose learned/promoted policy does not include the
// Kubernetes API server tries to reach it via the short Service name, the
// FQDN, and the raw ClusterIP. All three ultimately hit the same API server
// ClusterIP but exercise different classification paths (Cilium's
// reserved:kube-apiserver identity is independent of addressing method, while
// Calico's DestServiceName correlation is addressing-method-sensitive), so
// all three are attempted. Egress must already be blocked (protect mode,
// unrelated to this change) and must now also produce a recorded violation
// with an External destination, instead of being silently dropped.
func assessAPIServerEgressViolationReported(
	ctx context.Context, t *testing.T, _ *envconf.Config,
) context.Context {
	t.Helper()
	namespace := getNamespace(ctx)

	var kubernetesService corev1.Service
	require.NoError(t, getSecurityV1Alpha1Client(ctx).Get(ctx, "kubernetes", "default", &kubernetesService),
		"failed to get the kubernetes Service")
	clusterIP := kubernetesService.Spec.ClusterIP
	require.NotEmpty(t, clusterIP, "kubernetes Service has no ClusterIP")

	for _, dest := range []string{
		"kubernetes",                           // short name
		"kubernetes.default.svc.cluster.local", // FQDN
		clusterIP,                              // raw ClusterIP
	} {
		curlBlockedFromClient(ctx, t, dest)
	}

	require.Eventually(t, func() bool {
		found, err := hasExternalProtectViolation(ctx, namespace)
		if err != nil {
			t.Logf("failed to list network policies: %v", err)
			return false
		}
		return found
	}, defaultOperationTimeout, 1*time.Second,
		"expected a protect-mode violation with an External endpoint to be recorded for the API server egress")

	return ctx
}

// assessGenericExternalEgressViolationReported confirms that External
// classification is not accidentally API-server-only: egress to any other
// non-workload destination (here, a cluster node, reachable as a Cilium
// reserved:host/remote-node identity or a Calico HostEndpoint) that isn't
// part of the learned policy is also blocked and reported.
func assessGenericExternalEgressViolationReported(
	ctx context.Context, t *testing.T, _ *envconf.Config,
) context.Context {
	t.Helper()
	namespace := getNamespace(ctx)

	var nodes corev1.NodeList
	require.NoError(t, getSecurityV1Alpha1Client(ctx).List(ctx, &nodes), "failed to list nodes")
	require.NotEmpty(t, nodes.Items, "expected at least one node")

	var nodeIP string
	for _, addr := range nodes.Items[0].Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			nodeIP = addr.Address
			break
		}
	}
	require.NotEmpty(t, nodeIP, "expected node to have an internal IP")

	curlBlockedFromClient(ctx, t, nodeIP)

	require.Eventually(t, func() bool {
		found, err := hasExternalProtectViolation(ctx, namespace)
		if err != nil {
			t.Logf("failed to list network policies: %v", err)
			return false
		}
		return found
	}, defaultOperationTimeout, 1*time.Second,
		"expected a protect-mode violation with an External endpoint to be recorded for the node egress")

	return ctx
}
