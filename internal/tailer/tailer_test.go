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

func TestTailerContainerRestartNoLoss(t *testing.T) {
	dir := t.TempDir()
	log0 := filepath.Join(dir, "0.log")
	appendLines(t, log0, 0, 1)

	c := &collector{}
	startTailer(t, Config{Glob: glob(dir)}, c)
	time.Sleep(200 * time.Millisecond)

	appendLines(t, log0, 1, 101)
	log1 := filepath.Join(dir, "1.log")
	appendLines(t, log1, 101, 201)

	waitForSet(c, wantTexts(1, 201), 5*time.Second)
	missing, dup := missingAndDup(c.snapshot(), wantTexts(1, 201))
	require.Equal(t, 0, missing)
	require.Equal(t, 0, dup)
}

func TestTailerPodDeletionDrainsOpenFile(t *testing.T) {
	dir := t.TempDir()
	log0 := filepath.Join(dir, "0.log")
	appendLines(t, log0, 0, 1)

	c := &collector{}
	startTailer(t, Config{Glob: glob(dir)}, c)
	time.Sleep(200 * time.Millisecond)

	appendLines(t, log0, 1, 101)
	require.NoError(t, os.Remove(log0))

	waitForSet(c, wantTexts(1, 101), 5*time.Second)
	missing, dup := missingAndDup(c.snapshot(), wantTexts(1, 101))
	require.Equal(t, 0, missing)
	require.Equal(t, 0, dup)
}

func TestTailerKeepsStateDuringBriefAbsence(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "0.log")
	appendLines(t, log, 0, 1)

	c := &collector{}
	cfg := Config{Glob: glob(dir), PollInterval: 50 * time.Millisecond, GracePolls: 3}
	startTailer(t, cfg, c)
	time.Sleep(150 * time.Millisecond)
	appendLines(t, log, 1, 11)
	waitForSet(c, wantTexts(1, 11), 2*time.Second)

	// Move the file out of the glob for one poll, then move it back with the
	// same inode and append more lines. No line is replayed or lost.
	hidden := filepath.Join(dir, "0.hidden")
	require.NoError(t, os.Rename(log, hidden))
	time.Sleep(60 * time.Millisecond)
	require.NoError(t, os.Rename(hidden, log))
	appendLines(t, log, 11, 21)

	waitForSet(c, wantTexts(11, 21), 3*time.Second)
	missing, dup := missingAndDup(c.snapshot(), wantTexts(1, 21))
	require.Equal(t, 0, missing)
	require.Equal(t, 0, dup)
}

func TestTailerInodeReuseReadsFromStart(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "0.log")
	appendLines(t, log, 0, 1)

	c := &collector{}
	cfg := Config{Glob: glob(dir), PollInterval: 50 * time.Millisecond, GracePolls: 1}
	startTailer(t, cfg, c)
	time.Sleep(150 * time.Millisecond)
	appendLines(t, log, 1, 11)
	waitForSet(c, wantTexts(1, 11), 2*time.Second)

	// Remove the file and wait past the grace period so it is pruned.
	require.NoError(t, os.Remove(log))
	time.Sleep(300 * time.Millisecond)
	// A new file appears. It may reuse the inode. It must read from the start.
	appendLines(t, log, 100, 110)
	waitForSet(c, wantTexts(100, 110), 3*time.Second)
	missing, dup := missingAndDup(c.snapshot(), append(wantTexts(1, 11), wantTexts(100, 110)...))
	require.Equal(t, 0, missing)
	require.Equal(t, 0, dup)
}

func TestTailerIgnoresCompressedAndTempFiles(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "0.log")
	appendLines(t, log, 0, 1)
	// Pre-existing rotated artifacts that must never be read.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "0.log.20261029-120000.gz"),
		[]byte("2026-10-29T12:00:00.000000000Z stdout F should-not-read-gz\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "0.log.20261029-120001.tmp"),
		[]byte("2026-10-29T12:00:00.000000000Z stdout F should-not-read-tmp\n"), 0o600))

	c := &collector{}
	startTailer(t, Config{Glob: glob(dir)}, c)
	time.Sleep(300 * time.Millisecond)
	appendLines(t, log, 1, 11)
	waitForSet(c, wantTexts(1, 11), 3*time.Second)

	got := c.snapshot()
	require.NotContains(t, got, "should-not-read-gz")
	require.NotContains(t, got, "should-not-read-tmp")
	missing, dup := missingAndDup(got, wantTexts(1, 11))
	require.Equal(t, 0, missing)
	require.Equal(t, 0, dup)
}

func TestTailerDropsOversizeLine(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "0.log")
	appendLines(t, log, 0, 1)

	c := &collector{}
	cfg := Config{Glob: glob(dir), MaxLineBytes: 128, PollInterval: 50 * time.Millisecond}
	startTailer(t, cfg, c)
	time.Sleep(150 * time.Millisecond)

	// One oversize line, then a normal line.
	big := "2026-10-29T13:00:00.000000000Z stdout F "
	for len(big) < 500 {
		big += "A"
	}
	appendRaw(t, log, big+"\n")
	appendLines(t, log, 200, 201)

	waitForSet(c, []string{"msg-200"}, 3*time.Second)
	got := c.snapshot()
	require.Contains(t, got, "msg-200")
	for _, g := range got {
		require.LessOrEqual(t, len(g), 128, "an oversize line was emitted")
	}
}

