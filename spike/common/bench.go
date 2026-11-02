package common

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Result holds one benchmark run.
type Result struct {
	Rate       int
	Duration   time.Duration
	CPUPercent float64 // of one core
	RSSMiB     float64
	Emitted    int64
	Written    int64
	LatP50     time.Duration // end to end: detection plus delivery
	LatP99     time.Duration
	// Split is true when the tailer reports its read time, so the run can
	// separate detection from delivery. It is false for black-box options.
	Split  bool
	DetP50 time.Duration // write to read: poll or event wait plus the read
	DetP99 time.Duration
	DelP50 time.Duration // read to emit: parse, buffer, batch or flush
	DelP99 time.Duration
}

func cpuTime() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		panic("getrusage: " + err.Error())
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func rssMiB() float64 {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	var kb float64
	for _, l := range splitLines(string(raw)) {
		if _, err := fmt.Sscanf(l, "VmRSS: %f kB", &kb); err == nil {
			return kb / 1024
		}
	}
	return 0
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}

// Bench writes CRI lines at rate lines/s for d and measures the tailer in this process.
// Rate 0 measures idle CPU. Each line holds its write time, so Bench also measures latency.
func Bench(f Factory, rate int, d time.Duration) (Result, error) {
	return BenchWith(f, rate, d, nil)
}

// Probe returns the total CPU time and the memory (MiB) of the tailer under test.
// A nil probe measures the current process.
type Probe func() (time.Duration, float64)

// BenchWith is Bench with a custom probe, for tailers that run in another process.
func BenchWith(f Factory, rate int, d time.Duration, probe Probe) (Result, error) {
	if probe == nil {
		probe = func() (time.Duration, float64) { return cpuTime(), rssMiB() }
	}
	dir, err := os.MkdirTemp("", "tailbench")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	log := filepath.Join(dir, "0.log")
	if err := os.WriteFile(log, []byte(CRILine(0)+"\n"), 0o600); err != nil {
		return Result{}, err
	}

	var emitted atomic.Int64
	var mu sync.Mutex
	var e2e, det, del []time.Duration
	anySplit := false
	// The callback stays cheap, so the measured CPU reflects the tailer, not the
	// harness. It parses the embedded write time by hand and records the latency.
	// readAt is the time the tailer read the bytes. When it is set, the callback
	// also records detection (write to read) and delivery (read to emit).
	stop, err := f(filepath.Join(dir, "*.log*"), "", func(b []byte, readAt time.Time) {
		now := time.Now()
		emitted.Add(1)
		if ns, ok := embeddedNanos(b); ok {
			wrote := time.Unix(0, ns)
			mu.Lock()
			e2e = append(e2e, now.Sub(wrote))
			if !readAt.IsZero() {
				anySplit = true
				det = append(det, clampNonNeg(readAt.Sub(wrote)))
				del = append(del, clampNonNeg(now.Sub(readAt)))
			}
			mu.Unlock()
		}
	})
	if err != nil {
		return Result{}, err
	}
	defer stop()
	time.Sleep(time.Second)

	runtime.GC()
	cpu0, _ := probe()
	wall0 := time.Now()
	var written int64
	if rate > 0 {
		fh, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return Result{}, err
		}
		defer fh.Close()
		tick := 10 * time.Millisecond
		perTick := rate / int(time.Second/tick)
		t := time.NewTicker(tick)
		defer t.Stop()
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			<-t.C
			var buf []byte
			for i := 0; i < perTick; i++ {
				buf = append(
					buf,
					fmt.Sprintf(
						"2026-10-29T13:00:00.000000000Z stdout F {\"ts\":%d,\"n\":%d,\"msg\":\"[2026-10-29T13:00:00.000Z] \\\"GET /x HTTP/1.1\\\" 200 - via_upstream\"}\n",
						time.Now().UnixNano(),
						written,
					)...)
				written++
			}
			if _, err := fh.Write(buf); err != nil {
				return Result{}, err
			}
		}
	} else {
		time.Sleep(d)
	}
	// Drain first, so a tailer that batches or lags is charged for the CPU it
	// spends to catch up. Then sample CPU, wall time and memory.
	for i := 0; i < 100 && emitted.Load() < written; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	cpu1, mem1 := probe()
	wall1 := time.Now()

	res := Result{Rate: rate, Duration: wall1.Sub(wall0), Emitted: emitted.Load(), Written: written, RSSMiB: mem1}
	// The generator uses some CPU too; it is cheap, so it stays in the total as an upper bound.
	res.CPUPercent = float64(cpu1-cpu0) / float64(wall1.Sub(wall0)) * 100
	mu.Lock()
	defer mu.Unlock()
	res.LatP50, res.LatP99 = percentiles(e2e)
	res.Split = anySplit
	if anySplit {
		res.DetP50, res.DetP99 = percentiles(det)
		res.DelP50, res.DelP99 = percentiles(del)
	}
	return res, nil
}

// percentiles returns the p50 and p99 of d. It returns zero for an empty input.
func percentiles(d []time.Duration) (p50, p99 time.Duration) {
	if len(d) == 0 {
		return 0, 0
	}
	sorted := append([]time.Duration(nil), d...)
	sortDur(sorted)
	return sorted[len(sorted)/2], sorted[len(sorted)*99/100]
}

func clampNonNeg(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

// embeddedNanos reads the "ts":<nanos> field that the generator writes, without
// allocating. It returns false when the field is absent, as in idle CRI lines.
func embeddedNanos(b []byte) (int64, bool) {
	key := []byte("\"ts\":")
	i := -1
	for j := 0; j+len(key) <= len(b); j++ {
		if string(b[j:j+len(key)]) == string(key) {
			i = j + len(key)
			break
		}
	}
	if i < 0 {
		return 0, false
	}
	var ns int64
	got := false
	for ; i < len(b) && b[i] >= '0' && b[i] <= '9'; i++ {
		ns = ns*10 + int64(b[i]-'0')
		got = true
	}
	return ns, got
}

func sortDur(d []time.Duration) {
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
}
