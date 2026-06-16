package e2e_test

import (
	"os"
	"strings"
	"time"
)

const (
	defaultChartPath     = "../../charts/network-enforcer"
	defaultLogsDir       = "./logs"
	defaultImage         = "ghcr.io/rancher-sandbox/network-enforcer/controller:latest"
	defaultReleaseName   = "network-enforcer"
	defaultReleaseNS     = "network-enforcer"
	defaultNamespacePref = "network-enforcer-e2e"
	defaultCNI           = kindnet

	kindnetConfigPath = "./clusters/kindnet.yaml"
	noCNIConfigPath   = "./clusters/no-cni.yaml"
)

const (
	defaultHelmTimeout      = 3 * time.Minute
	defaultOperationTimeout = 2 * time.Minute
	defaultPodExecTimeout   = 45 * time.Second
)

type suiteConfig struct {
	kindConfigPath  string
	logsDir         string
	chartPath       string
	releaseName     string
	releaseNS       string
	image           string
	namespacePrefix string
	cni             cniType
}

func loadSuiteConfig() suiteConfig {
	conf := suiteConfig{
		logsDir:         defaultLogsDir,
		chartPath:       defaultChartPath,
		releaseName:     defaultReleaseName,
		releaseNS:       defaultReleaseNS,
		image:           defaultImage,
		namespacePrefix: defaultNamespacePref,
		cni:             cniType(readEnvOrDefault("E2E_CNI", string(defaultCNI))),
	}

	//nolint:exhaustive // all cases there are not kindnet should use the noCNIConfigPath
	switch conf.cni {
	case kindnet:
		conf.kindConfigPath = kindnetConfigPath
	default:
		conf.kindConfigPath = noCNIConfigPath
	}
	return conf
}

func readEnvOrDefault(name, defaultValue string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return defaultValue
	}
	return value
}
