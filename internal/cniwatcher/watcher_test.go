package cniwatcher_test

import (
	"log/slog"
	"os"
	"testing"

	"github.com/rancher-sandbox/network-enforcer/internal/cniwatcher"
	"github.com/rancher-sandbox/network-enforcer/internal/otel"
	"github.com/rancher-sandbox/network-enforcer/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNewCNIWatcher(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := t.Context()
	fakeClient := fake.NewClientBuilder().Build()
	otelService := otel.NewOpenTelemetryService(otel.OpenTelemetryConfig{
		Ctx:               ctx,
		Log:               logger,
		CollectorEndpoint: "localhost:4317",
	})

	watcher := cniwatcher.Watcher{
		Ctx:         ctx,
		Client:      fakeClient,
		Log:         logger,
		OtelService: otelService,
	}

	tests := []struct {
		name    string
		config  cniwatcher.Config
		wantErr bool
	}{
		{
			name: "Valid Calico config",
			config: cniwatcher.Config{
				NodeName:     "test-node",
				CNIType:      types.CNITypeCalico,
				ConnEndpoint: "goldmane:7443",
			},
			wantErr: false,
		},
		{
			name: "Valid Cilium config",
			config: cniwatcher.Config{
				NodeName:     "test-node",
				CNIType:      types.CNITypeCilium,
				ConnEndpoint: "unix:///var/run/cilium/hubble.sock",
			},
			wantErr: false,
		},
		{
			name: "Valid AWS VPC config",
			config: cniwatcher.Config{
				NodeName: "test-node",
				CNIType:  types.CNITypeAWSVPC,
			},
			wantErr: false,
		},
		{
			name: "Valid Flannel config",
			config: cniwatcher.Config{
				NodeName: "test-node",
				CNIType:  types.CNITypeFlannel,
			},
			wantErr: false,
		},
		{
			name: "Unknown CNI type",
			config: cniwatcher.Config{
				NodeName: "test-node",
				CNIType:  types.CNITypeUnknown,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cniWatcher, err := cniwatcher.NewCNIWatcher(tt.config, watcher)
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, cniWatcher)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, cniWatcher)
			}
		})
	}
}

func TestWatcher_ProcessPolicyDenyEvent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := t.Context()
	fakeClient := fake.NewClientBuilder().Build()

	watcher := &cniwatcher.Watcher{
		Ctx:    ctx,
		Client: fakeClient,
		Log:    logger,
	}

	// Test with nil event
	err := watcher.ProcessPolicyDenyEvent(nil)
	require.NoError(t, err)

	// Test with valid event
	event := &types.PolicyDenyEvent{
		Timestamp:    1234567890,
		NodeName:     "test-node",
		CNIType:      "calico",
		Protocol:     "TCP",
		SrcNamespace: "default",
		SrcName:      "test-pod",
		DstNamespace: "default",
		DstName:      "test-service",
	}

	err = watcher.ProcessPolicyDenyEvent(event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OpenTelemetry service is not initialized")

	otelService := otel.NewOpenTelemetryService(otel.OpenTelemetryConfig{
		Ctx:               ctx,
		Log:               logger,
		CollectorEndpoint: "localhost:4317",
	})

	err = otelService.Start()
	if err != nil {
		t.Logf("OTEL service start failed (expected in test): %v", err)
	}

	watcher.OtelService = otelService

	err = watcher.ProcessPolicyDenyEvent(event)
	assert.NoError(t, err)
}

func TestWatcher_GetNetworkPolicyAPIVersion(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	watcher := &cniwatcher.Watcher{Log: logger}

	tests := []struct {
		name    string
		kind    string
		want    string
		wantErr bool
	}{
		{
			name:    "NetworkPolicy",
			kind:    "NetworkPolicy",
			want:    "networking.k8s.io/v1",
			wantErr: false,
		},
		{
			name:    "CalicoNetworkPolicy",
			kind:    "CalicoNetworkPolicy",
			want:    "projectcalico.org/v3",
			wantErr: false,
		},
		{
			name:    "GlobalNetworkPolicy",
			kind:    "GlobalNetworkPolicy",
			want:    "projectcalico.org/v3",
			wantErr: false,
		},
		{
			name:    "CiliumNetworkPolicy",
			kind:    "CiliumNetworkPolicy",
			want:    "cilium.io/v2",
			wantErr: false,
		},
		{
			name:    "CiliumClusterwideNetworkPolicy",
			kind:    "CiliumClusterwideNetworkPolicy",
			want:    "cilium.io/v2",
			wantErr: false,
		},
		{
			name:    "Unknown policy kind",
			kind:    "UnknownPolicy",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := watcher.GetNetworkPolicyAPIVersion(tt.kind)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestPoliciesToStrings(t *testing.T) {
	tests := []struct {
		name     string
		policies []types.Policy
		want     []string
	}{
		{
			name:     "Empty policies",
			policies: []types.Policy{},
			want:     []string{},
		},
		{
			name: "Single policy",
			policies: []types.Policy{
				{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "v1",
						Kind:       "NetworkPolicy",
					},
					Name:      "test-policy",
					Namespace: "default",
				},
			},
			want: []string{"v1/NetworkPolicy/default/test-policy"},
		},
		{
			name: "Multiple policies",
			policies: []types.Policy{
				{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "v1",
						Kind:       "NetworkPolicy",
					},
					Name:      "policy1",
					Namespace: "default",
				},
				{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "v1",
						Kind:       "NetworkPolicy",
					},
					Name:      "policy2",
					Namespace: "kube-system",
				},
			},
			want: []string{
				"v1/NetworkPolicy/default/policy1",
				"v1/NetworkPolicy/kube-system/policy2",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := make([]string, len(tt.policies))
			for i, p := range tt.policies {
				got[i] = p.String()
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWatcher_ResolvePodOrServiceByIP(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		want    cniwatcher.PodOrServiceInfo
		wantErr bool
	}{
		{
			name:    "Empty IP",
			ip:      "",
			want:    cniwatcher.PodOrServiceInfo{},
			wantErr: true,
		},
		{
			name:    "Invalid IP format",
			ip:      "invalid-ip",
			want:    cniwatcher.PodOrServiceInfo{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			watcher := &cniwatcher.Watcher{
				Ctx: t.Context(),
			}
			got, err := watcher.ResolvePodOrServiceByIP(tt.ip)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