func TestTailerIsolatesPerFileErrors(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission test is meaningless as root")
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "0.log")
	bad := filepath.Join(dir, "1.log")
	appendLines(t, good, 0, 1)
	require.NoError(t, os.WriteFile(bad, []byte("x\n"), 0o000))

	c := &collector{}
	startTailer(t, Config{Glob: glob(dir)}, c)
	time.Sleep(200 * time.Millisecond)
	appendLines(t, good, 1, 11)

	waitForSet(c, wantTexts(1, 11), 3*time.Second)
	missing, _ := missingAndDup(c.snapshot(), wantTexts(1, 11))
	require.Equal(t, 0, missing, "an unreadable sibling blocked the good file")
}

func TestTailerStopSavesAndCloses(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(t.TempDir(), "offsets.json")
	log := filepath.Join(dir, "0.log")
	appendLines(t, log, 0, 1)

	c := &collector{}
	tl := startTailer(t, Config{Glob: glob(dir), CheckpointPath: state}, c)
	time.Sleep(200 * time.Millisecond)
	appendLines(t, log, 1, 11)
	waitForSet(c, wantTexts(1, 11), 3*time.Second)
	require.NoError(t, tl.Stop())

	// The checkpoint file exists and holds one record with a non-zero offset.
	recs, ok := loadCheckpoints(state)
	require.True(t, ok)
	require.Len(t, recs, 1)
	require.Positive(t, recs[0].Offset)

	// A second Stop is safe.
	require.NoError(t, tl.Stop())
}

func TestTailerRotationUnderLoad(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "0.log")
	appendLines(t, log, 0, 1)

	c := &collector{}
	cfg := Config{Glob: glob(dir), PollInterval: 30 * time.Millisecond}
	startTailer(t, cfg, c)
	time.Sleep(100 * time.Millisecond)

	const total = 2000
	rotateEvery := 400
	next := 1
	for next < total {
		end := min(next+50, total)
		appendLines(t, log, next, end)
		next = end
		if next%rotateEvery == 0 {
			ts := time.Now().UnixNano()
			require.NoError(
				t,
				os.Rename(log, filepath.Join(dir, "0.log."+time.Unix(0, ts).Format("20060102-150405.000000000"))),
			)
		}
		time.Sleep(10 * time.Millisecond)
	}

	waitForSet(c, wantTexts(1, total), 8*time.Second)
	missing, dup := missingAndDup(c.snapshot(), wantTexts(1, total))
	require.Equal(t, 0, missing)
	require.Equal(t, 0, dup)
}

func TestTailerPrunesOldCheckpoints(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(t.TempDir(), "offsets.json")

	// Seed the offset file with one fresh and one stale record.
	fresh := savedCheckpoint{Fingerprint: []byte("fresh"), Offset: 5, LastSeen: time.Now()}
	stale := savedCheckpoint{Fingerprint: []byte("stale"), Offset: 9, LastSeen: time.Now().Add(-time.Hour)}
	require.NoError(t, saveCheckpoints(state, []savedCheckpoint{fresh, stale}))

	// Start a tailer with no matching files and a short retention. After one
	// save, the stale record is gone and the fresh record is kept.
	log := filepath.Join(dir, "0.log")
	appendLines(t, log, 0, 5)
	c := &collector{}
	cfg := Config{
		Glob:           glob(dir),
		CheckpointPath: state,
		PollInterval:   50 * time.Millisecond,
		Retention:      10 * time.Minute,
	}
	tl := startTailer(t, cfg, c)
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, tl.Stop())

	recs, ok := loadCheckpoints(state)
	require.True(t, ok)
	var fingerprints []string
	for _, r := range recs {
		fingerprints = append(fingerprints, string(r.Fingerprint))
	}
	require.Contains(t, fingerprints, "fresh")
	require.NotContains(t, fingerprints, "stale")
}

func TestTailerResumesAcrossSplitLineRestart(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(t.TempDir(), "offsets.json")
	log := filepath.Join(dir, "0.log")

	// A dummy line present before the first run so first-run alignment
	// starts right after it (see TestTailerFirstRunAlignsToLineBoundary).
	appendLines(t, log, 0, 1)

	c := &collector{}
	tl := startTailer(t, Config{Glob: glob(dir), CheckpointPath: state, PollInterval: 50 * time.Millisecond}, c)
	time.Sleep(100 * time.Millisecond)

	// Only the P half of a split line arrives while the tailer runs, then
	// the tailer stops (flushing a checkpoint) before the F half appears.
	appendRaw(t, log, "2026-10-29T13:00:00.000000000Z stdout P split-part1-\n")
	time.Sleep(150 * time.Millisecond)
	require.NoError(t, tl.Stop())

	// Nothing partial was emitted before the F half arrives.
	for _, g := range c.snapshot() {
		require.NotEqual(t, "split-part1-", g, "a partial line was emitted")
	}

	// A fresh tailer resumes from the checkpoint; the F half now arrives.
	c2 := &collector{}
	tl2 := startTailer(t, Config{Glob: glob(dir), CheckpointPath: state, PollInterval: 50 * time.Millisecond}, c2)
	time.Sleep(150 * time.Millisecond)
	appendRaw(t, log, "2026-10-29T13:00:00.000000000Z stdout F split-part2\n")
	waitForSet(c2, []string{"split-part1-split-part2"}, 3*time.Second)
	require.NoError(t, tl2.Stop())

	got := c2.snapshot()
	require.Contains(t, got, "split-part1-split-part2")
	// The joined line appears exactly once and no partial remnant appears.
	n := 0
	for _, g := range got {
		if g == "split-part1-split-part2" {
			n++
		}
		require.NotEqual(t, "split-part2", g, "only the F remainder was emitted (P content lost)")
	}
	require.Equal(t, 1, n)
}
