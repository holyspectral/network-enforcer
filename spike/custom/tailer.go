package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const fingerprintSize = 256

type fileState struct {
	Fingerprint []byte `json:"fingerprint"`
	Offset      int64  `json:"offset"`

	f       *os.File
	path    string
	partial []byte
}

type tailer struct {
	glob      string
	statePath string
	onLine    func([]byte, time.Time)

	mu       sync.Mutex
	files    map[uint64]*fileState // key: inode
	saved    []fileState           // checkpoints loaded at start
	firstRun bool
	stop     chan struct{}
	done     chan struct{}
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

func startTailer(glob, statePath string, poll time.Duration, onLine func([]byte, time.Time)) (*tailer, error) {
	t := &tailer{
		glob:      glob,
		statePath: statePath,
		onLine:    onLine,
		files:     map[uint64]*fileState{},
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		firstRun:  true,
	}
	if statePath != "" {
		if raw, err := os.ReadFile(statePath); err == nil {
			if err := json.Unmarshal(raw, &t.saved); err != nil {
				return nil, err
			}
			t.firstRun = false
		}
	}
	t.poll(true)
	go func() {
		defer close(t.done)
		tick := time.NewTicker(poll)
		defer tick.Stop()
		for {
			select {
			case <-t.stop:
				t.mu.Lock()
				if err := t.save(); err != nil {
					fmt.Fprintln(os.Stderr, "save:", err)
				}
				for _, s := range t.files {
					if err := s.f.Close(); err != nil {
						fmt.Fprintln(os.Stderr, "close:", err)
					}
				}
				t.mu.Unlock()
				return
			case <-tick.C:
				t.poll(false)
			}
		}
	}()
	return t, nil
}

func (t *tailer) Stop() {
	close(t.stop)
	<-t.done
}

// poll opens new files, reads every tracked file to EOF, prunes files that are
// gone, then saves offsets.
func (t *tailer) poll(initial bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	paths, err := filepath.Glob(t.glob)
	if err != nil {
		fmt.Fprintln(os.Stderr, "glob:", err)
		return
	}
	present := map[uint64]bool{}
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		ino := inode(fi)
		present[ino] = true
		if _, ok := t.files[ino]; ok {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		fp, err := fingerprint(f)
		if err != nil || len(fp) == 0 {
			if cerr := f.Close(); cerr != nil {
				fmt.Fprintln(os.Stderr, "close:", cerr)
			}
			continue
		}
		st := &fileState{Fingerprint: fp, f: f, path: p}
		if !t.matchSaved(st) && initial && t.firstRun {
			st.Offset = fi.Size()
		}
		t.files[ino] = st
	}
	for _, s := range t.files {
		t.read(s)
	}
	// Prune files that no longer exist. A final read already ran above. Closing
	// the descriptor frees the inode, so a later reuse is treated as a new file.
	for ino, s := range t.files {
		if !present[ino] {
			if err := s.f.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "close:", err)
			}
			delete(t.files, ino)
		}
	}
	if err := t.save(); err != nil {
		fmt.Fprintln(os.Stderr, "save:", err)
	}
}

func (t *tailer) matchSaved(st *fileState) bool {
	for _, sv := range t.saved {
		n := len(sv.Fingerprint)
		if n > 0 && len(st.Fingerprint) >= n && bytes.Equal(st.Fingerprint[:n], sv.Fingerprint) {
			st.Offset = sv.Offset
			return true
		}
	}
	return false
}

func (t *tailer) read(s *fileState) {
	fi, err := s.f.Stat()
	if err != nil {
		return
	}
	if fi.Size() < s.Offset {
		// Truncated in place. Read the new content from the start and refresh the
		// fingerprint, so a later restart matches the new content, not the old.
		s.Offset = 0
		s.partial = nil
		if fp, err := fingerprint(s.f); err == nil {
			s.Fingerprint = fp
		}
	}
	buf := make([]byte, 64*1024)
	for {
		n, err := s.f.ReadAt(buf, s.Offset)
		if n > 0 {
			// readAt marks when the OS returned the bytes. Every line in this read
			// shares it, so detection covers the poll wait plus the read.
			readAt := time.Now()
			s.Offset += int64(n)
			s.partial = append(s.partial, buf[:n]...)
			for {
				i := bytes.IndexByte(s.partial, '\n')
				if i < 0 {
					break
				}
				t.onLine(s.partial[:i], readAt)
				s.partial = s.partial[i+1:]
			}
			// The fingerprint is set once the file has enough bytes.
			if len(s.Fingerprint) < fingerprintSize {
				if fp, err := fingerprint(s.f); err == nil && len(fp) > len(s.Fingerprint) {
					s.Fingerprint = fp
				}
			}
		}
		if err != nil {
			break
		}
	}
}

// save writes the offset of the last complete line, so a restart re-reads the partial tail.
func (t *tailer) save() error {
	if t.statePath == "" {
		return nil
	}
	out := make([]fileState, 0, len(t.files))
	for _, s := range t.files {
		out = append(out, fileState{Fingerprint: s.Fingerprint, Offset: s.Offset - int64(len(s.partial))})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	tmp := t.statePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, t.statePath)
}
