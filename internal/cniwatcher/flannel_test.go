package cniwatcher_test

import (
	"context"
	"log/slog"
	"os"
	"regexp"
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

func TestNewFlannelWatcher(t *testing.T) {
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
			client := fake.NewClientBuilder().Build()
			log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
			otelService := otel.NewOpenTelemetryService(otel.OpenTelemetryConfig{
				Ctx:               t.Context(),
				Log:               log,
				CollectorEndpoint: "localhost:4317",
			})

			watcher := cniwatcher.Watcher{
				Ctx:         t.Context(),
				Client:      client,
				Log:         log,
				OtelService: otelService,
			}

			flannelWatcher, err := cniwatcher.NewFlannelWatcher(watcher)
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, flannelWatcher)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, flannelWatcher)
			}
		})
	}
}

func TestFlannelWatcher_Start(t *testing.T) {
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
			client := fake.NewClientBuilder().Build()
			log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
			otelService := otel.NewOpenTelemetryService(otel.OpenTelemetryConfig{
				Ctx:               t.Context(),
				Log:               log,
				CollectorEndpoint: "localhost:4317",
			})

			watcher := &cniwatcher.FlannelWatcher{
				Watcher: cniwatcher.Watcher{
					Client:      client,
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

func TestFlannelWatcher_ResolvePodOrServiceByIP(t *testing.T) {
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
						Labels: map[string]string{
							"app": "test",
						},
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
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
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
				err := client.Create(t.Context(), pod)
				require.NoError(t, err)
			}

			watcher := &cniwatcher.FlannelWatcher{
				Watcher: cniwatcher.Watcher{
					Client: client,
					Ctx:    t.Context(),
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
				assert.Contains(t, info.Labels, "app=test")
			}
		})
	}
}

func TestFlannelWatcher_Shutdown(t *testing.T) {
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

			watcher := &cniwatcher.FlannelWatcher{
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

func TestDropByPolicyRegexFieldExtraction(t *testing.T) {
	dropByPolicyRegex := regexp.MustCompile(
		`(?P<timestamp>^\w+\s+\d+\s+\d+:\d+:\d+)` +
			`\s+[^ ]+\s+DROP by policy (?P<policy>[\w-]+\/[\w-]+)` +
			` IN=[^ ]* OUT=[^ ]* MAC=[^ ]* SRC=(?P<srcip>[^ ]+) DST=(?P<dstip>[^ ]+)` +
			`.*?PROTO=(?P<proto>[^ ]+)` +
			`(?: SPT=(?P<srcport>\d+) DPT=(?P<dstport>\d+))?`,
	)

	tests := []struct {
		name           string
		logLine        string
		shouldMatch    bool
		expectedFields map[string]string
	}{
		{
			name:        "TCP with ports",
			logLine:     "Jan 15 14:30:25 node1 DROP by policy default/deny-all IN=eth0 OUT=eth1 MAC=00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd SRC=10.244.1.5 DST=10.244.2.10 LEN=60 TOS=0x00 PREC=0x00 TTL=64 ID=12345 DF PROTO=TCP SPT=45678 DPT=80 WINDOW=29200 RES=0x00 SYN URGP=0",
			shouldMatch: true,
			expectedFields: map[string]string{
				"timestamp": "Jan 15 14:30:25",
				"policy":    "default/deny-all",
				"srcip":     "10.244.1.5",
				"dstip":     "10.244.2.10",
				"proto":     "TCP",
				"srcport":   "45678",
				"dstport":   "80",
			},
		},
		{
			name:        "ICMP without ports",
			logLine:     "Mar 10 16:45:30 master DROP by policy my-namespace/test_policy IN=flannel.1 OUT= MAC=12:34:56:78:9a:bc:de:f0:12:34:56:78:9a:bc SRC=172.16.0.50 DST=172.16.0.100 LEN=84 TOS=0x00 PREC=0x00 TTL=64 ID=0 DF PROTO=ICMP TYPE=8 CODE=0 ID=1234 SEQ=1",
			shouldMatch: true,
			expectedFields: map[string]string{
				"timestamp": "Mar 10 16:45:30",
				"policy":    "my-namespace/test_policy",
				"srcip":     "172.16.0.50",
				"dstip":     "172.16.0.100",
				"proto":     "ICMP",
				"srcport":   "",
				"dstport":   "",
			},
		},
		{
			name:        "Non-matching log line - different format",
			logLine:     "Jan 15 14:30:25 node1 ACCEPT by policy default/allow-all IN=eth0 OUT=eth1",
			shouldMatch: false,
		},
		{
			name:        "Non-matching log line - missing required fields",
			logLine:     "Jan 15 14:30:25 node1 DROP by policy default/deny-all",
			shouldMatch: false,
		},
		{
			name:        "Non-matching log line - completely different format",
			logLine:     "Jan 15 14:30:25 node1 kernel: some other kernel message",
			shouldMatch: false,
		},
		{
			name:        "Empty line",
			logLine:     "",
			shouldMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := dropByPolicyRegex.FindStringSubmatch(tt.logLine)

			if !tt.shouldMatch {
				assert.Nil(t, matches, "Expected no match for log line: %s", tt.logLine)
				return
			}

			require.NotNil(t, matches, "Expected match for log line: %s", tt.logLine)

			groupNames := dropByPolicyRegex.SubexpNames()
			fields := map[string]string{}
			for i, name := range groupNames {
				if i != 0 && name != "" {
					fields[name] = matches[i]
				}
			}

			for expectedField, expectedValue := range tt.expectedFields {
				actualValue, exists := fields[expectedField]
				assert.True(t, exists, "Expected field '%s' to be extracted", expectedField)
				assert.Equal(t, expectedValue, actualValue, "Field '%s' value mismatch", expectedField)
			}

			requiredFields := []string{"timestamp", "policy", "srcip", "dstip", "proto"}
			for _, field := range requiredFields {
				_, exists := fields[field]
				assert.True(t, exists, "Required field '%s' should be present", field)
			}

			_, srcPortExists := fields["srcport"]
			_, dstPortExists := fields["dstport"]
			assert.True(t, srcPortExists, "srcport field should exist (may be empty)")
			assert.True(t, dstPortExists, "dstport field should exist (may be empty)")
		})
	}
}
