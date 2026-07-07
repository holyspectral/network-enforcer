package otel_test

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/rancher-sandbox/network-enforcer/internal/otel"
	"github.com/rancher-sandbox/network-enforcer/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestNewOpenTelemetryService(t *testing.T) {
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := otel.OpenTelemetryConfig{
		Ctx:               ctx,
		Log:               logger,
		ServiceVersion:    "test-version",
		CollectorEndpoint: "localhost:4317",
	}
	service := otel.NewOpenTelemetryService(cfg)

	assert.NotNil(t, service)
	assert.Equal(t, cfg, service.Config)
}

func TestOtelService_Start(t *testing.T) {
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := otel.OpenTelemetryConfig{
		Ctx:               ctx,
		Log:               logger,
		ServiceVersion:    "test-version",
		CollectorEndpoint: "localhost:4317",
	}
	service := otel.NewOpenTelemetryService(cfg)

	err := service.Start()
	if err != nil {
		t.Logf("Expected error in test environment: %v", err)
	} else {
		t.Logf("OpenTelemetry started successfully in test environment")
	}
}

func TestOtelService_EmitPolicyDenyEvent(t *testing.T) {
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := otel.OpenTelemetryConfig{
		Ctx:               ctx,
		Log:               logger,
		ServiceVersion:    "test-version",
		CollectorEndpoint: "localhost:4317",
	}
	service := otel.NewOpenTelemetryService(cfg)

	event := &types.PolicyDenyEvent{
		Timestamp:    time.Now().Unix(),
		NodeName:     "test-node",
		CNIType:      "test-cni",
		Protocol:     "TCP",
		SrcNamespace: "default",
		SrcName:      "test-pod",
		DstNamespace: "default",
		DstName:      "test-service",
	}

	err := service.EmitPolicyDenyEvent(event)
	assert.Error(t, err)
}

func TestOtelService_Shutdown(t *testing.T) {
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := otel.OpenTelemetryConfig{
		Ctx:               ctx,
		Log:               logger,
		ServiceVersion:    "test-version",
		CollectorEndpoint: "localhost:4317",
	}
	service := otel.NewOpenTelemetryService(cfg)

	err := service.Shutdown(ctx)
	assert.NoError(t, err)
}
