# File Tailer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a custom, project-owned, stdlib-only tailer in `internal/tailer` that reads containerd CRI container logs and emits complete lines, meeting every hard criterion and every "Production scope" item in RFC 0006.

**Architecture:** A poll-based tailer tracks files by inode. It keeps each descriptor open so it drains renamed and removed files. It matches saved checkpoints by a file fingerprint. A CRI parser frames lines and joins partial (`P`) records. A checkpoint store writes offsets atomically.

**Tech Stack:** Go (stdlib only for production code), `log/slog` for logging, `testify` for tests. No third-party runtime dependencies.

**Spec:** `docs/superpowers/specs/2026-11-02-file-tailer-design.md`

## Global Constraints

- Production code uses the Go standard library only. No third-party imports in non-test files under `internal/tailer`.
- Go version floor: `go 1.27.2` (from `go.mod`).
- Logging uses `log/slog`, injected as a `*slog.Logger` field (match `internal/scraper`). Follow the Kubernetes log message style: start with a capital letter, no trailing period, past tense, name the object.
- Comments and documentation use Simplified Technical English: short sentences, one idea each, active voice, simple present tense.
- Tests use `testify` (`assert` and `require`) and table-driven style with a single `Test<FunctionName>` per function under test. Use `t.TempDir()` for files. No mocks.
- Never discard errors with `_, _ =`. Use `errors.Is` for error comparison. Use named fields in struct literals. Run `gofmt`.
- Delivery is at-least-once. Duplicates after a crash are acceptable.
- Commit each task with `git commit -s` (sign-off). Do not add a co-author trailer for the user; keep the Copilot co-author trailer.

## Review Focus

- **CRI timestamp absent or malformed** (M1 Task 1): a line whose first field is not an RFC3339 time must return an error and not panic. Covered by Task 1 Step 1.
- **A write that lands exactly on the poll boundary** (M2 Task 5): a line half-written at one poll and completed at the next must be emitted once, not twice and not dropped. Covered by Task 5.
- **Inode reuse after a file is deleted** (M4 Task 12): a new file that reuses the inode of a pruned file must read from offset 0, not resume the old offset. Covered by Task 12.
- **A corrupt checkpoint file on start** (M1 Task 3): a truncated or non-JSON offset file must not stop the tailer; it starts at the end. Covered by Task 3.
- **A fingerprint read on a file shorter than the fingerprint size** (M2 Task 6): a first line shorter than 256 bytes must not block tracking; the fingerprint refills as the file grows. Covered by Task 6.

---

## File Structure

- `internal/tailer/cri.go` — CRI line parsing and partial-record joining.
- `internal/tailer/cri_test.go`
- `internal/tailer/checkpoint.go` — atomic offset store.
- `internal/tailer/checkpoint_test.go`
- `internal/tailer/tailer.go` — `Config`, `Line`, `Handler`, `Tailer` interface, poll loop, start, stop.
- `internal/tailer/tailer_test.go` — poller, rotation, lifecycle and resilience scenarios.
- `internal/tailer/testhelpers_test.go` — shared test helpers (temp files, line sets, collector).

---

## Task 1: CRI line parser

**Files:**
- Create: `internal/tailer/cri.go`
- Test: `internal/tailer/cri_test.go`

**Interfaces:**
- Produces:
  - `type criRecord struct { Time time.Time; Stream string; Partial bool; Text []byte }`
  - `func parseCRI(line []byte) (criRecord, error)`

The CRI format is `<RFC3339Nano time> <stream> <P|F> <content>`. `stream` is `stdout` or `stderr`. `P` marks a partial record; `F` marks a full record.

- [ ] **Step 1: Write the failing test**

```go
package tailer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCRI(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		wantStream string
		wantPart   bool
		wantText   string
		wantErr    bool
	}{
		{
			name:       "full stdout line",
			line:       "2026-10-29T13:00:00.000000001Z stdout F hello world",
			wantStream: "stdout",
			wantPart:   false,
			wantText:   "hello world",
		},
		{
			name:       "partial stderr line",
			line:       "2026-10-29T13:00:00.000000001Z stderr P chunk",
			wantStream: "stderr",
			wantPart:   true,
			wantText:   "chunk",
		},
		{
			name:     "empty content",
			line:     "2026-10-29T13:00:00.000000001Z stdout F ",
			wantText: "",
		},
		{
			name:    "missing fields",
			line:    "2026-10-29T13:00:00Z stdout",
			wantErr: true,
		},
		{
			name:    "bad timestamp",
			line:    "not-a-time stdout F hello",
			wantErr: true,
		},
		{
			name:    "bad flag",
			line:    "2026-10-29T13:00:00.000000001Z stdout X hello",
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec, err := parseCRI([]byte(c.line))
			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.wantStream, rec.Stream)
			assert.Equal(t, c.wantPart, rec.Partial)
			assert.Equal(t, c.wantText, string(rec.Text))
			assert.False(t, rec.Time.IsZero())
			assert.Equal(t, time.UTC, rec.Time.UTC().Location())
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tailer/ -run TestParseCRI -v`
Expected: FAIL — `undefined: parseCRI`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package tailer reads containerd CRI container logs and emits complete lines.
package tailer

import (
	"bytes"
	"errors"
	"time"
)

// criRecord is one parsed CRI log record.
type criRecord struct {
	Time    time.Time
	Stream  string
	Partial bool
	Text    []byte
}

var errBadCRI = errors.New("bad CRI line")

