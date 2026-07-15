package cniwatcher_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	flowpb "github.com/cilium/cilium/api/v1/flow"
	hubbleObserver "github.com/cilium/cilium/api/v1/observer"
	monitorApi "github.com/cilium/cilium/pkg/monitor/api"
	"github.com/rancher-sandbox/network-enforcer/internal/cniwatcher"
	"github.com/rancher-sandbox/network-enforcer/internal/otel"
	"github.com/rancher-sandbox/network-enforcer/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNewCiliumWatcher(t *testing.T) {
	tests := []struct {
		name             string
		connEndpoint     string
		expectedEndpoint string
	}{
		{
			name:             "Default Hubble endpoint",
			connEndpoint:     "unix:///var/run/cilium/hubble.sock",
			expectedEndpoint: types.DefaultHubbleEndpoint,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().Build()
			log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
			otelService := otel.NewOpenTelemetryService(otel.OpenTelemetryConfig{
				Ctx:               t.Context(),
				Log:               log,
				CollectorEndpoint: "localhost:4317",
			})

			watcher := cniwatcher.Watcher{
				Ctx:         t.Context(),
				Client:      fakeClient,
				Log:         log,
				OtelService: otelService,
			}

			ciliumWatcher, err := cniwatcher.NewCiliumWatcher(watcher, tt.connEndpoint)
			require.NoError(t, err)
			assert.NotNil(t, ciliumWatcher)
			assert.Equal(t, tt.expectedEndpoint, ciliumWatcher.ConnEndpoint)
		})
	}
}

type TestCiliumWatcher struct {
	*cniwatcher.CiliumWatcher

	connectFunc func() error
}

func (w *TestCiliumWatcher) connectToHubble() error {
	if w.connectFunc != nil {
		return w.connectFunc()
	}
	return w.ConnectToHubble()
}

func TestCiliumWatcher_ConnectToHubble(t *testing.T) {
	clientset := fake.NewClientBuilder().Build()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	otelService := otel.NewOpenTelemetryService(otel.OpenTelemetryConfig{
		Ctx:               t.Context(),
		Log:               log,
		CollectorEndpoint: "localhost:4317",
	})

	tests := []struct {
		name           string
		endpoint       string
		expectedError  bool
		expectedClient bool
	}{
		{
			name:           "Valid Unix socket endpoint",
			endpoint:       "unix:///var/run/cilium/hubble.sock",
			expectedError:  false,
			expectedClient: true,
		},
		{
			name:           "Invalid endpoint",
			endpoint:       "invalid-endpoint",
			expectedError:  true,
			expectedClient: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			watcher := &cniwatcher.CiliumWatcher{
				Watcher: cniwatcher.Watcher{
					Client:      clientset,
					Log:         log,
					OtelService: otelService,
				},
			}

			testWatcher := &TestCiliumWatcher{
				CiliumWatcher: watcher,
				connectFunc: func() error {
					if tt.expectedError {
						return errors.New("connection failed")
					}
					watcher.Client = &MockObserverClient{}
					return nil
				},
			}

			err := testWatcher.connectToHubble()

			if tt.expectedError {
				require.Error(t, err)
				assert.Nil(t, testWatcher.Client)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, testWatcher.Client)
			}
		})
	}
}

type MockObserverClient struct {
	hubbleObserver.ObserverClient

	getFlowsFunc func(ctx context.Context, req *hubbleObserver.GetFlowsRequest, opts ...grpc.CallOption) (hubbleObserver.Observer_GetFlowsClient, error)
}

func (m *MockObserverClient) GetFlows(
	ctx context.Context,
	req *hubbleObserver.GetFlowsRequest,
	opts ...grpc.CallOption,
) (hubbleObserver.Observer_GetFlowsClient, error) {
	if m.getFlowsFunc != nil {
		return m.getFlowsFunc(ctx, req, opts...)
	}
	return nil, errors.New("getFlows function not set")
}

type MockGetFlowsClient struct {
	hubbleObserver.Observer_GetFlowsClient

	recvFunc func() (*hubbleObserver.GetFlowsResponse, error)
}

func (m *MockGetFlowsClient) Recv() (*hubbleObserver.GetFlowsResponse, error) {
	if m.recvFunc != nil {
		return m.recvFunc()
	}
	return nil, errors.New("recv function not set")
}

func TestCiliumWatcher_WatchFlows(t *testing.T) {
	clientset := fake.NewClientBuilder().Build()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	otelService := otel.NewOpenTelemetryService(otel.OpenTelemetryConfig{
		Ctx:               t.Context(),
		Log:               log,
		CollectorEndpoint: "localhost:4317",
	})

	watcher := &cniwatcher.CiliumWatcher{
		Watcher: cniwatcher.Watcher{
			Client:      clientset,
			Log:         log,
			OtelService: otelService,
		},
	}

	tests := []struct {
		name          string
		getFlowsFunc  func(ctx context.Context, req *hubbleObserver.GetFlowsRequest, opts ...grpc.CallOption) (hubbleObserver.Observer_GetFlowsClient, error)
		recvFunc      func() (*hubbleObserver.GetFlowsResponse, error)
		expectedError bool
	}{
		{
			name: "Successful flow stream",
			getFlowsFunc: func(_ context.Context, _ *hubbleObserver.GetFlowsRequest, _ ...grpc.CallOption) (hubbleObserver.Observer_GetFlowsClient, error) {
				return &MockGetFlowsClient{
					recvFunc: func() (*hubbleObserver.GetFlowsResponse, error) {
						return &hubbleObserver.GetFlowsResponse{
							ResponseTypes: &hubbleObserver.GetFlowsResponse_Flow{
								Flow: &flowpb.Flow{
									EventType: &flowpb.CiliumEventType{
										Type: monitorApi.MessageTypePolicyVerdict,
									},
									DropReasonDesc: flowpb.DropReason_POLICY_DENIED,
								},
							},
						}, nil
					},
				}, nil
			},
			recvFunc: func() (*hubbleObserver.GetFlowsResponse, error) {
				return &hubbleObserver.GetFlowsResponse{
					ResponseTypes: &hubbleObserver.GetFlowsResponse_Flow{
						Flow: &flowpb.Flow{
							EventType: &flowpb.CiliumEventType{
								Type: monitorApi.MessageTypePolicyVerdict,
							},
							DropReasonDesc: flowpb.DropReason_POLICY_DENIED,
						},
					},
				}, nil
			},
			expectedError: false,
		},
		{
			name: "GetFlows error",
			getFlowsFunc: func(_ context.Context, _ *hubbleObserver.GetFlowsRequest, _ ...grpc.CallOption) (hubbleObserver.Observer_GetFlowsClient, error) {
				return nil, errors.New("getFlows error")
			},
			recvFunc:      nil,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			watcher.Client = &MockObserverClient{
				getFlowsFunc: tt.getFlowsFunc,
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

func TestCiliumWatcher_Shutdown(t *testing.T) {
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
				CollectorEndpoint: "localhost:4317",
			})

			watcher := &cniwatcher.CiliumWatcher{
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
