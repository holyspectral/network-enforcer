package e2e_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/util/intstr"
	securityv1alpha1 "secuity.rancher.io/network-enforcer/api/v1alpha1"
)

// todo!: Add other cases
// - service different nodes.
// - deployment communication without service same nodes.
// - deployment communication without service different nodes.
// - external traffic
// - internal traffic through NodePort service.
func TestSimpleAppConnectivity(t *testing.T) {
	feature := features.New("Service same node").
		Setup(setupSharedK8sClient).
		Setup(setupTestNamespace).
		Setup(setupSimpleAppWorkload).
		Setup(generateTraffic).
		Assess("Check if policy proposals are generated", assessPolicyProposalsGenerated).
		Teardown(teardownSimpleAppWorkload).
		Teardown(teardownTestNamespace).
		Feature()

	testEnv.Test(t, feature)
}

func generateTraffic(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
	t.Helper()
	namespace := getNamespace(ctx)

	execCtx, cancel := context.WithTimeout(ctx, defaultPodExecTimeout)
	defer cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	r := getClient(ctx)
	err := r.ExecInDeployment(
		execCtx,
		namespace,
		simpleAppClientDeploymentName,
		[]string{"curl", "--silent", "--show-error", "--fail", "http://http-service"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err, "failed executing command in pod %q: %v", simpleAppClientDeploymentName, err)
	require.Empty(t, stderr.String(), "expected non-empty output from curl command")
	return ctx
}

func assessPolicyProposalsGenerated(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
	t.Helper()
	namespace := getNamespace(ctx)

	tcpProtocol := corev1.ProtocolTCP
	udpProtocol := corev1.ProtocolUDP
	dstPort := intstr.FromInt(80)
	dnsPort := intstr.FromInt(53)

	expectedClientEgressProposal := securityv1alpha1.NetworkPolicyProposal{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "deployment-" + simpleAppClientDeploymentName + "-egress",
			Namespace: namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": simpleAppClientDeploymentName},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Port:     &dstPort,
							Protocol: &tcpProtocol,
						},
					},
					To: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"kubernetes.io/metadata.name": namespace},
							},
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": simpleAppServerDeploymentName},
							},
						},
					},
				},
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Port:     &dnsPort,
							Protocol: &udpProtocol,
						},
					},
					To: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"},
							},
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"k8s-app": "kube-dns"},
							},
						},
					},
				},
			},
		},
	}
	expectedServerIngressProposal := securityv1alpha1.NetworkPolicyProposal{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "deployment-" + simpleAppServerDeploymentName + "-ingress",
			Namespace: namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": simpleAppServerDeploymentName},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"kubernetes.io/metadata.name": namespace},
							},
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": simpleAppClientDeploymentName},
							},
						},
					},
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Port:     &dstPort,
							Protocol: &tcpProtocol,
						},
					},
				},
			},
		},
	}

	var proposals securityv1alpha1.NetworkPolicyProposalList
	require.Eventually(t, func() bool {
		err := getClient(ctx).WithNamespace(namespace).List(ctx, &proposals)
		require.NoError(t, err, "failed to list network policy proposals")

		foundClientEgress := false
		foundServerIngress := false
		for _, proposal := range proposals.Items {
			switch proposal.Name {
			case expectedClientEgressProposal.Name:
				foundClientEgress = true
			case expectedServerIngressProposal.Name:
				foundServerIngress = true
			default:
				continue
			}
		}
		return foundClientEgress && foundServerIngress
	}, defaultOperationTimeout, 3*time.Second, "expected policy proposals were not generated")

	require.Len(t, proposals.Items, 2, "expected exactly 2 policy proposals to be generated")
	for _, proposal := range proposals.Items {
		switch proposal.Name {
		case expectedClientEgressProposal.Name:
			requireEqualNetworkPolicyProposal(t, expectedClientEgressProposal,
				proposal)
		case expectedServerIngressProposal.Name:
			requireEqualNetworkPolicyProposal(t, expectedServerIngressProposal,
				proposal)
		}
	}
	return ctx
}
