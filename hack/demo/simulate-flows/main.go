// simulate-flows sends fake OTLP flow metrics to the network-enforcer controller,
// simulating traffic between the demo workloads (frontend -> backend -> postgres).
// Usage: go run ./hack/demo/simulate-flows [--target localhost:4317] [--interval 10s]
package main

import (
	"context"
	"flag"
	"log"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type flow struct {
	srcNs, srcKind, srcName string
	dstNs, dstKind, dstName string
	srcAddr, dstAddr        string
	dstPort                 string
	protocol                string
	bytes                   int64
}

func demoFlows() []flow {
	const (
		demoNamespace  = "demo"
		deploymentKind = "Deployment"
		backendName    = "backend"
		tcpProtocol    = "tcp"
		backendAddr    = "10.42.0.20"

		frontendToBackendBytes  = int64(2048)
		backendToPostgresBytes  = int64(4096)
		externalToFrontendBytes = int64(512)
		backendToExternalBytes  = int64(1024)
	)

	return []flow{
		{
			srcNs: demoNamespace, srcKind: deploymentKind, srcName: "frontend",
			dstNs: demoNamespace, dstKind: deploymentKind, dstName: backendName,
			srcAddr: "10.42.0.10", dstAddr: backendAddr,
			dstPort: "8080", protocol: tcpProtocol, bytes: frontendToBackendBytes,
		},
		{
			srcNs: demoNamespace, srcKind: deploymentKind, srcName: backendName,
			dstNs: demoNamespace, dstKind: deploymentKind, dstName: "postgres",
			srcAddr: backendAddr, dstAddr: "10.42.0.30",
			dstPort: "5432", protocol: tcpProtocol, bytes: backendToPostgresBytes,
		},
		{
			// External traffic hitting the frontend
			srcNs: "", srcKind: "", srcName: "",
			dstNs: demoNamespace, dstKind: deploymentKind, dstName: "frontend",
			srcAddr: "203.0.113.50", dstAddr: "10.42.0.10",
			dstPort: "8080", protocol: tcpProtocol, bytes: externalToFrontendBytes,
		},
		{
			// Backend calling an external API
			srcNs: demoNamespace, srcKind: deploymentKind, srcName: backendName,
			dstNs: "", dstKind: "", dstName: "",
			srcAddr: backendAddr, dstAddr: "198.51.100.1",
			dstPort: "443", protocol: tcpProtocol, bytes: backendToExternalBytes,
		},
	}
}

func main() {
	target := flag.String("target", "localhost:4317", "OTLP gRPC endpoint")
	interval := flag.Duration("interval", 10*time.Second, "interval between metric exports")
	flag.Parse()

	conn, err := grpc.NewClient(*target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to %s: %v", *target, err)
	}
	defer func() { _ = conn.Close() }()

	client := colmetricspb.NewMetricsServiceClient(conn)
	log.Printf("sending flow metrics to %s every %s", *target, *interval)

	for {
		flows := demoFlows()
		req := buildRequest(flows)
		_, exportErr := client.Export(context.Background(), req)
		if exportErr != nil {
			log.Printf("export failed: %v (will retry)", exportErr)
		} else {
			log.Printf("exported %d flows", len(flows))
		}
		time.Sleep(*interval)
	}
}

func buildRequest(flows []flow) *colmetricspb.ExportMetricsServiceRequest {
	dataPoints := make([]*metricspb.NumberDataPoint, 0, len(flows))
	for _, f := range flows {
		dp := &metricspb.NumberDataPoint{
			Value: &metricspb.NumberDataPoint_AsInt{AsInt: f.bytes},
			Attributes: []*commonpb.KeyValue{
				strAttr("k8s.src.namespace", f.srcNs),
				strAttr("k8s.src.owner.type", f.srcKind),
				strAttr("k8s.src.owner.name", f.srcName),
				strAttr("k8s.dst.namespace", f.dstNs),
				strAttr("k8s.dst.owner.type", f.dstKind),
				strAttr("k8s.dst.owner.name", f.dstName),
				strAttr("src.address", f.srcAddr),
				strAttr("dst.address", f.dstAddr),
				strAttr("dst.port", f.dstPort),
				strAttr("transport", f.protocol),
			},
		}
		dataPoints = append(dataPoints, dp)
	}

	return &colmetricspb.ExportMetricsServiceRequest{
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
										DataPoints: dataPoints,
									},
								},
							},
						},
					},
				},
			},
		},
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
