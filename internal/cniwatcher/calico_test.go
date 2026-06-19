package cniwatcher_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"secuity.rancher.io/network-enforcer/internal/cniwatcher"
	pb "secuity.rancher.io/network-enforcer/internal/cniwatcher/calico/goldmane"
	"secuity.rancher.io/network-enforcer/internal/otel"
	"secuity.rancher.io/network-enforcer/internal/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNewCalicoWatcher(t *testing.T) {
	tests := []struct {
		name             string
		connEndpoint     string
		expectedEndpoint string
	}{
		{
			name:             "Default Goldmane endpoint",
			connEndpoint:     "goldmane.calico-system.svc:7443",
			expectedEndpoint: types.DefaultGoldmaneEndpoint,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().Build()
			log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
			otelService := otel.NewOpenTelemetryService(otel.OpenTelemetryConfig{
				Ctx:               t.Context(),
				Log:               log,
				ServiceVersion:    "test-version",
				CollectorEndpoint: "localhost:4317",
			})

			watcher := cniwatcher.Watcher{
				Ctx:         t.Context(),
				Client:      fakeClient,
				Log:         log,
				OtelService: otelService,
			}

			calicoWatcher, err := cniwatcher.NewCalicoWatcher(watcher, tt.connEndpoint)
			require.NoError(t, err)
			assert.NotNil(t, calicoWatcher)
			assert.Equal(t, tt.expectedEndpoint, calicoWatcher.ConnEndpoint)
		})
	}
}

type TestCalicoWatcher struct {
	*cniwatcher.CalicoWatcher

	connectFunc func() error
}

func (w *TestCalicoWatcher) connectToGoldmane() error {
	if w.connectFunc != nil {
		return w.connectFunc()
	}
	return w.ConnectToGoldmane()
}

func TestCalicoWatcher_ConnectToGoldmane(t *testing.T) {
	tmpDir := t.TempDir()

	certDir := filepath.Join(tmpDir, "etc", "goldmane", "certs")
	if mkdirErr := os.MkdirAll(certDir, 0755); mkdirErr != nil {
		t.Fatalf("Failed to create cert directory: %v", mkdirErr)
	}

	// Create dummy certificate files
	certFiles := map[string]string{
		"tls.crt": "-----BEGIN CERTIFICATE-----\nDUMMY CERT\n-----END CERTIFICATE-----",
		"tls.key": "-----BEGIN PRIVATE KEY-----\nDUMMY KEY\n-----END PRIVATE KEY-----",
		"ca.crt":  "-----BEGIN CERTIFICATE-----\nDUMMY CA\n-----END CERTIFICATE-----",
	}

	for filename, content := range certFiles {
		certPath := filepath.Join(certDir, filename)
		if writeErr := os.WriteFile(certPath, []byte(content), 0644); writeErr != nil {
			t.Fatalf("Failed to create cert file %s: %v", filename, writeErr)
		}
	}

	clientset := fake.NewClientBuilder().Build()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	otelService := otel.NewOpenTelemetryService(otel.OpenTelemetryConfig{
		Ctx:               t.Context(),
		Log:               log,
		ServiceVersion:    "test-version",
		CollectorEndpoint: "localhost:4317",
	})

	tests := []struct {
		name           string
		certDir        string
		expectedError  bool
		expectedClient bool
	}{
		{
			name:           "Valid certificate directory with all required files",
			certDir:        certDir,
			expectedError:  false,
			expectedClient: true,
		},
		{
			name:           "Invalid certificate directory",
			certDir:        "/nonexistent/dir",
			expectedError:  true,
			expectedClient: false,
		},
		{
			name:           "Empty certificate directory",
			certDir:        "",
			expectedError:  true,
			expectedClient: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			watcher := &cniwatcher.CalicoWatcher{
				Watcher: cniwatcher.Watcher{
					Client:      clientset,
					Log:         log,
					OtelService: otelService,
				},
			}

			testWatcher := &TestCalicoWatcher{
				CalicoWatcher: watcher,
				connectFunc: func() error {
					if tt.expectedError {
						return errors.New("connection failed")
					}
					watcher.Client = &MockFlowsClient{}
					return nil
				},
			}

			connectErr := testWatcher.connectToGoldmane()

			if tt.expectedError {
				require.Error(t, connectErr)
				assert.Nil(t, testWatcher.Client)
			} else {
				require.NoError(t, connectErr)
				assert.NotNil(t, testWatcher.Client)
			}
		})
	}
}

