// Package common holds the shared scenarios and benchmark driver for tailer options.
package common

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Emit reports one complete line without the trailing newline. readAt is the
// time the tailer read the bytes from the OS. A tailer that cannot observe its
// own read time passes the zero value, and the benchmark then measures only the
// end-to-end latency.
type Emit func(line []byte, readAt time.Time)

// Factory starts a tailer on glob. It keeps offsets in state ("" means in memory).
// It calls onLine for each complete line.
type Factory func(glob, state string, onLine Emit) (stop func(), err error)

// CRILine returns one containerd CRI format line.
func CRILine(i int) string {
	return fmt.Sprintf(
		"2026-10-29T13:00:00.%09dZ stdout F {\"n\":%d,\"msg\":\"[2026-10-29T13:00:00.000Z] \\\"GET /x HTTP/1.1\\\" 200 - via_upstream\"}",
		i%1000000000,
		i,
	)
}

type collector struct {
	mu    sync.Mutex
	lines []string
}

func (c *collector) add(b []byte, _ time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, string(b))
}

func (c *collector) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...)
}

// waitSet waits until every line in want is present, then settles so a late
// duplicate or replay can still appear in the snapshot.
func (c *collector) waitSet(want []string, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if missing, _ := missingAndDup(c.snapshot(), want); missing == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond)
}

