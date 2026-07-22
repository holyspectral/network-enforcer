package e2e_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
	"sigs.k8s.io/e2e-framework/support/kind"
	"sigs.k8s.io/e2e-framework/third_party/helm"
)

var (
	testEnv env.Environment //nolint:gochecknoglobals // provided by e2e-framework
)

func TestMain(m *testing.M) {
	testSuiteConf := loadSuiteConfig()

	cfg, _ := envconf.NewFromFlags()
	testEnv = env.NewWithConfig(cfg)

	clusterName := envconf.RandomName(testSuiteConf.namespacePrefix, 20)

	setupFuncs := []env.Func{
		envfuncs.CreateClusterWithConfig(kind.NewProvider(), clusterName, testSuiteConf.kindConfigPath),
		envfuncs.LoadImageToCluster(clusterName, testSuiteConf.image),
		installCNI(testSuiteConf.cni),
		installCertManager(),
		installNetEnforcerChart(&testSuiteConf),
	}

	testEnv.Setup(setupFuncs...)

	exitCode := testEnv.Run(m)
	if exitCode == 0 {
		if err := deleteKindCluster(clusterName); err != nil {
			fmt.Fprintf(os.Stderr, "failed to delete kind cluster %q after success: %v\n", clusterName, err)
			exitCode = 1
		}
	} else {
		fmt.Fprintf(os.Stderr, "tests failed: keeping kind cluster %q for investigation\n", clusterName)
	}

	os.Exit(exitCode)
}

func installNetEnforcerChart(testCfg *suiteConfig) env.Func {
	return func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
		manager := helm.New(cfg.KubeconfigFile())

		repo, tag := parseImage(testCfg.image)

		helmOpts := []helm.Option{
			helm.WithName(testCfg.releaseName),
			helm.WithNamespace(testCfg.releaseNS),
			helm.WithChart(testCfg.chartPath),
			helm.WithArgs("--create-namespace"),
			helm.WithArgs("--set", fmt.Sprintf("controller.image.repository=%s", repo)),
			helm.WithArgs("--set", fmt.Sprintf("controller.image.tag=%s", tag)),
			// we reduce the time here to have faster feedback
			helm.WithArgs("--set", "obi.config.data.otel_metrics_export.interval=3s"),
			helm.WithArgs("--set", "controller.drainFlowsInterval=3s"),

			helm.WithWait(),
			helm.WithTimeout(defaultHelmTimeout.String()),
		}

		if testCfg.cni == kindnet {
			// with kindnet we don't need the cniwatcher
			helmOpts = append(helmOpts, helm.WithArgs("--set", "cniwatcher.enabled=false"))
		}

		if err := manager.RunInstall(helmOpts...); err != nil {
			return ctx, fmt.Errorf("install network enforcer chart: %w", err)
		}

		// Wait the Controller to be ready
		r, err := resources.New(cfg.Client().RESTConfig())
		if err != nil {
			return ctx, fmt.Errorf("create resources client: %w", err)
		}

		err = wait.For(
			conditions.New(r).DeploymentAvailable("network-enforcer-controller-manager", testCfg.releaseNS),
			wait.WithTimeout(defaultOperationTimeout),
		)
		if err != nil {
			return ctx, fmt.Errorf("wait network enforcer deployment ready: %w", err)
		}

		return ctx, nil
	}
}

func parseImage(image string) (string, string) {
	if i := strings.LastIndex(image, ":"); i > 0 && i > strings.LastIndex(image, "/") {
		return image[:i], image[i+1:]
	}
	return image, "latest"
}

func deleteKindCluster(clusterName string) error {
	cmd := exec.Command("kind", "delete", "cluster", "--name", clusterName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"kind delete cluster --name %s: %w (output: %s)",
			clusterName,
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return nil
}
