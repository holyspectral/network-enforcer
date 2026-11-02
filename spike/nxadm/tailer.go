package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nxadm/tail"
)

type tailer struct {
	glob, statePath string
	onLine          func([]byte, time.Time)
	mu              sync.Mutex
	tails           map[string]*tail.Tail
	saved           map[string]int64
	firstRun        bool
	stop, done      chan struct{}
}

func startTailer(glob, statePath string, onLine func([]byte, time.Time)) (*tailer, error) {
	t := &tailer{
		glob:      glob,
		statePath: statePath,
		onLine:    onLine,
		tails:     map[string]*tail.Tail{},
		saved:     map[string]int64{},
		firstRun:  true,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	if statePath != "" {
		if raw, err := os.ReadFile(statePath); err == nil {
			if err := json.Unmarshal(raw, &t.saved); err != nil {
				return nil, err
			}
			t.firstRun = false
		}
	}
	t.scan(true)
	go func() {
		defer close(t.done)
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-t.stop:
				t.mu.Lock()
				if err := t.save(); err != nil {
					fmt.Fprintln(os.Stderr, "save:", err)
				}
				for _, tl := range t.tails {
					if err := tl.Stop(); err != nil {
						fmt.Fprintln(os.Stderr, "stop:", err)
					}
					tl.Cleanup()
				}
				t.mu.Unlock()
				return
			case <-tick.C:
				t.scan(false)
			}
		}
	}()
	return t, nil
}

func (t *tailer) Stop() { close(t.stop); <-t.done }

func (t *tailer) scan(initial bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	paths, err := filepath.Glob(t.glob)
	if err != nil {
		fmt.Fprintln(os.Stderr, "glob:", err)
		return
	}
	for _, p := range paths {
		if _, ok := t.tails[p]; ok {
			continue
		}
		loc := &tail.SeekInfo{Offset: 0, Whence: io.SeekStart}
		if off, ok := t.saved[p]; ok {
			loc.Offset = off
		} else if initial && t.firstRun {
			loc.Whence = io.SeekEnd
		}
		tl, err := tail.TailFile(
			p,
			tail.Config{Follow: true, ReOpen: true, Poll: false, Location: loc, Logger: tail.DiscardingLogger},
		)
		if err != nil {
			continue
		}
		t.tails[p] = tl
		go func() {
			// l.Time is the time the library read the line. It gives the read
			// time for the detection and delivery split.
			for l := range tl.Lines {
				t.onLine([]byte(l.Text), l.Time)
			}
		}()
	}
	if err := t.save(); err != nil {
		fmt.Fprintln(os.Stderr, "save:", err)
	}
}

// scan tracks files by path. The library follows a renamed file by name
// (ReOpen), but the glob also matches the rotated name, so a renamed file can be
// tailed twice. This is a known limit of the path-based approach.
func (t *tailer) save() error {
	if t.statePath == "" {
		return nil
	}
	out := map[string]int64{}
	for p, tl := range t.tails {
		if off, err := tl.Tell(); err == nil {
			out[p] = off
		}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return os.WriteFile(t.statePath, raw, 0o600)
}