func appendLines(t *testing.T, path string, from, to int) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i := from; i < to; i++ {
		if _, err := f.WriteString(CRILine(i) + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func appendRaw(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func expected(from, to int) []string {
	var out []string
	for i := from; i < to; i++ {
		out = append(out, CRILine(i))
	}
	return out
}

func missingAndDup(got, want []string) (missing, dup int) {
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		if seen[w] == 0 {
			missing++
		}
	}
	for k, c := range seen {
		if c > 1 {
			dup += c - 1
		}
		if !wantSet[k] {
			dup += c // an unexpected line counts as a duplicate or replay
		}
	}
	return
}

func start(t *testing.T, f Factory, glob, state string, c *collector) func() {
	t.Helper()
	stop, err := f(glob, state, c.add)
	if err != nil {
		t.Fatal(err)
	}
	return stop
}

// RunScenarios checks the hard criteria for one tailer option.
func RunScenarios(t *testing.T, f Factory) {
	t.Run("start_at_end_on_first_run", func(t *testing.T) {
		dir := t.TempDir()
		log := filepath.Join(dir, "0.log")
		appendLines(t, log, 0, 50)
		c := &collector{}
		stop := start(t, f, filepath.Join(dir, "*.log*"), "", c)
		defer stop()
		time.Sleep(time.Second)
		appendLines(t, log, 50, 60)
		c.waitSet(expected(50, 60), 5*time.Second)
		got := c.snapshot()
		missing, dup := missingAndDup(got, expected(50, 60))
		if missing != 0 || dup != 0 || len(got) != 10 {
			t.Fatalf("want exactly lines 50..59, got %d lines, %d missing, %d dup", len(got), missing, dup)
		}
	})

	t.Run("no_partial_lines", func(t *testing.T) {
		dir := t.TempDir()
		log := filepath.Join(dir, "0.log")
		appendLines(t, log, 0, 1)
		c := &collector{}
		stop := start(t, f, filepath.Join(dir, "*.log*"), "", c)
		defer stop()
		time.Sleep(time.Second)
		full := CRILine(100) + "\n"
		appendRaw(t, log, full[:40])
		time.Sleep(1500 * time.Millisecond)
		if n := len(c.snapshot()); n != 0 {
			t.Fatalf("partial line emitted: %v", c.snapshot())
		}
		appendRaw(t, log, full[40:])
		c.waitSet([]string{CRILine(100)}, 5*time.Second)
		got := c.snapshot()
		if len(got) != 1 || got[0] != CRILine(100) {
			t.Fatalf("want one whole line, got %q", got)
		}
	})

	t.Run("rename_rotation_no_loss", func(t *testing.T) {
		dir := t.TempDir()
		log := filepath.Join(dir, "0.log")
		appendLines(t, log, 0, 1)
		c := &collector{}
		stop := start(t, f, filepath.Join(dir, "*.log*"), "", c)
		defer stop()
		time.Sleep(time.Second)
		appendLines(t, log, 1, 101)
		// Rotate at once: data may sit unread in the old file.
		if err := os.Rename(log, filepath.Join(dir, "0.log.20261029-130000")); err != nil {
			t.Fatal(err)
		}
		appendLines(t, log, 101, 201)
		c.waitSet(expected(1, 201), 8*time.Second)
		missing, dup := missingAndDup(c.snapshot(), expected(1, 201))
		if missing != 0 || dup != 0 {
			t.Fatalf("missing=%d dup=%d total=%d", missing, dup, len(c.snapshot()))
		}
	})

	t.Run("copytruncate_no_loss", func(t *testing.T) {
		dir := t.TempDir()
		log := filepath.Join(dir, "0.log")
		appendLines(t, log, 0, 1)
		c := &collector{}
		stop := start(t, f, filepath.Join(dir, "*.log*"), "", c)
		defer stop()
		time.Sleep(time.Second)
		appendLines(t, log, 1, 101)
		c.waitSet(expected(1, 101), 5*time.Second)
		if err := os.Truncate(log, 0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		appendLines(t, log, 101, 201)
		c.waitSet(expected(1, 201), 5*time.Second)
		missing, dup := missingAndDup(c.snapshot(), expected(1, 201))
		if missing != 0 || dup != 0 {
			t.Fatalf("missing=%d dup=%d total=%d", missing, dup, len(c.snapshot()))
		}
	})

	t.Run("container_restart_no_loss", func(t *testing.T) {
		dir := t.TempDir()
		log0 := filepath.Join(dir, "0.log")
		appendLines(t, log0, 0, 1)
		c := &collector{}
		stop := start(t, f, filepath.Join(dir, "*.log*"), "", c)
		defer stop()
		time.Sleep(time.Second)
		// The container restarts. The kubelet keeps 0.log and the runtime writes a
		// new 1.log. Some lines may sit unread in 0.log at the restart. The tailer
		// must drain 0.log to the end and read 1.log from the start.
		appendLines(t, log0, 1, 101)
		log1 := filepath.Join(dir, "1.log")
		appendLines(t, log1, 101, 201)
		c.waitSet(expected(1, 201), 8*time.Second)
		missing, dup := missingAndDup(c.snapshot(), expected(1, 201))
		if missing != 0 || dup != 0 {
			t.Fatalf("missing=%d dup=%d total=%d", missing, dup, len(c.snapshot()))
		}
	})

	t.Run("pod_deletion_drains_open_file", func(t *testing.T) {
		dir := t.TempDir()
		log0 := filepath.Join(dir, "0.log")
		appendLines(t, log0, 0, 1)
		c := &collector{}
		stop := start(t, f, filepath.Join(dir, "*.log*"), "", c)
		defer stop()
		time.Sleep(time.Second)
		// The pod is deleted. The kubelet removes the log file. Lines written just
		// before the removal may sit unread. The tailer holds the open descriptor,
		// so it must drain them before it closes the descriptor.
		appendLines(t, log0, 1, 101)
		if err := os.Remove(log0); err != nil {
			t.Fatal(err)
		}
		c.waitSet(expected(1, 101), 8*time.Second)
		missing, dup := missingAndDup(c.snapshot(), expected(1, 101))
		if missing != 0 || dup != 0 {
			t.Fatalf("missing=%d dup=%d total=%d", missing, dup, len(c.snapshot()))
		}
	})

	t.Run("restart_resumes_from_saved_offset", func(t *testing.T) {
		dir := t.TempDir()
		state := filepath.Join(t.TempDir(), "offsets.json")
		log := filepath.Join(dir, "0.log")
		appendLines(t, log, 0, 1)
		c1 := &collector{}
		stop := start(t, f, filepath.Join(dir, "*.log*"), state, c1)
		time.Sleep(time.Second)
		appendLines(t, log, 1, 51)
		c1.waitSet(expected(1, 51), 5*time.Second)
		stop()
		appendLines(t, log, 51, 101) // written while the agent is down
		c2 := &collector{}
		stop2 := start(t, f, filepath.Join(dir, "*.log*"), state, c2)
		defer stop2()
		c2.waitSet(expected(51, 101), 5*time.Second)
		got := c2.snapshot()
		missing, dup := missingAndDup(got, expected(51, 101))
		replayed := 0
		old := map[string]bool{}
		for _, l := range expected(0, 51) {
			old[l] = true
		}
		for _, g := range got {
			if old[g] {
				replayed++
			}
		}
		if missing != 0 || dup != 0 || replayed != 0 {
			t.Fatalf("missing=%d dup=%d replayed=%d total=%d", missing, dup, replayed, len(got))
		}
	})

	t.Run("restart_after_copytruncate", func(t *testing.T) {
		dir := t.TempDir()
		state := filepath.Join(t.TempDir(), "offsets.json")
		log := filepath.Join(dir, "0.log")
		appendLines(t, log, 0, 1)
		c1 := &collector{}
		stop := start(t, f, filepath.Join(dir, "*.log*"), state, c1)
		time.Sleep(time.Second)
		appendLines(t, log, 1, 51)
		c1.waitSet(expected(1, 51), 5*time.Second)
		if err := os.Truncate(log, 0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		appendLines(t, log, 51, 101)
		c1.waitSet(expected(51, 101), 5*time.Second)
		stop()
		appendLines(t, log, 101, 151) // written while the agent is down
		c2 := &collector{}
		stop2 := start(t, f, filepath.Join(dir, "*.log*"), state, c2)
		defer stop2()
		c2.waitSet(expected(101, 151), 5*time.Second)
		got := c2.snapshot()
		missing, dup := missingAndDup(got, expected(101, 151))
		replayed := 0
		old := map[string]bool{}
		for _, l := range expected(1, 101) {
			old[l] = true
		}
		for _, g := range got {
			if old[g] {
				replayed++
			}
		}
		if missing != 0 || dup != 0 || replayed != 0 {
			t.Fatalf("missing=%d dup=%d replayed=%d total=%d", missing, dup, replayed, len(got))
		}
	})
}

// ParseRate reads a lines/s rate from args[1], or returns 0 when absent.
// It exits on a non-numeric value, so a typo does not run at rate 0 silently.
func ParseRate(args []string) int {
	if len(args) < 2 {
		return 0
	}
	n, err := strconv.Atoi(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad rate %q: %v\n", args[1], err)
		os.Exit(2)
	}
	return n
}
