package main

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/fileconsumer"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/fileconsumer/emit"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/fileconsumer/matcher"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension/xextension/storage"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
)

// filePersister keeps checkpoints in a JSON file.
type filePersister struct {
	mu   sync.Mutex
	path string
	data map[string][]byte
}

func newFilePersister(path string) (*filePersister, error) {
	p := &filePersister{path: path, data: map[string][]byte{}}
	raw, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(raw, &p.data); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (p *filePersister) flush() error {
	raw, err := json.Marshal(p.data)
	if err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

func (p *filePersister) Get(_ context.Context, k string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.data[k], nil
}

func (p *filePersister) Set(_ context.Context, k string, v []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data[k] = v
	return p.flush()
}

func (p *filePersister) Delete(_ context.Context, k string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.data, k)
	return p.flush()
}

func (p *filePersister) Batch(_ context.Context, ops ...*storage.Operation) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, op := range ops {
		switch op.Type {
		case storage.Set:
			p.data[op.Key] = op.Value
		case storage.Delete:
			delete(p.data, op.Key)
		case storage.Get:
			op.Value = p.data[op.Key]
		}
	}
	return p.flush()
}

type tailer struct {
	mgr *fileconsumer.Manager
}

// startTailer tails files matching glob. Each line goes to onLine.
func startTailer(
	glob, statePath string,
	poll time.Duration,
	onTruncate string,
	onLine func([]byte, time.Time),
) (*tailer, error) {
	cfg := fileconsumer.NewConfig()
	cfg.Include = []string{glob}
	cfg.PollInterval = poll
	cfg.StartAt = "end"
	cfg.OnTruncate = onTruncate
	cfg.FlushPeriod = 0
	cfg.IncludeFileName = false
	cfg.Criteria = matcher.Criteria{Include: []string{glob}}

	cb := emit.Callback(func(_ context.Context, tokens [][]byte, _ map[string]any, _ int64, _ []int64) error {
		// fileconsumer does not expose the read time. The callback passes the zero
		// value, so the benchmark measures only the end-to-end latency.
		for _, t := range tokens {
			onLine(append([]byte(nil), t...), time.Time{})
		}
		return nil
	})
	mgr, err := cfg.Build(
		component.TelemetrySettings{
			Logger:         zap.NewNop(),
			TracerProvider: tracenoop.NewTracerProvider(),
			MeterProvider:  metricnoop.NewMeterProvider(),
		},
		cb,
	)
	if err != nil {
		return nil, err
	}
	var per *filePersister
	if statePath != "" {
		per, err = newFilePersister(statePath)
		if err != nil {
			return nil, err
		}
	}
	if per != nil {
		err = mgr.Start(per)
	} else {
		err = mgr.Start(nil)
	}
	if err != nil {
		return nil, err
	}
	return &tailer{mgr: mgr}, nil
}

func (t *tailer) Stop() error { return t.mgr.Stop() }
