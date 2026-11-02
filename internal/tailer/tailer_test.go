package tailer

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// startTailer builds and starts a tailer. It stops the tailer at test end.
func startTailer(t *testing.T, cfg Config, c *collector) *Tailer {
	t.Helper()
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 50 * time.Millisecond
	}
	tl, err := New(cfg, c.handler)
	require.NoError(t, err)
	require.NoError(t, tl.Start(context.Background()))
	t.Cleanup(func() { _ = tl.Stop() })
	return tl
}

func TestTailerStartsAtEnd(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "0.log")
	appendLines(t, log, 0, 50)

	c := &collector{}
	startTailer(t, Config{Glob: glob(dir)}, c)
	time.Sleep(300 * time.Millisecond)
	appendLines(t, log, 50, 60)

	waitForSet(c, wantTexts(50, 60), 3*time.Second)
	got := c.snapshot()
	require.Equal(t, wantTexts(50, 60), got)
}

func TestNewRejectsEmptyGlob(t *testing.T) {
	_, err := New(Config{}, func(Line) {})
	require.Error(t, err)
}
