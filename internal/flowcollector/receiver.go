package flowcollector

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc"

	"secuity.rancher.io/network-enforcer/internal/topology"
)

const targetMetricName = "obi.network.flow.bytes"

type Receiver struct {
	colmetricspb.UnimplementedMetricsServiceServer

	store  *topology.Store
	port   int
	log    logr.Logger
	server *grpc.Server
}

func NewReceiver(store *topology.Store, port int, log logr.Logger) *Receiver {
	return &Receiver{
		store: store,
		port:  port,
		log:   log.WithName("flowcollector"),
	}
}

func (r *Receiver) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", r.port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", r.port, err)
	}

	r.server = grpc.NewServer()
	colmetricspb.RegisterMetricsServiceServer(r.server, r)

	r.log.Info("listening", "port", r.port)

	go func() {
		<-ctx.Done()
		r.server.GracefulStop()
	}()

	if err := r.server.Serve(lis); err != nil {
		return fmt.Errorf("gRPC server error: %w", err)
	}
	return nil
}

func (r *Receiver) Export(_ context.Context, req *colmetricspb.ExportMetricsServiceRequest) (*colmetricspb.ExportMetricsServiceResponse, error) {
	now := time.Now()

	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				r.log.Info("received metric", "name", m.GetName())
				if m.GetName() != targetMetricName {
					continue
				}
				r.processMetric(m, now)
			}
		}
	}

	return &colmetricspb.ExportMetricsServiceResponse{}, nil
}

func (r *Receiver) processMetric(m *metricspb.Metric, now time.Time) {
	var dataPoints []*metricspb.NumberDataPoint

	switch d := m.GetData().(type) {
	case *metricspb.Metric_Sum:
		dataPoints = d.Sum.GetDataPoints()
	case *metricspb.Metric_Gauge:
		dataPoints = d.Gauge.GetDataPoints()
	default:
		return
	}

	for _, dp := range dataPoints {
		rec, ok := r.parseDataPoint(dp, now)
		if !ok {
			continue
		}
		r.store.Record(rec)
	}
}

func (r *Receiver) parseDataPoint(dp *metricspb.NumberDataPoint, now time.Time) (topology.FlowRecord, bool) {
	attrs := attrMap(dp.GetAttributes())
	r.log.Info("parsed datapoint", "attrs", attrs)

	srcNs := attrs["k8s.src.namespace"]
	srcKind := attrs["k8s.src.owner.type"]
	srcName := attrs["k8s.src.owner.name"]
	dstNs := attrs["k8s.dst.namespace"]
	dstKind := attrs["k8s.dst.owner.type"]
	dstName := attrs["k8s.dst.owner.name"]
	srcAddr := attrs["src.address"]
	dstAddr := attrs["dst.address"]
	dstPortStr := attrs["dst.port"]
	protocol := strings.ToUpper(attrs["transport"])
	if protocol == "" {
		protocol = "TCP"
	}

	dstPort, err := strconv.ParseInt(dstPortStr, 10, 32)
	if err != nil || dstPort <= 0 || dstPort > 65535 {
		r.log.V(1).Info("Dropped datapoint with missing or invalid dst.port", "value", dstPortStr)
		return topology.FlowRecord{}, false
	}

	var byteCount int64
	switch v := dp.GetValue().(type) {
	case *metricspb.NumberDataPoint_AsInt:
		byteCount = v.AsInt
	case *metricspb.NumberDataPoint_AsDouble:
		byteCount = int64(v.AsDouble)
	}

	return topology.FlowRecord{
		Source: topology.WorkloadKey{
			Namespace: srcNs,
			OwnerKind: srcKind,
			OwnerName: srcName,
		},
		Dest: topology.WorkloadKey{
			Namespace: dstNs,
			OwnerKind: dstKind,
			OwnerName: dstName,
		},
		DstPort:    int32(dstPort),
		Protocol:   protocol,
		SrcAddress: srcAddr,
		DstAddress: dstAddr,
		FirstSeen:  now,
		LastSeen:   now,
		ByteCount:  byteCount,
	}, true
}

func attrMap(attrs []*commonpb.KeyValue) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, kv := range attrs {
		v := kv.GetValue()
		if v == nil {
			continue
		}
		switch val := v.Value.(type) {
		case *commonpb.AnyValue_StringValue:
			if val.StringValue != "" {
				m[kv.GetKey()] = val.StringValue
			}
		case *commonpb.AnyValue_IntValue:
			m[kv.GetKey()] = strconv.FormatInt(val.IntValue, 10)
		case *commonpb.AnyValue_DoubleValue:
			m[kv.GetKey()] = strconv.FormatFloat(val.DoubleValue, 'f', -1, 64)
		case *commonpb.AnyValue_BoolValue:
			m[kv.GetKey()] = strconv.FormatBool(val.BoolValue)
		}
	}
	return m
}
