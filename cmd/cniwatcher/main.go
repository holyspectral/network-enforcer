package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rancher-sandbox/network-enforcer/internal/cniwatcher"
	"github.com/rancher-sandbox/network-enforcer/internal/otel"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const otelShutdownTimeout = 10 * time.Second

//nolint:gochecknoglobals // this is set by the build process
var CniWatcherVersion string

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, cancel := context.WithCancel(context.Background())

	otelCfg := otel.OpenTelemetryConfig{
		Ctx:               ctx,
		Log:               logger,
		ServiceVersion:    CniWatcherVersion,
		CollectorEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	}
	otelService := otel.NewOpenTelemetryService(otelCfg)
	if err := otelService.Start(); err != nil {
		logger.Warn("Failed to start OpenTelemetry", "err", err)
	}

	ctrlClient, err := client.New(config.GetConfigOrDie(), client.Options{})
	if err != nil {
		logger.Error("unable to create k8s client", "err", err)
		os.Exit(1)
	}

	cniwatcherCfg, err := cniwatcher.NewConfig(os.Getenv("NODE_NAME"), os.Getenv("CNIWATCHER_CNI_TYPE"))
	if err != nil {
		logger.Error("failed to create cniWatcher config", "err", err)
		os.Exit(1)
	}

	watcher := cniwatcher.Watcher{
		Ctx:         ctx,
		Client:      ctrlClient,
		Log:         logger,
		OtelService: otelService,
	}

	cniWatcher, err := cniwatcher.NewCNIWatcher(cniwatcherCfg, watcher)
	if err != nil {
		logger.Error("Failed to create cniWatcher", "err", err)
		os.Exit(1)
	}

	shutdownCh := make(chan struct{})

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		logger.Info("Received shutdown signal")
		cancel()
		close(shutdownCh)
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- cniWatcher.Start()
	}()

	select {
	case startErr := <-errCh:
		if startErr != nil {
			logger.Error("Failed to start cniWatcher", "err", startErr)
			os.Exit(1)
		}
	case <-shutdownCh:
		performCleanupAndShutdown(logger, otelService, cniWatcher)
	}

	cancel()
	logger.Info("cniWatcher exited")
}

func performCleanupAndShutdown(logger *slog.Logger, otelService *otel.Service, cniWatcher cniwatcher.CNIWatcher) {
	ctxOtelShutdown, otelCancel := context.WithTimeout(context.Background(), otelShutdownTimeout)
	defer otelCancel()

	logger.Info("Shutting down OpenTelemetry")
	if shutdownErr := otelService.Shutdown(ctxOtelShutdown); shutdownErr != nil {
		logger.Error("Failed to shutdown OpenTelemetry", "err", shutdownErr)
	}

	logger.Info("Shutting down cniWatcher")
	if shutdownErr := cniWatcher.Shutdown(); shutdownErr != nil {
		logger.Error("Failed to shutdown cniWatcher", "err", shutdownErr)
	}
}
