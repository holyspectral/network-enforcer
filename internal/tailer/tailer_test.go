package tailer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// startTailer builds and starts a tailer. It stops the tailer at test end
// and returns the tailer for test-specific control.
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

func TestTailerStopWithoutStart(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Glob: glob(dir)}
	tl, err := New(cfg, func(Line) {})
	require.NoError(t, err)
	err = tl.Stop()
	require.NoError(t, err)
}

func TestTailerNoPartialLine(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "0.log")
	appendLines(t, log, 0, 1)

	c := &collector{}
	startTailer(t, Config{Glob: glob(dir)}, c)
	time.Sleep(300 * time.Millisecond)

	full := criLine(100) + "\n"
	half := len(full) / 2
	appendRaw(t, log, full[:half])
	time.Sleep(300 * time.Millisecond)
	require.Empty(t, c.snapshot(), "a partial line was emitted")

	appendRaw(t, log, full[half:])
	waitForSet(c, wantTexts(100, 101), 3*time.Second)
	require.Equal(t, wantTexts(100, 101), c.snapshot())
}

func TestTailerFirstRunAlignsToLineBoundary(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "0.log")
	// Write one full line, then a half line with no newline.
	appendLines(t, log, 0, 1)
	appendRaw(t, log, "2026-10-29T13:00:00.000000000Z stdout F partial-start")

	c := &collector{}
	startTailer(t, Config{Glob: glob(dir)}, c)
	time.Sleep(300 * time.Millisecond)
	// Complete the half line, then add a clean line.
	appendRaw(t, log, "-end\n")
	appendLines(t, log, 200, 201)

	waitForSet(c, []string{"msg-200"}, 3*time.Second)
	got := c.snapshot()
	// The tailer must not emit "partial-start-end": it began after the last
	// newline, so it dropped the broken head and kept the clean tail.
	require.NotContains(t, got, "partial-start-end")
	require.Contains(t, got, "msg-200")
}

func TestTailerShortFirstLine(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "0.log")
	state := filepath.Join(t.TempDir(), "offsets.json")

	// A first line far shorter than fingerprintSize.
	appendRaw(t, log, "2026-10-29T13:00:00.000000000Z stdout F x\n")
	c := &collector{}
	tl := startTailer(t, Config{Glob: glob(dir), CheckpointPath: state}, c)
	time.Sleep(300 * time.Millisecond)
	appendLines(t, log, 0, 20)
	waitForSet(c, wantTexts(0, 20), 3*time.Second)
	require.NoError(t, tl.Stop())

	// A restart resumes with no replay of the short line or the batch.
	c2 := &collector{}
	startTailer(t, Config{Glob: glob(dir), CheckpointPath: state}, c2)
	appendLines(t, log, 20, 30)
	waitForSet(c2, wantTexts(20, 30), 3*time.Second)
	missing, dup := missingAndDup(c2.snapshot(), wantTexts(20, 30))
	require.Equal(t, 0, missing)
	require.Equal(t, 0, dup)
}

func TestTailerRestartResumesFromSavedOffset(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(t.TempDir(), "offsets.json")
	log := filepath.Join(dir, "0.log")
	appendLines(t, log, 0, 1)

	c1 := &collector{}
	tl := startTailer(t, Config{Glob: glob(dir), CheckpointPath: state}, c1)
	time.Sleep(200 * time.Millisecond)
	appendLines(t, log, 1, 51)
	waitForSet(c1, wantTexts(1, 51), 3*time.Second)
	require.NoError(t, tl.Stop())

	// Lines written while the agent is down.
	appendLines(t, log, 51, 101)

	c2 := &collector{}
	startTailer(t, Config{Glob: glob(dir), CheckpointPath: state}, c2)
	waitForSet(c2, wantTexts(51, 101), 3*time.Second)
	got := c2.snapshot()
	missing, dup := missingAndDup(got, wantTexts(51, 101))
	require.Equal(t, 0, missing)
	require.Equal(t, 0, dup)
	// No replay of the first run's lines.
	for _, old := range wantTexts(0, 51) {
		require.NotContains(t, got, old)
	}
}

func TestTailerRenameRotationNoLoss(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "0.log")
	appendLines(t, log, 0, 1)

	c := &collector{}
	startTailer(t, Config{Glob: glob(dir)}, c)
	time.Sleep(200 * time.Millisecond)

	appendLines(t, log, 1, 101)
	// Rotate by rename, then the runtime writes a new 0.log.
	require.NoError(t, os.Rename(log, filepath.Join(dir, "0.log.20261029-130000")))
	appendLines(t, log, 101, 201)

	waitForSet(c, wantTexts(1, 201), 5*time.Second)
	missing, dup := missingAndDup(c.snapshot(), wantTexts(1, 201))
	require.Equal(t, 0, missing)
	require.Equal(t, 0, dup)
}

func TestTailerCopyTruncate(t *testing.T) {
	cases := []struct {
		name   string
		regrow bool
	}{
		{name: "truncate to zero then grow", regrow: false},
		{name: "truncate and regrow past old offset between polls", regrow: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "0.log")
			appendLines(t, log, 0, 1)

			c := &collector{}
			cfg := Config{Glob: glob(dir), PollInterval: 50 * time.Millisecond}
			startTailer(t, cfg, c)
			time.Sleep(150 * time.Millisecond)

			appendLines(t, log, 1, 101)
			waitForSet(c, wantTexts(1, 101), 3*time.Second)

			require.NoError(t, os.Truncate(log, 0))
			if tc.regrow {
				// Regrow past the old offset within one poll window.
				appendLines(t, log, 101, 301)
			} else {
				time.Sleep(100 * time.Millisecond)
				appendLines(t, log, 101, 201)
			}

			end := 201
			if tc.regrow {
				end = 301
			}
			waitForSet(c, wantTexts(101, end), 4*time.Second)
			missing, dup := missingAndDup(c.snapshot(), wantTexts(1, end))
			require.Equal(t, 0, missing)
			require.Equal(t, 0, dup)
		})
	}
}

func TestTailerRestartAfterCopyTruncate(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(t.TempDir(), "offsets.json")
	log := filepath.Join(dir, "0.log")
	appendLines(t, log, 0, 1)

	c1 := &collector{}
	tl := startTailer(t, Config{Glob: glob(dir), CheckpointPath: state}, c1)
	time.Sleep(200 * time.Millisecond)
	appendLines(t, log, 1, 51)
	waitForSet(c1, wantTexts(1, 51), 3*time.Second)
	require.NoError(t, os.Truncate(log, 0))
	time.Sleep(150 * time.Millisecond)
	appendLines(t, log, 51, 101)
	waitForSet(c1, wantTexts(51, 101), 3*time.Second)
	require.NoError(t, tl.Stop())

	appendLines(t, log, 101, 151)
	c2 := &collector{}
	startTailer(t, Config{Glob: glob(dir), CheckpointPath: state}, c2)
	waitForSet(c2, wantTexts(101, 151), 3*time.Second)
	got := c2.snapshot()
	missing, dup := missingAndDup(got, wantTexts(101, 151))
	require.Equal(t, 0, missing)
	require.Equal(t, 0, dup)
	for _, old := range wantTexts(1, 101) {
		require.NotContains(t, got, old)
	}
}
