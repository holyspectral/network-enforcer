// Command kubelet measures the kubelet log API (kubectl logs) with the same
// checks as the other spike options. It needs a kind cluster and kubectl.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const ns = "tailer-spike"

// generator writes numbered lines after a head start, so the follow
// client attaches before the first line.
const generator = `sleep 8; awk -v n=%d -v d=%s 'BEGIN{while(i<n){for(j=0;j<%d;j++){printf "%%d 2026-10-29 GET /x HTTP/1.1 200 via_upstream\n", i++; fflush()} system("sleep " d)}}'; sleep 3600`

type usage struct {
	cpu    time.Duration
	rssMiB float64
}

type line struct {
	seq     int
	ts      string
	ok      bool
	latency time.Duration
}

func kubectl(args ...string) *exec.Cmd {
	return exec.Command("kubectl", append([]string{"-n", ns}, args...)...)
}

func run(args ...string) error {
	out, err := kubectl(args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl %v: %w: %s", args, err, out)
	}
	return nil
}

func startPod(name string, lines int, delay string, batch int) error {
	script := fmt.Sprintf(generator, lines, delay, batch)
	if lines == 0 {
		script = "sleep 3600"
	}
	if err := run(
		"run",
		name,
		"--image=busybox:1.36",
		"--restart=Never",
		"--command",
		"--",
		"sh",
		"-c",
		script,
	); err != nil {
		return err
	}
	return run("wait", "--for=condition=Ready", "pod/"+name, "--timeout=120s")
}

func deletePod(name string) {
	if err := run("delete", "pod", name, "--now", "--wait=true"); err != nil {
		fmt.Fprintln(os.Stderr, "delete pod:", err)
	}
}

// parse reads "<rfc3339nano> <seq> <rest>" lines from kubectl --timestamps.
func parse(raw string, now time.Time) line {
	parts := strings.SplitN(raw, " ", 3)
	if len(parts) < 3 || !strings.HasSuffix(raw, "via_upstream") {
		return line{}
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return line{}
	}
	seq, err := strconv.Atoi(parts[1])
	if err != nil {
		return line{}
	}
	return line{seq: seq, ts: parts[0], ok: true, latency: now.Sub(ts)}
}

// follow streams the log for d, or until want lines arrive.
func follow(pod string, d time.Duration, want int, extra ...string) ([]line, []string, usage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	args := append([]string{"-n", ns, "logs", "-f", "--timestamps", pod}, extra...)
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGINT) }
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, usage{}, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, usage{}, err
	}
	var lines []line
	var bad []string
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		l := parse(sc.Text(), time.Now())
		if !l.ok {
			bad = append(bad, sc.Text())
			continue
		}
		lines = append(lines, l)
		if want > 0 && len(lines) >= want {
			cancel()
			break
		}
	}
	if _, err := io.Copy(io.Discard, out); err != nil {
		fmt.Fprintln(os.Stderr, "drain:", err)
	}
	if err := cmd.Wait(); err != nil {
		var ee *exec.ExitError
		if !errors.Is(err, context.DeadlineExceeded) && !errors.As(err, &ee) {
			return lines, bad, usage{}, err
		}
	}
	u := usage{}
	if ps := cmd.ProcessState; ps != nil {
		u.cpu = ps.UserTime() + ps.SystemTime()
		if ru, ok := ps.SysUsage().(*syscall.Rusage); ok {
			u.rssMiB = float64(ru.Maxrss) / 1024
		}
	}
	return lines, bad, u, nil
}

func gaps(lines []line) (missing, dup int) {
	for i := 1; i < len(lines); i++ {
		switch d := lines[i].seq - lines[i-1].seq; {
		case d > 1:
			missing += d - 1
		case d <= 0:
			dup++
		}
	}
	return missing, dup
}

