// Command gen writes containerd CRI log lines at a fixed rate.
//
// It runs outside the measured cgroup, so its cost is not charged to a tailer.
// Each line holds two plaintext markers: TS=<write unix nanoseconds> and
// N=<sequence number>. The markers use no quotes, so they survive a JSON
// envelope added by a downstream tool. The receiver reads them back.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// criLine returns one CRI format line with the TS and N markers.
// The format is "<rfc3339> <stream> <tag> <content>".
func criLine(seq int, now time.Time) string {
	return fmt.Sprintf(
		"2026-10-29T13:00:00.000000000Z stdout F TS=%d N=%d GET /x HTTP/1.1 200 - via_upstream envoy proxy access log padding",
		now.UnixNano(),
		seq,
	)
}

func main() {
	dir := flag.String("dir", "", "log directory")
	rate := flag.Int("rate", 0, "lines per second (0 means idle)")
	seconds := flag.Int("seconds", 20, "run time in seconds")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "gen: -dir is required")
		os.Exit(1)
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "gen: mkdir:", err)
		os.Exit(1)
	}
	path := filepath.Join(*dir, "0.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen: open:", err)
		os.Exit(1)
	}
	defer f.Close()

	d := time.Duration(*seconds) * time.Second
	if *rate <= 0 {
		time.Sleep(d)
		fmt.Println("written 0")
		return
	}

	tick := 10 * time.Millisecond
	perTick := *rate / int(time.Second/tick)
	if perTick < 1 {
		perTick = 1
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	end := time.Now().Add(d)
	written := 0
	for time.Now().Before(end) {
		<-t.C
		var buf []byte
		for i := 0; i < perTick; i++ {
			buf = append(buf, criLine(written, time.Now())...)
			buf = append(buf, '\n')
			written++
		}
		if _, err := f.Write(buf); err != nil {
			fmt.Fprintln(os.Stderr, "gen: write:", err)
			os.Exit(1)
		}
	}
	fmt.Printf("written %d\n", written)
}
