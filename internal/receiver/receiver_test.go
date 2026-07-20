package receiver

import (
	"log/slog"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/rancher-sandbox/network-enforcer/internal/ownerkind"
	"github.com/rancher-sandbox/network-enforcer/internal/topology"
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

	udpAttrs := map[string]string{
		"transport":          "UDP",
		"src.port":           "43740",
		"dst.port":           "53",
		"k8s.src.owner.type": string(ownerkind.KindDeployment),
		"k8s.src.owner.name": "ubuntu-deployment",
		"k8s.src.namespace":  "default",
		"k8s.dst.owner.type": string(ownerkind.KindDeployment),
		"k8s.dst.owner.name": "coredns",
		"k8s.dst.namespace":  "kube-system",
		"src.address":        "192.168.92.22",
		"dst.address":        "192.168.21.204",
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
		{
			// src.port=43740 (> 1024), dst.port=53 (< 1024): request direction, must be recorded.
			name:  "UDPRequestIsRecorded",
			attrs: udpAttrs,
			flow: &topology.FlowRecord{
				Source: topology.WorkloadKey{
					Namespace: "default",
					OwnerKind: ownerkind.KindDeployment,
					OwnerName: "ubuntu-deployment",
				},
				Dest: topology.WorkloadKey{
					Namespace: "kube-system",
					OwnerKind: ownerkind.KindDeployment,
					OwnerName: "coredns",
				},
				DstPort:    53,
				Protocol:   corev1.ProtocolUDP,
				SrcAddress: "192.168.92.22",
				DstAddress: "192.168.21.204",
			},
		},
		{
			// src.port=53 (< 1024), dst.port=47452 (> 1024): response direction, must be dropped.
			name:  "UDPResponseIsDropped",
			attrs: mutateAttrs(mutateAttrs(udpAttrs, "src.port", "53"), "dst.port", "47452"),
			flow:  nil,
		},
	}

	store := topology.NewStore()
	r := NewReceiver(store, 0, "", NewTestLogger(t))

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			flow := r.generateFlow(tc.attrs)
			require.Equal(t, tc.flow, flow)
		})
	}
}
