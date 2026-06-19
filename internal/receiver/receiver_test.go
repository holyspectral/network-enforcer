package receiver

import (
	"log/slog"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"secuity.rancher.io/network-enforcer/internal/ownerkind"
	"secuity.rancher.io/network-enforcer/internal/topology"
)

type testLogWriter struct {
	t *testing.T
}

func (w *testLogWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", string(p))
	return len(p), nil
}

// NewTestLogger returns an [slog.Logger] that writes to t.Logf.
func NewTestLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewJSONHandler(&testLogWriter{t: t}, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})).With("component", t.Name())
}

func TestReceiverGenerateFlow(t *testing.T) {
	t.Parallel()
	attrs := map[string]string{
		"iface.direction":    "egress",
		"k8s.dst.owner.type": string(ownerkind.KindDeployment),
		"k8s.src.owner.type": string(ownerkind.KindDeployment),
		"k8s.dst.owner.name": "server",
		"k8s.src.owner.name": "client",
		"k8s.src.namespace":  "default",
		"k8s.dst.namespace":  "default",
		"transport":          "TCP",
		"dst.port":           "80",
	}

	mutateAttrs := func(attrs map[string]string, key, value string) map[string]string {
		newAttrs := maps.Clone(attrs)
		newAttrs[key] = value
		return newAttrs
	}

	tests := []struct {
		name  string
		attrs map[string]string
		flow  *topology.FlowRecord
	}{
		{
			name:  "DropsIngressFlow",
			attrs: mutateAttrs(attrs, "iface.direction", "ingress"),
			flow:  nil,
		},
		{
			name:  "DropsServiceDestination",
			attrs: mutateAttrs(attrs, "k8s.dst.owner.type", string(ownerkind.KindService)),
			flow:  nil,
		},
		{
			name:  "MissingSrcInfo",
			attrs: mutateAttrs(attrs, "k8s.src.owner.name", ""),
			flow:  nil,
		},
		{
			name:  "UnsupportedDstKind",
			attrs: mutateAttrs(attrs, "k8s.dst.owner.type", string(ownerkind.KindCronJob)),
			flow:  nil,
		},
		{
			name:  "WrongProtocol",
			attrs: mutateAttrs(attrs, "transport", "SCTP"),
			flow:  nil,
		},
		{
			name:  "WrongPort",
			attrs: mutateAttrs(attrs, "dst.port", "65536"),
			flow:  nil,
		},
		{
			name:  "CorrectFlow",
			attrs: attrs,
			flow: &topology.FlowRecord{
				Source: topology.WorkloadKey{
					Namespace: "default",
					OwnerName: "client",
					OwnerKind: ownerkind.KindDeployment,
				},
				Dest: topology.WorkloadKey{
					Namespace: "default",
					OwnerName: "server",
					OwnerKind: ownerkind.KindDeployment,
				},
				DstAddress: "",
				SrcAddress: "",
				Protocol:   corev1.ProtocolTCP,
				DstPort:    80,
			},
		},
	}

	store := topology.NewStore()
	r := NewReceiver(store, 0, NewTestLogger(t))

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			flow := r.generateFlow(tc.attrs)
			require.Equal(t, tc.flow, flow)
		})
	}
}
