package tailer

import (
	"fmt"
	"os"
	"path/filepath"
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
		_, writeErr := f.WriteString(criLine(i) + "\n")
		require.NoError(t, writeErr)
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
	mu  sync.Mutex
	got []string
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
func missingAndDup(got, want []string) (int, int) {
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	missing := 0
	for _, w := range want {
		if seen[w] == 0 {
			missing++
		}
	}
	dup := 0
	for k, c := range seen {
		if c > 1 {
			dup += c - 1
		}
		if !wantSet[k] {
			dup += c
		}
	}
	return missing, dup
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

// glob returns the live-log glob for a directory.
func glob(dir string) string {
	return filepath.Join(dir, "*.log")
}
