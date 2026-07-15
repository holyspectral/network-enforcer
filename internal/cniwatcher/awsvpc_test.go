package cniwatcher_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/rancher-sandbox/network-enforcer/internal/cniwatcher"
	"github.com/rancher-sandbox/network-enforcer/internal/otel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNewAWSVPCWatcher(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "Valid watcher",
			wantErr: false,
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

			awsWatcher, err := cniwatcher.NewAWSVPCWatcher(watcher)
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, awsWatcher)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, awsWatcher)
			}
		})
	}
}

func TestAWSVPCWatcher_Start(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "Successful start",
			wantErr: false,
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

			watcher := &cniwatcher.AWSVPCWatcher{
				Watcher: cniwatcher.Watcher{
					Ctx:         t.Context(),
					Client:      fakeClient,
					Log:         log,
					OtelService: otelService,
				},
			}

			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			watcher.Ctx = ctx

			err := watcher.Start()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				// Start will likely fail due to missing log file, but that's expected in tests
				// We're mainly testing that the method doesn't panic
				assert.NoError(t, err)
			}
		})
	}
}

func TestAWSVPCWatcher_ResolvePodOrServiceByIP(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		pods    []*corev1.Pod
		wantErr bool
	}{
		{
			name: "Valid IP address",
			ip:   "10.0.0.1",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
					Status: corev1.PodStatus{
						PodIP: "10.0.0.1",
					},
				},
			},
			wantErr: false,
		},
		{
			name:    "Invalid IP address",
			ip:      "invalid-ip",
			pods:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithIndex(&corev1.Pod{}, "status.podIP", func(obj client.Object) []string {
					pod := obj.(*corev1.Pod)
					if pod.Status.PodIP != "" {
						return []string{pod.Status.PodIP}
					}
					return nil
				}).
				WithIndex(&corev1.Service{}, "spec.clusterIP", func(obj client.Object) []string {
					svc := obj.(*corev1.Service)
					if svc.Spec.ClusterIP != "" && svc.Spec.ClusterIP != "None" {
						return []string{svc.Spec.ClusterIP}
					}
					return nil
				}).
				Build()

			for _, pod := range tt.pods {
				err := fakeClient.Create(t.Context(), pod)
				require.NoError(t, err)
			}

			watcher := &cniwatcher.AWSVPCWatcher{
				Watcher: cniwatcher.Watcher{
					Ctx:    t.Context(),
					Client: fakeClient,
				},
			}

			info, err := watcher.ResolvePodOrServiceByIP(tt.ip)
			if tt.wantErr {
				require.Error(t, err)
				assert.Empty(t, info.Name)
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, info.Name)

				assert.Equal(t, tt.pods[0].Name, info.Name)
				assert.Equal(t, tt.pods[0].Namespace, info.Namespace)
			}
		})
	}
}

func TestAWSVPCWatcher_Shutdown(t *testing.T) {
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

			watcher := &cniwatcher.AWSVPCWatcher{
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
