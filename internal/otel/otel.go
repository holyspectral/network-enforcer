package otel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/rancher-sandbox/network-enforcer/internal/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.30.0"
	"go.opentelemetry.io/otel/trace"
)

const DefaultOtelCollectorEndpoint = "localhost:4317"

type OpenTelemetryConfig struct {
	Ctx               context.Context
	Log               *slog.Logger
	CollectorEndpoint string
}

type OpenTelemetryService struct {
	TracerProvider *sdktrace.TracerProvider
	Tracer         trace.Tracer
}

type Service struct {
	Config  OpenTelemetryConfig
	Service *OpenTelemetryService
}

func NewOpenTelemetryService(cfg OpenTelemetryConfig) *Service {
	return &Service{
		Config:  cfg,
		Service: &OpenTelemetryService{},
	}
}

func (s *Service) Start() error {
	if s.Config.CollectorEndpoint == "" {
		s.Config.CollectorEndpoint = DefaultOtelCollectorEndpoint
	}

	exporter, err := otlptracegrpc.New(s.Config.Ctx,
		otlptracegrpc.WithEndpoint(s.Config.CollectorEndpoint),
		otlptracegrpc.WithInsecure(), // TODO: replace with TLS in production later.
	)
	if err != nil {
		return fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	res, err := resource.New(s.Config.Ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String("cniwatcher"),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create resource: %w", err)
	}

	s.Service.TracerProvider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(s.Service.TracerProvider)
	s.Service.Tracer = s.Service.TracerProvider.Tracer("cniwatcher")
	s.Config.Log.Info("OpenTelemetry initialized", "collector", s.Config.CollectorEndpoint)
	return nil
}

func policiesToStrings(policies []types.Policy) []string {
	if policies == nil {
		return nil
	}
	result := make([]string, len(policies))
	for i, p := range policies {
		result[i] = p.String()
	}
	return result
}

func addStringAttr(attrs []attribute.KeyValue, key, value string) []attribute.KeyValue {
	if value != "" {
		return append(attrs, attribute.String(key, value))
	}
	return attrs
}

func addStringSliceAttr(attrs []attribute.KeyValue, key string, value []string) []attribute.KeyValue {
	if len(value) > 0 {
		return append(attrs, attribute.StringSlice(key, value))
	}
	return attrs
}

func addIntAttr(attrs []attribute.KeyValue, key string, value int64) []attribute.KeyValue {
	if value != 0 {
		return append(attrs, attribute.Int64(key, value))
	}
	return attrs
}

func (s *Service) EmitPolicyDenyEvent(event *types.PolicyDenyEvent) error {
	if s.Service.Tracer == nil {
		return errors.New("OpenTelemetry is not initialized , skip emitting policy deny event")
	}

	s.Config.Log.Info("Emitting policy deny event", "event", event)

	ctx := context.Background()
	_, span := s.Service.Tracer.Start(ctx, "policy.deny")
	defer span.End()

	// Build attributes conditionally - only add non-empty values
	attrs := []attribute.KeyValue{
		attribute.String("timestamp.formatted", time.Unix(event.Timestamp, 0).Format(time.RFC3339)),
	}
	attrs = addStringAttr(attrs, "cni.type", event.CNIType)
	attrs = addStringAttr(attrs, "network.protocol", string(event.Protocol))
	attrs = addStringAttr(attrs, "node.name", event.NodeName)
	attrs = addStringAttr(attrs, "source.namespace", event.SrcNamespace)
	attrs = addStringAttr(attrs, "source.name", event.SrcName)
	attrs = addStringSliceAttr(attrs, "source.labels", event.SrcLabels)
	attrs = addStringSliceAttr(attrs, "source.workloads", event.SrcWorkloads)
	attrs = addStringAttr(attrs, "destination.namespace", event.DstNamespace)
	attrs = addStringAttr(attrs, "destination.name", event.DstName)
	attrs = addStringSliceAttr(attrs, "destination.labels", event.DstLabels)
	attrs = addStringSliceAttr(attrs, "destination.workloads", event.DstWorkloads)
	attrs = addIntAttr(attrs, "destination.port", int64(event.DstPort))
	attrs = addStringSliceAttr(attrs, "egress.enforced_by", policiesToStrings(event.EgressEnforcedBy))
	attrs = addStringSliceAttr(attrs, "ingress.enforced_by", policiesToStrings(event.IngressEnforcedBy))

	span.SetAttributes(attrs...)
	return nil
}

func (s *Service) Shutdown(ctx context.Context) error {
	if s.Service.TracerProvider == nil {
		return nil
	}

	return s.Service.TracerProvider.Shutdown(ctx)
}
