package grpcexporter

import (
	"fmt"
	"net"
	"strconv"

	"github.com/rancher-sandbox/network-enforcer/internal/tlsutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// AgentClientFactory creates gRPC AgentClient instances that connect to
// cniwatcher pods on a configurable port. When a cert dir is configured it
// dials with mTLS, otherwise it uses an insecure connection.
type AgentClientFactory struct {
	port    string
	certDir string
}

// AgentFactoryConfig holds the configuration for the AgentClientFactory.
type AgentFactoryConfig struct {
	// CertDirPath is the directory containing tls.crt, tls.key, and ca.crt. When
	// set, connections use mTLS; when empty they are insecure.
	CertDirPath string
	// Port is the gRPC port of the cniwatcher ScrapeViolations server.
	Port int
}

// NewAgentClientFactory validates the configuration and returns a new factory.
func NewAgentClientFactory(conf *AgentFactoryConfig) (*AgentClientFactory, error) {
	if conf.Port == 0 || conf.Port > 65535 {
		return nil, fmt.Errorf("invalid gRPC port: %d", conf.Port)
	}

	if conf.CertDirPath != "" {
		if err := tlsutil.ValidateCertDir(conf.CertDirPath); err != nil {
			return nil, fmt.Errorf("invalid certificate directory %q: %w", conf.CertDirPath, err)
		}
	}
	return &AgentClientFactory{
		port:    strconv.Itoa(conf.Port),
		certDir: conf.CertDirPath,
	}, nil
}

// getConnCredentials returns gRPC transport credentials based on the factory's
// TLS configuration. Without a cert dir it returns insecure credentials.
func (f *AgentClientFactory) getConnCredentials(serverName string) (credentials.TransportCredentials, error) {
	if f.certDir == "" {
		return insecure.NewCredentials(), nil
	}
	return tlsutil.ClientCredentials(f.certDir, serverName)
}

// NewClient creates a new AgentClient connected to the given pod IP using the
// configured port and TLS settings. It returns an error if the dial fails.
func (f *AgentClientFactory) NewClient(podIP, podName, podNamespace string) (*AgentClient, error) {
	serverName := fmt.Sprintf("%s.%s", podName, podNamespace)
	creds, err := f.getConnCredentials(serverName)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection credentials: %w", err)
	}

	host := net.JoinHostPort(podIP, f.port)
	conn, err := grpc.NewClient(host, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("grpc dial failed host %s: %w", host, err)
	}

	return NewAgentClient(conn), nil
}
