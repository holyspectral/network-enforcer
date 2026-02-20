package flowcollector

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"

	"secuity.rancher.io/network-enforcer/internal/topology"
)

func TestReceiver_Export(t *testing.T) {
	store := topology.NewStore()
	r := NewReceiver(store, 0, logr.Discard())

	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{},
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "obi.network.flow.bytes",
								Data: &metricspb.Metric_Sum{
									Sum: &metricspb.Sum{
										DataPoints: []*metricspb.NumberDataPoint{
											{
												Value: &metricspb.NumberDataPoint_AsInt{AsInt: 1024},
												Attributes: []*commonpb.KeyValue{
													strAttr("k8s.src.namespace", "default"),
													strAttr("k8s.src.owner.kind", "Deployment"),
													strAttr("k8s.src.owner.name", "frontend"),
													strAttr("k8s.dst.namespace", "default"),
													strAttr("k8s.dst.owner.kind", "Deployment"),
													strAttr("k8s.dst.owner.name", "backend"),
													strAttr("dst.port", "8080"),
													strAttr("transport", "tcp"),
													strAttr("src.address", "10.0.0.1"),
													strAttr("dst.address", "10.0.0.2"),
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	resp, err := r.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	wk := topology.WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "frontend"}
	flows := store.FlowsForWorkload(wk)
	if len(flows) != 1 {
		t.Fatalf("expected 1 flow, got %d", len(flows))
	}
	if flows[0].DstPort != 8080 {
		t.Fatalf("expected dst port 8080, got %d", flows[0].DstPort)
	}
	if flows[0].Protocol != "TCP" {
		t.Fatalf("expected TCP, got %s", flows[0].Protocol)
	}
	if flows[0].ByteCount != 1024 {
		t.Fatalf("expected 1024 bytes, got %d", flows[0].ByteCount)
	}
}

func TestReceiver_Export_IgnoresOtherMetrics(t *testing.T) {
	store := topology.NewStore()
	r := NewReceiver(store, 0, logr.Discard())

	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "some.other.metric",
								Data: &metricspb.Metric_Sum{
									Sum: &metricspb.Sum{
										DataPoints: []*metricspb.NumberDataPoint{
											{Value: &metricspb.NumberDataPoint_AsInt{AsInt: 100}},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := r.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	workloads := store.Workloads()
	if len(workloads) != 0 {
		t.Fatalf("expected 0 workloads from ignored metric, got %d", len(workloads))
	}
}

func TestReceiver_Export_GaugeMetric(t *testing.T) {
	store := topology.NewStore()
	r := NewReceiver(store, 0, logr.Discard())

	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "obi.network.flow.bytes",
								Data: &metricspb.Metric_Gauge{
									Gauge: &metricspb.Gauge{
										DataPoints: []*metricspb.NumberDataPoint{
											{
												Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 512.5},
												Attributes: []*commonpb.KeyValue{
													strAttr("k8s.src.namespace", "ns1"),
													strAttr("k8s.src.owner.kind", "DaemonSet"),
													strAttr("k8s.src.owner.name", "monitor"),
													strAttr("k8s.dst.namespace", "ns2"),
													strAttr("k8s.dst.owner.kind", "Deployment"),
													strAttr("k8s.dst.owner.name", "api"),
													strAttr("dst.port", "443"),
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := r.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	wk := topology.WorkloadKey{Namespace: "ns1", OwnerKind: "DaemonSet", OwnerName: "monitor"}
	flows := store.FlowsForWorkload(wk)
	if len(flows) != 1 {
		t.Fatalf("expected 1 flow, got %d", len(flows))
	}
	if flows[0].ByteCount != 512 {
		t.Fatalf("expected 512 bytes (truncated from double), got %d", flows[0].ByteCount)
	}
}

func strAttr(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key: key,
		Value: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_StringValue{StringValue: value},
		},
	}
}
