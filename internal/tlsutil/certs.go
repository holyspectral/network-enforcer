package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/grpc/credentials"
)

const (
	// CertFile is the standard file name for a TLS certificate.
	CertFile = "tls.crt"
	// KeyFile is the standard file name for a TLS private key.
	KeyFile = "tls.key"
	// CAFile is the standard file name for a CA certificate.
	CAFile = "ca.crt"
)

// LoadCACertPool reads PEM-encoded CA certificates from the given path and
// returns an [x509.CertPool] containing it.
// It supports certificate rotation when called on each handshake.
func LoadCACertPool(path string) (*x509.CertPool, error) {
	caPem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate %s: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPem) {
		return nil, fmt.Errorf("failed to parse CA certificate from %s", path)
	}
	return pool, nil
}

// LoadKeyPair loads a TLS certificate and private key from the given paths.
func LoadKeyPair(certPath, keyPath string) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to load key pair (%s, %s): %w", certPath, keyPath, err)
	}
	return cert, nil
}

// ValidateCertDir checks that dirPath exists and contains a loadable TLS key
// pair (tls.crt + tls.key). It is intended for fail-fast validation at
// startup before any connections are attempted.
func ValidateCertDir(dirPath string) error {
	if dirPath == "" {
		return errors.New("certificate directory path is empty")
	}
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return fmt.Errorf("certificate directory does not exist: %w", err)
	}
	_, err := LoadKeyPair(
		filepath.Join(dirPath, CertFile),
		filepath.Join(dirPath, KeyFile),
	)
	return err
}

// ServerCredentials creates gRPC transport credentials for server-side mTLS.
// It loads the server certificate and key, and configures client certificate
// verification against the CA pool from the given certDir.
func ServerCredentials(certDir string) (credentials.TransportCredentials, error) {
	if err := ValidateCertDir(certDir); err != nil {
		return nil, fmt.Errorf("invalid cert dir: %w", err)
	}

	certPath := filepath.Join(certDir, CertFile)
	keyPath := filepath.Join(certDir, KeyFile)
	caPath := filepath.Join(certDir, CAFile)

	serverCert, err := LoadKeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load server key pair: %w", err)
	}

	caPool, err := LoadCACertPool(caPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load CA certificate pool: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		MinVersion:   tls.VersionTLS13,
	}
	return credentials.NewTLS(tlsConfig), nil
}

// ClientCredentials creates gRPC transport credentials for client-side mTLS.
// It loads the client certificate and key, and configures server certificate
// verification against the CA pool. The serverName is used for TLS SNI and
// certificate hostname verification.
func ClientCredentials(certDir, serverName string) (credentials.TransportCredentials, error) {
	if err := ValidateCertDir(certDir); err != nil {
		return nil, fmt.Errorf("invalid cert dir: %w", err)
	}

	certPath := filepath.Join(certDir, CertFile)
	keyPath := filepath.Join(certDir, KeyFile)
	caPath := filepath.Join(certDir, CAFile)

	pool, err := LoadCACertPool(caPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load CA certificate pool: %w", err)
	}

	clientCert, err := LoadKeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load client key pair: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13,
		ServerName:   serverName,
	}
	return credentials.NewTLS(tlsConfig), nil
}