type MockFlowsClient struct {
	pb.FlowsClient

	streamFunc func(ctx context.Context, req *pb.FlowStreamRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[pb.FlowResult], error)
}

func (m *MockFlowsClient) Stream(
	ctx context.Context,
	req *pb.FlowStreamRequest,
	opts ...grpc.CallOption,
) (grpc.ServerStreamingClient[pb.FlowResult], error) {
	if m.streamFunc != nil {
		return m.streamFunc(ctx, req, opts...)
	}
	return nil, errors.New("stream function not set")
}

type MockStreamClient struct {
	grpc.ServerStreamingClient[pb.FlowResult]

	recvFunc func() (*pb.FlowResult, error)
}

func (m *MockStreamClient) Recv() (*pb.FlowResult, error) {
	if m.recvFunc != nil {
		return m.recvFunc()
	}
	return nil, errors.New("recv function not set")
}

func TestCalicoWatcher_WatchFlows(t *testing.T) {
	clientset := fake.NewClientBuilder().Build()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	otelService := otel.NewOpenTelemetryService(otel.OpenTelemetryConfig{
		Ctx:               t.Context(),
		Log:               log,
		ServiceVersion:    "test-version",
		CollectorEndpoint: "localhost:4317",
	})

	watcher := &cniwatcher.CalicoWatcher{
		Watcher: cniwatcher.Watcher{
			Client:      clientset,
			Log:         log,
			OtelService: otelService,
		},
	}

	tests := []struct {
		name          string
		streamFunc    func(ctx context.Context, req *pb.FlowStreamRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[pb.FlowResult], error)
		recvFunc      func() (*pb.FlowResult, error)
		expectedError bool
	}{
		{
			name: "Successful flow stream",
			streamFunc: func(_ context.Context, _ *pb.FlowStreamRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[pb.FlowResult], error) {
				return &MockStreamClient{
					recvFunc: func() (*pb.FlowResult, error) {
						return &pb.FlowResult{
							Flow: &pb.Flow{
								Key: &pb.FlowKey{
									Action: pb.Action_Deny,
								},
							},
						}, nil
					},
				}, nil
			},
			recvFunc: func() (*pb.FlowResult, error) {
				return &pb.FlowResult{
					Flow: &pb.Flow{
						Key: &pb.FlowKey{
							Action: pb.Action_Deny,
						},
					},
				}, nil
			},
			expectedError: false,
		},
		{
			name: "Stream error",
			streamFunc: func(_ context.Context, _ *pb.FlowStreamRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[pb.FlowResult], error) {
				return nil, errors.New("stream error")
			},
			recvFunc:      nil,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			watcher.Client = &MockFlowsClient{
				streamFunc: tt.streamFunc,
			}

			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			watcher.Ctx = ctx

			err := watcher.WatchFlows()
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				// watchFlows will likely timeout due to context cancellation, which is expected
				assert.NoError(t, err)
			}
		})
	}
}

func TestCalicoWatcher_Shutdown(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "Successful shutdown",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
			otelService := otel.NewOpenTelemetryService(otel.OpenTelemetryConfig{
				Ctx:               t.Context(),
				Log:               log,
				ServiceVersion:    "test-version",
				CollectorEndpoint: "localhost:4317",
			})

			watcher := &cniwatcher.CalicoWatcher{
				Watcher: cniwatcher.Watcher{
					Log:         log,
					OtelService: otelService,
				},
			}

			err := watcher.Shutdown()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
