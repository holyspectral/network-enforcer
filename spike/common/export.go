package common

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// NewExportSink returns an Emit that appends the constant work marker "ASD" to
// each line and writes it to standard output. Every Go tailer under the uniform
// harness uses this sink, so each one does the same per-line work. The sink
// writes each record with one direct write, so it holds no timer and adds no
// idle cost. The measured idle CPU then reflects only the tailer poll or watch.
// A mutex serializes the writes, so lines from different goroutines do not mix.
func NewExportSink() (emit Emit, closeFn func()) {
	var mu sync.Mutex
	buf := make([]byte, 0, 512)
	emit = func(line []byte, _ time.Time) {
		mu.Lock()
		buf = append(buf[:0], line...)
		buf = append(buf, "ASD\n"...)
		if _, err := os.Stdout.Write(buf); err != nil {
			fmt.Fprintln(os.Stderr, "export: write:", err)
		}
		mu.Unlock()
	}
	closeFn = func() {}
	return emit, closeFn
}

// RunExport starts the tailer from factory in export mode and blocks until a
// termination signal. The glob comes from the LOG_GLOB environment variable, or
// defaults to /logs/*.log inside the container. Offsets stay in memory. The
// function prints READY on standard error once the tailer is running, so the
// driver can wait for readiness, like the external tools do.
func RunExport(factory Factory) {
	glob := os.Getenv("LOG_GLOB")
	if glob == "" {
		glob = "/logs/*.log"
	}
	emit, closeSink := NewExportSink()
	stop, err := factory(glob, "", emit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "export:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "READY")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	stop()
	closeSink()
}
