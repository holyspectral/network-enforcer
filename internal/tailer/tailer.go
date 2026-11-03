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

const (
	defaultPollInterval = 200 * time.Millisecond
	defaultMaxLineBytes = 1 << 20
	defaultGracePolls   = 2
	defaultRetention    = 10 * time.Minute
	readBufSize         = 64 * 1024
)

func (c *Config) applyDefaults() {
	if c.PollInterval == 0 {
		c.PollInterval = defaultPollInterval
	}
	if c.MaxLineBytes == 0 {
		c.MaxLineBytes = defaultMaxLineBytes
	}
	if c.GracePolls == 0 {
		c.GracePolls = defaultGracePolls
	}
	if c.Retention == 0 {
		c.Retention = defaultRetention
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
}

// Tailer reads CRI log files and emits complete lines.
type Tailer struct {
	cfg      Config
	handler  Handler
	saved    []savedCheckpoint
	firstRun bool

	mu      sync.Mutex
	started bool
	files   map[uint64]*fileState

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
// context ends. Calling Start multiple times is a no-op after the first call.
func (t *Tailer) Start(ctx context.Context) error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return nil
	}
	t.started = true
	t.mu.Unlock()

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

// Stop ends the poll loop and waits for it. Stop is safe to call even if
// Start was never called.
func (t *Tailer) Stop() error {
	t.mu.Lock()
	started := t.started
	t.mu.Unlock()

	if !started {
		return nil
	}

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

// alignToLineEnd returns the offset just after the last newline at or before
// size. If there is incomplete data after the last newline, it returns size
// to skip that data and start fresh at EOF.
func alignToLineEnd(f *os.File, size int64) int64 {
	const chunk = 64 * 1024
	pos := size
	buf := make([]byte, chunk)
	for pos > 0 {
		start := max(pos-chunk, 0)
		n, err := f.ReadAt(buf[:pos-start], start)
		if n > 0 {
			for i := n - 1; i >= 0; i-- {
				if buf[i] == '\n' {
					afterNewline := start + int64(i) + 1
					if afterNewline < size {
						return size
					}
					return afterNewline
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

// poll opens new files, reads each tracked file to EOF, then saves offsets.
func (t *Tailer) poll(initial bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	paths, globErr := filepath.Glob(t.cfg.Glob)
	if globErr != nil {
		t.cfg.Logger.Error("Could not glob logs", "glob", t.cfg.Glob, "err", globErr)
		return
	}
	present := t.openNewFiles(paths, initial)
	for _, s := range t.files {
		t.read(s)
	}
	t.pruneAbsent(present)
	if saveErr := t.save(); saveErr != nil {
		t.cfg.Logger.Error("Could not save checkpoints", "err", saveErr)
	}
}

// openNewFiles stats every glob match, opens files not yet tracked, and
// returns the set of inodes currently present on disk.
func (t *Tailer) openNewFiles(paths []string, initial bool) map[uint64]bool {
	present := map[uint64]bool{}
	for _, p := range paths {
		fi, statErr := os.Stat(p)
		if statErr != nil {
			continue
		}
		ino := inode(fi)
		present[ino] = true
		if _, ok := t.files[ino]; ok {
			continue
		}
		t.openFile(p, fi, ino, initial)
	}
	return present
}

// openFile opens one newly seen path and adds it to the tracked files.
func (t *Tailer) openFile(p string, fi os.FileInfo, ino uint64, initial bool) {
	f, openErr := os.Open(p)
	if openErr != nil {
		return
	}
	fp, fpErr := fingerprint(f)
	if fpErr != nil || len(fp) == 0 {
		if closeErr := f.Close(); closeErr != nil {
			t.cfg.Logger.Error("Could not close file", "path", p, "err", closeErr)
		}
		return
	}
	st := &fileState{fingerprint: fp, f: f, path: p}
	if !t.matchSaved(st) && initial && t.firstRun {
		st.offset = alignToLineEnd(f, fi.Size())
	}
	t.files[ino] = st
}

// pruneAbsent closes and drops every tracked file whose inode is not in
// present. A renamed or removed file is absent from the live glob, and the
// caller has already drained it with a final read before calling this.
func (t *Tailer) pruneAbsent(present map[uint64]bool) {
	for ino, s := range t.files {
		if present[ino] {
			continue
		}
		if err := s.f.Close(); err != nil {
			t.cfg.Logger.Error("Could not close file", "path", s.path, "err", err)
		}
		delete(t.files, ino)
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
	if !t.checkTruncation(s) {
		return
	}

	buf := make([]byte, readBufSize)
	for {
		n, readErr := s.f.ReadAt(buf, s.offset)
		if n > 0 {
			t.consume(s, buf[:n])
		}
		if readErr != nil {
			break
		}
	}
}

// consume advances the offset, emits complete lines from the newly read
// chunk, and grows the fingerprint until it reaches its target size.
func (t *Tailer) consume(s *fileState, chunk []byte) {
	readAt := time.Now()
	s.offset += int64(len(chunk))
	s.partial = append(s.partial, chunk...)
	for {
		i := bytes.IndexByte(s.partial, '\n')
		if i < 0 {
			break
		}
		t.emit(s, s.partial[:i], readAt)
		s.partial = s.partial[i+1:]
	}
	if len(s.fingerprint) < fingerprintSize {
		if fp, err := fingerprint(s.f); err == nil && len(fp) > len(s.fingerprint) {
			s.fingerprint = fp
		}
	}
}

// checkTruncation detects copytruncate rotation and truncate-regrow. It
// reports false if the file cannot be stat'd, so the caller must stop reading.
func (t *Tailer) checkTruncation(s *fileState) bool {
	fi, err := s.f.Stat()
	if err != nil {
		t.cfg.Logger.Warn("Could not stat file", "path", s.path, "err", err)
		return false
	}
	if fi.Size() < s.offset {
		t.resetTruncated(s)
	} else if cur, fpErr := fingerprint(s.f); fpErr == nil && !headMatches(cur, s.fingerprint) {
		// The file was truncated and regrew past the old offset between polls.
		t.resetTruncated(s)
	}
	return true
}

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