func pct(lines []line, p float64) time.Duration {
	d := make([]time.Duration, len(lines))
	for i, l := range lines {
		d[i] = l.latency
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[int(float64(len(d)-1)*p)]
}

func logFiles(pod string) string {
	node := "kind-control-plane"
	out, err := kubectl("get", "pod", pod, "-o", "jsonpath={.metadata.uid}").Output()
	if err != nil {
		return "unknown"
	}
	ls, err := exec.Command("docker", "exec", node, "sh", "-c",
		"ls /var/log/pods/"+ns+"_"+pod+"_"+string(out)+"/*/").Output()
	if err != nil {
		return "unknown"
	}
	return strings.Join(strings.Fields(string(ls)), " ")
}

func idle() error {
	if err := startPod("idle", 0, "", 0); err != nil {
		return err
	}
	defer deletePod("idle")
	_, _, u, err := follow("idle", 20*time.Second, 0, "--tail=0")
	if err != nil {
		return err
	}
	fmt.Printf("idle: cpu=%.2f%% rss=%.1f MiB\n", u.cpu.Seconds()/20*100, u.rssMiB)
	return nil
}

func bench() error {
	if err := startPod("bench", 100000, "0.01", 50); err != nil {
		return err
	}
	defer deletePod("bench")
	start := time.Now()
	lines, bad, u, err := follow("bench", 120*time.Second, 100000)
	if err != nil {
		return err
	}
	el := time.Since(start) - 8*time.Second
	missing, dup := gaps(lines)
	fmt.Printf("bench: received=%d missing=%d dup=%d malformed=%d\n", len(lines), missing, dup, len(bad))
	if len(lines) > 0 {
		fmt.Printf("bench: cpu=%.2f%% of one core over %.1fs, rss=%.1f MiB, latency p50=%v p99=%v\n",
			u.cpu.Seconds()/el.Seconds()*100, el.Seconds(), u.rssMiB, pct(lines, 0.5), pct(lines, 0.99))
	}
	return nil
}

// rotation follows a long run, then tries to resume after the rotation.
func rotation() error {
	if err := startPod("rot", 400000, "0.01", 50); err != nil {
		return err
	}
	defer deletePod("rot")
	lines, bad, _, err := follow("rot", 150*time.Second, 400000)
	if err != nil {
		return err
	}
	missing, dup := gaps(lines)
	fmt.Printf("rotation: received=%d missing=%d dup=%d malformed=%d\n", len(lines), missing, dup, len(bad))
	fmt.Printf("rotation: node log files: %s\n", logFiles("rot"))
	if len(lines) < 2000 {
		return errors.New("too few lines for the resume check")
	}
	mark := lines[1000]
	out, err := kubectl("logs", "rot", "--timestamps", "--since-time="+mark.ts).Output()
	if err != nil {
		return err
	}
	first := strings.Fields(string(out))
	seq := -1
	if len(first) > 1 {
		seq, _ = strconv.Atoi(first[1])
	}
	fmt.Printf("resume: wanted first seq=%d, API returned first seq=%d (%d bytes)\n", mark.seq, seq, len(out))
	return nil
}

func startAtEnd() error {
	if err := startPod("slow", 1000000, "0.1", 10); err != nil {
		return err
	}
	defer deletePod("slow")
	time.Sleep(14 * time.Second)
	before, err := kubectl("logs", "slow", "--tail=1").Output()
	if err != nil {
		return err
	}
	last, err := strconv.Atoi(strings.Fields(string(before))[0])
	if err != nil {
		return err
	}
	lines, _, _, err := follow("slow", 4*time.Second, 0, "--tail=0")
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		return errors.New("no lines in start-at-end check")
	}
	fmt.Printf("start-at-end: last line before follow=%d, first line received=%d, pass=%t\n",
		last, lines[0].seq, lines[0].seq > last)
	return nil
}

func main() {
	steps := []struct {
		name string
		fn   func() error
	}{{"idle", idle}, {"start-at-end", startAtEnd}, {"bench", bench}, {"rotation", rotation}}
	if err := run("create", "ns", ns); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	defer func() {
		if err := exec.Command("kubectl", "delete", "ns", ns, "--wait=false").Run(); err != nil {
			fmt.Fprintln(os.Stderr, "delete ns:", err)
		}
	}()
	for _, s := range steps {
		if len(os.Args) > 1 && os.Args[1] != s.name {
			continue
		}
		if err := s.fn(); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", s.name, err)
		}
	}
}