// parseCRI reads one CRI record from a raw log line without the newline.
// The format is "<time> <stream> <P|F> <content>".
func parseCRI(line []byte) (criRecord, error) {
	// Split into at most four fields. The content can hold spaces.
	first := bytes.IndexByte(line, ' ')
	if first < 0 {
		return criRecord{}, errBadCRI
	}
	rest := line[first+1:]
	second := bytes.IndexByte(rest, ' ')
	if second < 0 {
		return criRecord{}, errBadCRI
	}
	stream := rest[:second]
	rest = rest[second+1:]
	third := bytes.IndexByte(rest, ' ')
	if third < 0 {
		return criRecord{}, errBadCRI
	}
	flag := rest[:third]
	content := rest[third+1:]

	t, err := time.Parse(time.RFC3339Nano, string(line[:first]))
	if err != nil {
		return criRecord{}, errBadCRI
	}
	var partial bool
	switch string(flag) {
	case "P":
		partial = true
	case "F":
		partial = false
	default:
		return criRecord{}, errBadCRI
	}
	return criRecord{Time: t, Stream: string(stream), Partial: partial, Text: content}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tailer/ -run TestParseCRI -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tailer/cri.go internal/tailer/cri_test.go
git commit -s -m "feat(tailer): add CRI line parser"
```

---

## Task 2: CRI partial-record joiner

**Files:**
- Modify: `internal/tailer/cri.go`
- Test: `internal/tailer/cri_test.go`

**Interfaces:**
- Produces:
  - `type criJoiner struct { ... }`
  - `func (j *criJoiner) add(rec criRecord) (criRecord, bool)` — returns a full record and `true` when a line is complete, else `false`.

A sequence of `P` records followed by one `F` record forms one line. The joiner keeps the time and stream of the first record in the group.

- [ ] **Step 1: Write the failing test**

```go
func TestCriJoiner(t *testing.T) {
	cases := []struct {
		name  string
		recs  []criRecord
		wantN int
		wantL []string
	}{
		{
			name:  "single full line",
			recs:  []criRecord{{Stream: "stdout", Text: []byte("abc")}},
			wantN: 1,
			wantL: []string{"abc"},
		},
		{
			name: "two partials then full",
			recs: []criRecord{
				{Stream: "stdout", Partial: true, Text: []byte("ab")},
				{Stream: "stdout", Partial: true, Text: []byte("cd")},
				{Stream: "stdout", Partial: false, Text: []byte("ef")},
			},
			wantN: 1,
			wantL: []string{"abcdef"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var j criJoiner
			var got []string
			for _, r := range c.recs {
				if out, ok := j.add(r); ok {
					got = append(got, string(out.Text))
				}
			}
			assert.Equal(t, c.wantN, len(got))
			assert.Equal(t, c.wantL, got)
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tailer/ -run TestCriJoiner -v`
Expected: FAIL — `undefined: criJoiner`.

- [ ] **Step 3: Write minimal implementation**

Add to `cri.go`:

```go
// criJoiner joins partial CRI records into one line. It keeps the time and the
// stream of the first record in the group.
type criJoiner struct {
	buf    []byte
	time   time.Time
	stream string
	open   bool
}

// add takes one record. It returns the joined record and true when the line is
// complete. It returns false while the line is still partial.
func (j *criJoiner) add(rec criRecord) (criRecord, bool) {
	if !j.open {
		j.time = rec.Time
		j.stream = rec.Stream
		j.buf = j.buf[:0]
	}
	j.buf = append(j.buf, rec.Text...)
	if rec.Partial {
		j.open = true
		return criRecord{}, false
	}
	j.open = false
	out := criRecord{Time: j.time, Stream: j.stream, Text: append([]byte(nil), j.buf...)}
	return out, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tailer/ -run TestCriJoiner -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tailer/cri.go internal/tailer/cri_test.go
git commit -s -m "feat(tailer): join partial CRI records"
```

---

## Task 3: Checkpoint store

**Files:**
- Create: `internal/tailer/checkpoint.go`
- Test: `internal/tailer/checkpoint_test.go`

**Interfaces:**
- Produces:
  - `type checkpoint struct { Fingerprint []byte; Offset int64; MissingPolls int; LastSeen time.Time }` (JSON tags: `fingerprint`, `offset`, omit `MissingPolls` and `LastSeen` from the on-disk form with `json:"-"` — they are runtime only).

  Correction: the on-disk record holds only `Fingerprint` and `Offset`. Define a separate on-disk type:
  - `type savedCheckpoint struct { Fingerprint []byte \`json:"fingerprint"\`; Offset int64 \`json:"offset"\` }`
  - `func loadCheckpoints(path string) ([]savedCheckpoint, bool)` — returns the records and `true` when the file loaded. A missing or corrupt file returns `nil, false`.
  - `func saveCheckpoints(path string, recs []savedCheckpoint) error` — writes atomically with `fsync` and rename.

- [ ] **Step 1: Write the failing test**

```go
package tailer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckpointRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "offsets.json")
	want := []savedCheckpoint{
		{Fingerprint: []byte("abc"), Offset: 10},
		{Fingerprint: []byte("def"), Offset: 20},
	}
	require.NoError(t, saveCheckpoints(path, want))

	got, ok := loadCheckpoints(path)
	require.True(t, ok)
	assert.Equal(t, want, got)

	// The write leaves no temporary file behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestLoadCheckpointsMissingOrCorrupt(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "none.json")
	_, ok := loadCheckpoints(missing)
	assert.False(t, ok)

	corrupt := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(corrupt, []byte("{not json"), 0o600))
	_, ok = loadCheckpoints(corrupt)
	assert.False(t, ok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tailer/ -run TestCheckpoint -v && go test ./internal/tailer/ -run TestLoadCheckpoints -v`
Expected: FAIL — `undefined: savedCheckpoint`.

- [ ] **Step 3: Write minimal implementation**

```go
package tailer

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// savedCheckpoint is one offset record on disk.
type savedCheckpoint struct {
	Fingerprint []byte `json:"fingerprint"`
	Offset      int64  `json:"offset"`
}

// loadCheckpoints reads the offset file. It returns false when the file is
// absent or corrupt, so the caller starts at the end.
func loadCheckpoints(path string) ([]savedCheckpoint, bool) {
	if path == "" {
		return nil, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var recs []savedCheckpoint
	if err := json.Unmarshal(raw, &recs); err != nil {
		return nil, false
	}
	return recs, true
}

// saveCheckpoints writes the offset file atomically. It writes a temporary file,
// syncs it, renames it and syncs the directory.
func saveCheckpoints(path string, recs []savedCheckpoint) error {
	if path == "" {
		return nil
	}
	raw, err := json.Marshal(recs)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tailer/ -run 'TestCheckpoint|TestLoadCheckpoints' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tailer/checkpoint.go internal/tailer/checkpoint_test.go
git commit -s -m "feat(tailer): add atomic checkpoint store"
```

---

## Task 4: Test helpers, Config, Line, and the Tailer skeleton

**Files:**
- Create: `internal/tailer/tailer.go`
- Create: `internal/tailer/testhelpers_test.go`
- Test: `internal/tailer/tailer_test.go`

**Interfaces:**
- Produces:
  - `type Line struct { Path string; Stream string; Time time.Time; ReadAt time.Time; Text []byte }`
  - `type Handler func(Line)`
  - `type Config struct { Glob string; CheckpointPath string; PollInterval time.Duration; MaxLineBytes int; GracePolls int; Retention time.Duration; Logger *slog.Logger }`
  - `func New(cfg Config, h Handler) (*Tailer, error)`
  - `func (t *Tailer) Start(ctx context.Context) error`
  - `func (t *Tailer) Stop() error`
  - Defaults: `PollInterval` 200 ms, `MaxLineBytes` 1 MiB (1048576), `GracePolls` 2, `Retention` 10 min. `New` returns an error when `Glob` is empty.

This task builds the skeleton and the shared test helpers. The poll loop starts empty and is filled in Task 5. The first test asserts the start-at-end behavior, which drives the minimal loop.

- [ ] **Step 1: Write the failing test and the helpers**

Create `internal/tailer/testhelpers_test.go`:

```go
package tailer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// criLine returns one CRI full line with a stable content for index i.
func criLine(i int) string {
	return fmt.Sprintf("2026-10-29T13:00:00.%09dZ stdout F msg-%d", i%1000000000, i)
}

// appendLines appends CRI full lines [from, to) to path.
func appendLines(t *testing.T, path string, from, to int) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	defer f.Close()
	for i := from; i < to; i++ {
		_, err := f.WriteString(criLine(i) + "\n")
		require.NoError(t, err)
	}
}

// appendRaw appends a raw string to path.
func appendRaw(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	defer f.Close()
	_, err = f.WriteString(s)
	require.NoError(t, err)
}

// wantTexts returns the message texts for indices [from, to).
func wantTexts(from, to int) []string {
	var out []string
	for i := from; i < to; i++ {
		out = append(out, fmt.Sprintf("msg-%d", i))
	}
	return out
}

// collector stores received line texts for assertions.
type collector struct {
	mu   sync.Mutex
	got  []string
}

func (c *collector) handler(l Line) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, string(l.Text))
}

func (c *collector) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.got...)
}

// missingAndDup compares got against want. An unexpected line counts as a dup.
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
			dup += c
		}
	}
	return
}

// waitForSet polls until every want line is present, then settles so a late
// duplicate can still appear.
func waitForSet(c *collector, want []string, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if missing, _ := missingAndDup(c.snapshot(), want); missing == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(600 * time.Millisecond)
}

// sortedTexts returns a sorted copy for stable comparison.
func sortedTexts(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// glob returns the live-log glob for a directory.
func glob(dir string) string {
	return filepath.Join(dir, "*.log")
}
```

Create `internal/tailer/tailer_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tailer/ -run 'TestTailerStartsAtEnd|TestNewRejectsEmptyGlob' -v`
Expected: FAIL — `undefined: New`, `undefined: Config`, `undefined: Line`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/tailer/tailer.go`:

```go
package tailer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const fingerprintSize = 256

// Line is one complete log line emitted to the handler.
type Line struct {
	Path   string
	Stream string
	Time   time.Time
	ReadAt time.Time
	Text   []byte
}

// Handler receives one complete line.
type Handler func(Line)

// Config holds the tailer settings. Only Glob is required.
type Config struct {
	Glob           string
	CheckpointPath string
	PollInterval   time.Duration
	MaxLineBytes   int
	GracePolls     int
	Retention      time.Duration
	Logger         *slog.Logger
}

func (c *Config) applyDefaults() {
	if c.PollInterval == 0 {
		c.PollInterval = 200 * time.Millisecond
	}
	if c.MaxLineBytes == 0 {
		c.MaxLineBytes = 1 << 20
	}
	if c.GracePolls == 0 {
		c.GracePolls = 2
	}
	if c.Retention == 0 {
		c.Retention = 10 * time.Minute
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// fileState holds the runtime state for one tracked file.
type fileState struct {
	fingerprint []byte
	offset      int64
	f           *os.File
	path        string
	partial     []byte
	joiner      criJoiner
	missing     int
	gone        bool
}

// Tailer reads CRI log files and emits complete lines.
type Tailer struct {
	cfg      Config
	handler  Handler
	saved    []savedCheckpoint
	firstRun bool

	mu    sync.Mutex
	files map[uint64]*fileState

	stop chan struct{}
	done chan struct{}
}

// New builds a tailer. It returns an error when Glob is empty.
func New(cfg Config, h Handler) (*Tailer, error) {
	if cfg.Glob == "" {
		return nil, errors.New("tailer: Glob is required")
	}
	cfg.applyDefaults()
	t := &Tailer{
		cfg:      cfg,
		handler:  h,
		files:    map[uint64]*fileState{},
		firstRun: true,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	if recs, ok := loadCheckpoints(cfg.CheckpointPath); ok {
		t.saved = recs
		t.firstRun = false
	}
	return t, nil
}

// Start runs the first poll and then polls on the interval until Stop or the
// context ends.
func (t *Tailer) Start(ctx context.Context) error {
	t.poll(true)
	go func() {
		defer close(t.done)
		tick := time.NewTicker(t.cfg.PollInterval)
		defer tick.Stop()
		for {
			select {
			case <-t.stop:
				t.shutdown()
				return
			case <-ctx.Done():
				t.shutdown()
				return
			case <-tick.C:
				t.poll(false)
			}
		}
	}()
	return nil
}

// Stop ends the poll loop and waits for it.
func (t *Tailer) Stop() error {
	select {
	case <-t.stop:
	default:
		close(t.stop)
	}
	<-t.done
	return nil
}

func (t *Tailer) shutdown() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.save(); err != nil {
		t.cfg.Logger.Error("Could not save checkpoints", "err", err)
	}
	for _, s := range t.files {
		if err := s.f.Close(); err != nil {
			t.cfg.Logger.Error("Could not close file", "path", s.path, "err", err)
		}
	}
}

func inode(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}

func fingerprint(f *os.File) ([]byte, error) {
	buf := make([]byte, fingerprintSize)
	n, err := f.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:n], nil
}

// poll opens new files, reads each tracked file to EOF, then saves offsets.
func (t *Tailer) poll(initial bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	paths, err := filepath.Glob(t.cfg.Glob)
	if err != nil {
		t.cfg.Logger.Error("Could not glob logs", "glob", t.cfg.Glob, "err", err)
		return
	}
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		ino := inode(fi)
		if _, ok := t.files[ino]; ok {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		fp, err := fingerprint(f)
		if err != nil || len(fp) == 0 {
			f.Close()
			continue
		}
		st := &fileState{fingerprint: fp, f: f, path: p}
		if !t.matchSaved(st) && initial && t.firstRun {
			st.offset = fi.Size()
		}
		t.files[ino] = st
	}
	for _, s := range t.files {
		t.read(s)
	}
	if err := t.save(); err != nil {
		t.cfg.Logger.Error("Could not save checkpoints", "err", err)
	}
}

func (t *Tailer) matchSaved(st *fileState) bool {
	for _, sv := range t.saved {
		n := len(sv.Fingerprint)
		if n > 0 && len(st.fingerprint) >= n && bytes.Equal(st.fingerprint[:n], sv.Fingerprint) {
			st.offset = sv.Offset
			return true
		}
	}
	return false
}

// read reads one file to EOF and emits each complete line.
func (t *Tailer) read(s *fileState) {
	buf := make([]byte, 64*1024)
	for {
		n, err := s.f.ReadAt(buf, s.offset)
		if n > 0 {
			readAt := time.Now()
			s.offset += int64(n)
			s.partial = append(s.partial, buf[:n]...)
			for {
				i := bytes.IndexByte(s.partial, '\n')
				if i < 0 {
					break
				}
				t.emit(s, s.partial[:i], readAt)
				s.partial = s.partial[i+1:]
			}
		}
		if err != nil {
			break
		}
	}
}

// emit parses one raw line and sends a complete record to the handler.
func (t *Tailer) emit(s *fileState, raw []byte, readAt time.Time) {
	rec, err := parseCRI(raw)
	if err != nil {
		t.cfg.Logger.Warn("Could not parse CRI line", "path", s.path, "err", err)
		return
	}
	out, ok := s.joiner.add(rec)
	if !ok {
		return
	}
	t.handler(Line{
		Path:   s.path,
		Stream: out.Stream,
		Time:   out.Time,
		ReadAt: readAt,
		Text:   out.Text,
	})
}

// save writes the offset of the last complete line for each file.
func (t *Tailer) save() error {
	out := make([]savedCheckpoint, 0, len(t.files))
	for _, s := range t.files {
		out = append(out, savedCheckpoint{
			Fingerprint: s.fingerprint,
			Offset:      s.offset - int64(len(s.partial)),
		})
	}
	return saveCheckpoints(t.cfg.CheckpointPath, out)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/tailer/ -run 'TestTailerStartsAtEnd|TestNewRejectsEmptyGlob' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tailer/tailer.go internal/tailer/tailer_test.go internal/tailer/testhelpers_test.go
git commit -s -m "feat(tailer): add poll loop skeleton and start-at-end"
```

---

## Task 5: No partial line

**Files:**
- Modify: `internal/tailer/tailer_test.go`

**Interfaces:**
- Consumes: `New`, `Start`, `Stop`, `Config`, `Line` from Task 4.

The read loop from Task 4 already holds a partial buffer and emits only on `\n`. This task adds the test that pins the behavior. If the test fails, fix `read`.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails or passes**

Run: `go test -race ./internal/tailer/ -run TestTailerNoPartialLine -v`
Expected: This should PASS with the Task 4 implementation. If it FAILS, the read loop emits partial data; fix `read` so it only emits up to the last `\n`. Watch it fail first, then fix.

Note: a test that passes on first run proves the Task 4 code already satisfies it. That is acceptable here because the behavior is shared with Task 4; the value is the regression pin. Do not add production code in this task unless the test fails.

- [ ] **Step 3: Commit**

```bash
git add internal/tailer/tailer_test.go
git commit -s -m "test(tailer): pin no-partial-line behavior"
```

---

## Task 6: First-run line alignment and short first line

**Files:**
- Modify: `internal/tailer/tailer.go`
- Modify: `internal/tailer/tailer_test.go`

**Interfaces:**
- Consumes: `fileState`, `poll` from Task 4.
- Produces: an aligned first-run offset. On first run the offset moves forward to the byte after the last `\n` at or before the file size, so the first emitted line is whole.

The Task 4 code sets `st.offset = fi.Size()`, which can land mid-line. Fix: when starting at end on first run, scan back to the last `\n` so the next read begins on a line boundary. Also add a test for a first line shorter than the fingerprint size.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/tailer/ -run 'TestTailerFirstRunAlignsToLineBoundary|TestTailerShortFirstLine' -v`
Expected: `TestTailerFirstRunAlignsToLineBoundary` FAILs — the tailer starts mid-line and emits `partial-start-end`. `TestTailerShortFirstLine` may already pass; keep it as a pin.

- [ ] **Step 3: Write minimal implementation**

In `poll`, replace `st.offset = fi.Size()` with an aligned offset and refresh the fingerprint as files grow. Add a helper:

```go
// alignToLineEnd returns the offset just after the last newline at or before
// size. A file with no newline aligns to 0, so the first whole line is kept.
func alignToLineEnd(f *os.File, size int64) int64 {
	const chunk = 64 * 1024
	pos := size
	buf := make([]byte, chunk)
	for pos > 0 {
		start := pos - chunk
		if start < 0 {
			start = 0
		}
		n, err := f.ReadAt(buf[:pos-start], start)
		if n > 0 {
			for i := n - 1; i >= 0; i-- {
				if buf[i] == '\n' {
					return start + int64(i) + 1
				}
			}
		}
		if err != nil {
			break
		}
		pos = start
	}
	return 0
}
```

Change the first-run branch:

```go
		if !t.matchSaved(st) && initial && t.firstRun {
			st.offset = alignToLineEnd(f, fi.Size())
		}
```

Also, in `read`, refill the fingerprint once the file grows past the current fingerprint length. Add after the inner emit loop, inside the `if n > 0` block:

```go
			if len(s.fingerprint) < fingerprintSize {
				if fp, err := fingerprint(s.f); err == nil && len(fp) > len(s.fingerprint) {
					s.fingerprint = fp
				}
			}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/tailer/ -run 'TestTailerFirstRunAlignsToLineBoundary|TestTailerShortFirstLine' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tailer/tailer.go internal/tailer/tailer_test.go
git commit -s -m "feat(tailer): align first-run offset to a line boundary"
```

---

## Task 7: Restart resumes from the saved offset

**Files:**
- Modify: `internal/tailer/tailer_test.go`

**Interfaces:**
- Consumes: `matchSaved`, `save`, `loadCheckpoints` from Tasks 3 and 4.

The fingerprint match and offset save from Task 4 drive this. Add the test. Fix only if it fails.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test -race ./internal/tailer/ -run TestTailerRestartResumesFromSavedOffset -v`
Expected: PASS with the Task 4 and Task 6 code. If it FAILs, debug `save`/`matchSaved`. Watch the result before moving on.

- [ ] **Step 3: Commit**

```bash
git add internal/tailer/tailer_test.go
git commit -s -m "test(tailer): pin restart-resume behavior"
```

---

## Task 8: Rename rotation and drain of a renamed file

**Files:**
- Modify: `internal/tailer/tailer.go`
- Modify: `internal/tailer/tailer_test.go`

**Interfaces:**
- Consumes: `poll`, `fileState` from Task 4.
- Produces: pruning of files that are gone. The poll reads every tracked file, then removes a tracked file from the map once it is absent from the glob and fully drained.

Task 4 never prunes, so a renamed file stays open forever and a removed file is never closed. This task adds the "present" set and the drain-then-close step. Because tracking is by inode and the descriptor stays open, a renamed file is still read to EOF through its old descriptor.

- [ ] **Step 1: Write the failing test**

```go
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
```

Note: the glob is `*.log`, so `0.log.<ts>` does NOT match. The renamed file stays tracked only through its open inode descriptor. This is the intended production behavior (the kubelet keeps the newest rotated file uncompressed, and the tailer drains it through the open fd).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/tailer/ -run TestTailerRenameRotationNoLoss -v`
Expected: Likely PASS for line delivery, because the inode descriptor still reads the renamed file, and the new `0.log` has a new inode and is read from 0 on first sight. If it FAILs with a duplicate or a loss, continue to Step 3. Either way, add the prune logic in Step 3 so the renamed file is eventually closed.

- [ ] **Step 3: Write minimal implementation**

In `poll`, build a present set and prune drained, absent files. Replace the read-and-save tail of `poll` with:

```go
	present := map[uint64]bool{}
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		present[inode(fi)] = true
	}
	for _, s := range t.files {
		t.read(s)
	}
	for ino, s := range t.files {
		if present[ino] {
			continue
		}
		// A renamed or removed file is absent from the live glob. The final read
		// above drained it. Close the descriptor and drop it.
		if err := s.f.Close(); err != nil {
			t.cfg.Logger.Error("Could not close file", "path", s.path, "err", err)
		}
		delete(t.files, ino)
	}
	if err := t.save(); err != nil {
		t.cfg.Logger.Error("Could not save checkpoints", "err", err)
	}
```

Move the `present` build to the top loop where files are opened, so the stat runs once. (Combine with the existing open loop: record `present[ino] = true` there.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/tailer/ -run TestTailerRenameRotationNoLoss -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tailer/tailer.go internal/tailer/tailer_test.go
git commit -s -m "feat(tailer): prune drained files after rotation"
```

---

## Task 9: copytruncate and truncate-regrow

**Files:**
- Modify: `internal/tailer/tailer.go`
- Modify: `internal/tailer/tailer_test.go`

**Interfaces:**
- Consumes: `read`, `fileState`, `fingerprint` from Task 4.
- Produces: truncation handling in `read`. When the file size drops below the offset, or the fingerprint at offset 0 no longer matches, restart at offset 0 and refresh the fingerprint.

- [ ] **Step 1: Write the failing test**

```go
func TestTailerCopyTruncate(t *testing.T) {
	cases := []struct {
		name    string
		regrow  bool
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/tailer/ -run TestTailerCopyTruncate -v`
Expected: the `regrow` case FAILs — the size is larger than the offset, so the old offset is kept and the new head is skipped. The zero case may pass or fail depending on timing.

- [ ] **Step 3: Write minimal implementation**

At the top of `read`, before the read loop:

```go
	fi, err := s.f.Stat()
	if err != nil {
		t.cfg.Logger.Warn("Could not stat file", "path", s.path, "err", err)
		return
	}
	if fi.Size() < s.offset {
		t.resetTruncated(s)
	} else if cur, err := fingerprint(s.f); err == nil && !headMatches(cur, s.fingerprint) {
		// The file was truncated and regrew past the old offset between polls.
		t.resetTruncated(s)
	}
```

Add helpers:

```go
// resetTruncated restarts a file at offset 0 and refreshes its fingerprint.
func (t *Tailer) resetTruncated(s *fileState) {
	s.offset = 0
	s.partial = nil
	s.joiner = criJoiner{}
	if fp, err := fingerprint(s.f); err == nil {
		s.fingerprint = fp
	}
}

// headMatches reports whether cur starts with the saved fingerprint prefix.
func headMatches(cur, saved []byte) bool {
	n := len(saved)
	if n == 0 || len(cur) < n {
		return false
	}
	return bytes.Equal(cur[:n], saved)
}
```

Guard the regrow check so it does not run for a brand-new file whose fingerprint equals its head (the normal case). `headMatches` returns true for a normal file, so no reset runs. Confirm the normal append tests still pass.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/tailer/ -run 'TestTailerCopyTruncate|TestTailerStartsAtEnd|TestTailerRenameRotationNoLoss' -v`
Expected: PASS for all three.

- [ ] **Step 5: Commit**

```bash
git add internal/tailer/tailer.go internal/tailer/tailer_test.go
git commit -s -m "feat(tailer): handle copytruncate and truncate-regrow"
```

---

## Task 10: Restart after copytruncate

**Files:**
- Modify: `internal/tailer/tailer_test.go`

**Interfaces:**
- Consumes: Task 9 reset, Task 7 resume.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test -race ./internal/tailer/ -run TestTailerRestartAfterCopyTruncate -v`
Expected: PASS. If it FAILs, the fingerprint after truncate was not refreshed on save; confirm `resetTruncated` updates `s.fingerprint` and `save` writes it.

- [ ] **Step 3: Commit**

```bash
git add internal/tailer/tailer_test.go
git commit -s -m "test(tailer): pin restart-after-copytruncate"
```

---

## Task 11: Container restart and pod deletion

**Files:**
- Modify: `internal/tailer/tailer_test.go`

**Interfaces:**
- Consumes: inode tracking and prune from Tasks 4 and 8.

A container restart creates a new numbered file with a new inode; it is read from offset 0 (not first run, so the first-run branch does not apply, and a new file defaults to offset 0). A pod deletion removes the open file; the final read drains it through the descriptor, then the prune closes it.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test -race ./internal/tailer/ -run 'TestTailerContainerRestartNoLoss|TestTailerPodDeletionDrainsOpenFile' -v`
Expected: PASS. The pod-deletion case relies on the final read in `poll` running before the prune. Confirm `poll` reads every tracked file before it checks `present`. If a line is lost, reorder so the read runs first. Watch it fail/pass before moving on.

- [ ] **Step 3: Commit**

```bash
git add internal/tailer/tailer_test.go
git commit -s -m "test(tailer): pin container-restart and pod-deletion drains"
```

---

## Task 12: Missing-file grace and inode reuse

**Files:**
- Modify: `internal/tailer/tailer.go`
- Modify: `internal/tailer/tailer_test.go`

**Interfaces:**
- Consumes: `fileState.missing`, prune loop from Task 8.
- Produces: a grace period. A file absent from the glob keeps its state for `GracePolls` polls before it is closed and pruned. This covers the brief rename window when neither the old nor the new name matches `*.log`. Inode reuse is safe because a reused inode only appears after the old state is pruned; a fresh file at a new path with a reused inode is opened as new with offset 0.

Note on the rename window: with a `*.log` glob, the renamed `0.log.<ts>` never matches again, and the file is drained through the open descriptor regardless of the grace period. The grace period matters for a transient stat failure or a file that momentarily leaves the glob. The inode-reuse test proves a pruned-and-recreated path reads from 0.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/tailer/ -run 'TestTailerKeepsStateDuringBriefAbsence|TestTailerInodeReuseReadsFromStart' -v`
Expected: `TestTailerKeepsStateDuringBriefAbsence` FAILs — the file is pruned on the first absent poll, so the re-added file is treated as new and the offset resets, which replays lines.

- [ ] **Step 3: Write minimal implementation**

Change the prune loop so a file waits `GracePolls` polls while absent, and reads once more when it reappears. Replace the prune loop body:

```go
	for ino, s := range t.files {
		if present[ino] {
			s.missing = 0
			continue
		}
		t.read(s) // final drain through the open descriptor
		s.missing++
		if s.missing >= t.cfg.GracePolls {
			if err := s.f.Close(); err != nil {
				t.cfg.Logger.Error("Could not close file", "path", s.path, "err", err)
			}
			delete(t.files, ino)
		}
	}
```

Keep the earlier `for _, s := range t.files { t.read(s) }` for present files, or fold both reads together. Ensure a present file is read once per poll and an absent file is read once per poll until pruned.

Because a new path with a reused inode only appears after the old state is pruned, no extra inode logic is needed: the open loop opens it fresh with offset 0.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/tailer/ -run 'TestTailerKeepsStateDuringBriefAbsence|TestTailerInodeReuseReadsFromStart|TestTailerPodDeletionDrainsOpenFile' -v`
Expected: PASS for all three.

- [ ] **Step 5: Commit**

```bash
git add internal/tailer/tailer.go internal/tailer/tailer_test.go
git commit -s -m "feat(tailer): add missing-file grace and inode-reuse safety"
```

---

## Task 13: Glob ignores .gz and .tmp; lag across rotations

**Files:**
- Modify: `internal/tailer/tailer_test.go`

**Interfaces:**
- Consumes: the `*.log` glob. The glob already excludes `.gz` and `.tmp` by shape.

The `*.log` glob never matches `0.log.<ts>.gz` or `0.log.<ts>.tmp`. This task pins that, and pins that a pre-existing compressed file is never read. Lag across rotations (a line in a file the kubelet already compressed) is a known, accepted loss per the RFC; the test asserts the tailer does not crash and keeps serving the live file.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test -race ./internal/tailer/ -run TestTailerIgnoresCompressedAndTempFiles -v`
Expected: PASS. If a `.gz` or `.tmp` line appears, the production glob is wrong; confirm callers pass a `*.log` glob and the code does not widen it.

- [ ] **Step 3: Commit**

```bash
git add internal/tailer/tailer_test.go
git commit -s -m "test(tailer): pin that .gz and .tmp files are ignored"
```

---

## Task 14: Maximum line size

**Files:**
- Modify: `internal/tailer/tailer.go`
- Modify: `internal/tailer/tailer_test.go`

**Interfaces:**
- Consumes: `read`, `Config.MaxLineBytes`.
- Produces: a size guard. A partial buffer that grows past `MaxLineBytes` with no newline is dropped up to the next newline, logged and counted. A single line longer than the limit is not emitted.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/tailer/ -run TestTailerDropsOversizeLine -v`
Expected: FAIL — the oversize line is emitted (its text is ~460 bytes).

- [ ] **Step 3: Write minimal implementation**

In `read`, after appending to `s.partial` and before the newline loop, drop an oversize partial head:

```go
			if len(s.partial) > t.cfg.MaxLineBytes {
				if i := bytes.IndexByte(s.partial, '\n'); i < 0 {
					// No newline yet. Drop the head but keep the last MaxLineBytes
					// so a newline that arrives soon still frames a bounded line.
					drop := len(s.partial) - t.cfg.MaxLineBytes
					s.partial = s.partial[drop:]
					t.cfg.Logger.Warn("Dropped oversize log head", "path", s.path, "bytes", drop)
				}
			}
```

Also enforce the limit per framed line in the newline loop: before `t.emit`, if `i > t.cfg.MaxLineBytes`, log and skip instead of emit:

```go
				if i > t.cfg.MaxLineBytes {
					t.cfg.Logger.Warn("Dropped oversize log line", "path", s.path, "bytes", i)
				} else {
					t.emit(s, s.partial[:i], readAt)
				}
				s.partial = s.partial[i+1:]
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/tailer/ -run TestTailerDropsOversizeLine -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tailer/tailer.go internal/tailer/tailer_test.go
git commit -s -m "feat(tailer): enforce a maximum line size"
```

---

## Task 15: Per-file error isolation

**Files:**
- Modify: `internal/tailer/tailer_test.go`

**Interfaces:**
- Consumes: the open loop and `read`.

An unreadable file (bad permissions) must not stop the other files. The open loop already `continue`s on an open error, and `read` already returns on a stat error. Add the test to pin it.

- [ ] **Step 1: Write the failing test**

```go
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
```

Note: this test guards against a regression where an unreadable file aborts the poll. It skips as root because root bypasses file permissions. This is a real assertion, not a stub: it fails if the open error ever propagates out of the poll loop.

- [ ] **Step 2: Run test to verify it passes**

Run: `go test -race ./internal/tailer/ -run TestTailerIsolatesPerFileErrors -v`
Expected: PASS (skips as root). If it FAILs as non-root, the open error aborts the poll; confirm `poll` uses `continue` on each per-file error.

- [ ] **Step 3: Commit**

```bash
git add internal/tailer/tailer_test.go
git commit -s -m "test(tailer): pin per-file error isolation"
```

---

## Task 16: Stop saves checkpoint and closes descriptors

**Files:**
- Modify: `internal/tailer/tailer_test.go`

**Interfaces:**
- Consumes: `Stop`, `shutdown`, `save` from Task 4.

- [ ] **Step 1: Write the failing test**

```go
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
	require.Greater(t, recs[0].Offset, int64(0))

	// A second Stop is safe.
	require.NoError(t, tl.Stop())
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test -race ./internal/tailer/ -run TestTailerStopSavesAndCloses -v`
Expected: PASS. The double-Stop guard in Task 4 (`select` on the closed channel) makes the second call safe. If it panics on a double close, confirm the guard is present.

- [ ] **Step 3: Commit**

```bash
git add internal/tailer/tailer_test.go
git commit -s -m "test(tailer): pin stop saves checkpoint and closes files"
```

---

## Task 17: Rotation under load

**Files:**
- Modify: `internal/tailer/tailer_test.go`

**Interfaces:**
- Consumes: the whole tailer.

A stress test: a writer appends at a steady rate while the file rotates several times. No line is lost or duplicated.

- [ ] **Step 1: Write the failing test**

```go
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
		end := next + 50
		if end > total {
			end = total
		}
		appendLines(t, log, next, end)
		next = end
		if next%rotateEvery == 0 {
			ts := time.Now().UnixNano()
			require.NoError(t, os.Rename(log, filepath.Join(dir, "0.log."+time.Unix(0, ts).Format("20060102-150405.000000000"))))
		}
		time.Sleep(10 * time.Millisecond)
	}

	waitForSet(c, wantTexts(1, total), 8*time.Second)
	missing, dup := missingAndDup(c.snapshot(), wantTexts(1, total))
	require.Equal(t, 0, missing)
	require.Equal(t, 0, dup)
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test -race ./internal/tailer/ -run TestTailerRotationUnderLoad -v`
Expected: PASS. If a duplicate appears at a rotation boundary, the new `0.log` with a reused-by-the-fs inode could clash with a not-yet-pruned old inode. Because the old file is drained and pruned when absent, and the new file has a distinct inode while the old fd is open, this should hold. If it FAILs, inspect the inode handling at the rotation moment.

- [ ] **Step 3: Commit**

```bash
git add internal/tailer/tailer_test.go
git commit -s -m "test(tailer): add rotation-under-load stress test"
```

---

## Task 18: Checkpoint retention for deleted pods

**Files:**
- Modify: `internal/tailer/tailer.go`
- Modify: `internal/tailer/checkpoint.go`
- Modify: `internal/tailer/tailer_test.go`

**Interfaces:**
- Consumes: `save`, `loadCheckpoints`, `fileState`.
- Produces: on-disk pruning. A checkpoint for a file that is no longer tracked is kept for `Retention`, then dropped from the saved set, so the offset file does not grow without bound as pods come and go.

Design: record a `LastSeen` time per saved record. On `save`, write the live files' current records plus any loaded records younger than `Retention` whose fingerprint is not among the live files. Extend `savedCheckpoint` with a `LastSeen time.Time json:"lastSeen"` field.

- [ ] **Step 1: Write the failing test**

```go
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
	cfg := Config{Glob: glob(dir), CheckpointPath: state, PollInterval: 50 * time.Millisecond, Retention: 10 * time.Minute}
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/tailer/ -run TestTailerPrunesOldCheckpoints -v`
Expected: FAIL — `LastSeen` is not a field yet, so the test does not compile.

- [ ] **Step 3: Write minimal implementation**

Add the field in `checkpoint.go`:

```go
type savedCheckpoint struct {
	Fingerprint []byte    `json:"fingerprint"`
	Offset      int64     `json:"offset"`
	LastSeen    time.Time `json:"lastSeen"`
}
```

(Add `import "time"` to `checkpoint.go`.)

Rewrite `save` in `tailer.go` to merge live records with retained old records:

```go
func (t *Tailer) save() error {
	now := time.Now()
	live := map[string]bool{}
	out := make([]savedCheckpoint, 0, len(t.files))
	for _, s := range t.files {
		out = append(out, savedCheckpoint{
			Fingerprint: s.fingerprint,
			Offset:      s.offset - int64(len(s.partial)),
			LastSeen:    now,
		})
		live[string(s.fingerprint)] = true
	}
	for _, sv := range t.saved {
		if live[string(sv.Fingerprint)] {
			continue
		}
		if !sv.LastSeen.IsZero() && now.Sub(sv.LastSeen) > t.cfg.Retention {
			continue
		}
		out = append(out, sv)
	}
	if err := saveCheckpoints(t.cfg.CheckpointPath, out); err != nil {
		return err
	}
	t.saved = out
	return nil
}
```

This keeps a not-yet-matched saved record (for a file that has not appeared yet) until it ages out, and refreshes `t.saved` so later polls see the merged set. A record with a zero `LastSeen` (from an older on-disk format) is kept, so an upgrade does not drop offsets.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/tailer/ -run 'TestTailerPrunesOldCheckpoints|TestTailerRestartResumesFromSavedOffset|TestTailerRestartAfterCopyTruncate' -v`
Expected: PASS for all three. The resume tests confirm the merge did not break fingerprint matching.

- [ ] **Step 5: Commit**

```bash
git add internal/tailer/tailer.go internal/tailer/checkpoint.go internal/tailer/tailer_test.go
git commit -s -m "feat(tailer): retain and prune checkpoints by age"
```

---

## Task 19: Package documentation, full suite, lint, and RFC status

**Files:**
- Modify: `internal/tailer/tailer.go` (package doc comment, if not already complete)
- Modify: `docs/rfc/0006-file-tailer.md` (mark the production-scope items done)

**Interfaces:** none new.

- [ ] **Step 1: Run the full package suite with the race detector**

Run: `go test -race ./internal/tailer/... -v`
Expected: all tests PASS. Fix any flaky timing by widening a `waitForSet` deadline, not by weakening an assertion.

- [ ] **Step 2: Run the project build and the full test suite**

Run: `make test`
Expected: PASS. Report any failure by name, including a failure not caused by this change.

- [ ] **Step 3: Run the linter**

Run: `make lint-fix`
Expected: no outstanding issues. Do not add `//nolint`. Fix the underlying issue.

- [ ] **Step 4: Update the RFC production-scope status**

In `docs/rfc/0006-file-tailer.md`, under "Production scope of the custom tailer", note that the implementation covers the listed items, with a short line per item or a single summary sentence that points to `internal/tailer`. Keep the prose in Simplified Technical English. Do not change the decision or the results sections.

- [ ] **Step 5: Commit**

```bash
git add internal/tailer docs/rfc/0006-file-tailer.md
git commit -s -m "docs(tailer): update RFC production-scope status"
```

---

## Self-review notes

- **Spec coverage:** every RFC production-scope item maps to a task via the spec's coverage table. CRI `P` join → Task 2; max line → Task 14; atomic checkpoint and corrupt recovery → Task 3; first-run alignment → Task 6; truncate-regrow → Task 9; deleted-pod checkpoint pruning → Task 18; live-log-only glob → Task 13; drain removed/renamed files → Tasks 8, 11; new container instance from start → Task 11; inode-reuse grace → Task 12; short first line → Task 6; per-file error isolation → Task 15; close read rotated files → Tasks 8, 12.
- **Review Focus:** each of the five lines maps to a task test (1, 5, 12, 3, 6).
- **Type consistency:** `savedCheckpoint` gains `LastSeen` in Task 18; earlier tasks use only `Fingerprint` and `Offset`, which still compile. `Config`, `Line`, `fileState`, `parseCRI`, `criJoiner` keep the same signatures throughout.
- **Known timing risk:** the tests use real time and polling. Deadlines are generous (3–8 s) relative to the 30–50 ms poll in tests. If CI is slow, widen deadlines rather than reduce coverage.
