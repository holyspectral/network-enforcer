package e2e_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient/decoder"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
)

const (
	testFolder                    = "./testdata"
	simpleAppManifest             = "simple_app.yaml"
	simpleAppClientDeploymentName = "http-client"
	simpleAppServerDeploymentName = "http-server"
	simpleAppContainerName        = "curl-client"
	simpleAppClientPodName        = "simple-app-client"
	simpleAppServerPodName        = "simple-app-server"
)

func teardownSimpleAppWorkload(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
	t.Helper()
	namespace := getNamespace(ctx)

	err := decoder.DeleteWithManifestDir(
		ctx,
		getClient(ctx),
		testFolder,
		simpleAppManifest,
		[]resources.DeleteOption{},
		decoder.MutateNamespace(namespace),
	)
	require.NoError(t, err, "failed to delete simple app manifest")

	clientDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: simpleAppClientDeploymentName, Namespace: namespace},
	}
	err = wait.For(
		conditions.New(getClient(ctx)).ResourceDeleted(clientDeployment),
		wait.WithTimeout(defaultOperationTimeout),
	)
	require.NoError(t, err, "wait client deployment deletion")

	serverDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: simpleAppServerDeploymentName, Namespace: namespace},
	}
	err = wait.For(
		conditions.New(getClient(ctx)).ResourceDeleted(serverDeployment),
		wait.WithTimeout(defaultOperationTimeout),
	)
	require.NoError(t, err, "wait server deployment deletion")

	return ctx
}

func setupSimpleAppWorkload(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
	t.Helper()
	t.Log("installing simple app")
	namespace := getNamespace(ctx)

	err := decoder.ApplyWithManifestDir(
		ctx,
		getClient(ctx),
		testFolder,
		simpleAppManifest,
		[]resources.CreateOption{},
		// we should mutate the nodeSelector here, since we want them both on the same node and on different nodes.
		decoder.MutateNamespace(namespace),
	)
	require.NoError(t, err, "failed to apply simple app manifest")

	err = wait.For(
		conditions.New(getClient(ctx)).DeploymentAvailable(simpleAppClientDeploymentName, namespace),
		wait.WithTimeout(defaultOperationTimeout),
	)
	require.NoError(t, err, "wait client deployment ready")

	err = wait.For(
		conditions.New(getClient(ctx)).DeploymentAvailable(simpleAppServerDeploymentName, namespace),
		wait.WithTimeout(defaultOperationTimeout),
	)
	require.NoError(t, err, "wait server deployment ready")
	return ctx
}
