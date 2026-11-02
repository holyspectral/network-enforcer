// Command driver runs each tailer approach as a container and measures it with
// one standard. It reads CPU from cgroup cpu.stat usage_usec and memory from
// cgroup memory.stat anon, the same way for every approach, including the Go
// options. Each container writes its output to the receiver through its standard
// output. The generator writes the log file from outside the measured cgroup.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type approach struct {
	name      string
	image     string
	entry     string   // optional --entrypoint, empty uses the image default
	args      []string // arguments after the image
	env       []string // -e values
	ready     []string // stderr substrings that show the tailer watches the file
	mountConf bool     // mount the uniform conf dir at /conf and logs at /data/logs
}

var approaches = []approach{
	{
		name:  "custom",
		image: "spike-uniform-custom:latest",
		env:   []string{"LOG_GLOB=/logs/*.log"},
		ready: []string{"READY"},
	},
	{
		name:  "nxadm",
		image: "spike-uniform-nxadm:latest",
		env:   []string{"LOG_GLOB=/logs/*.log"},
		ready: []string{"READY"},
	},
	{name: "fc", image: "spike-uniform-fc:latest", env: []string{"LOG_GLOB=/logs/*.log"}, ready: []string{"READY"}},
	{
		name: "vector", image: "timberio/vector:0.50.0-alpine", entry: "/usr/local/bin/vector",
		args: []string{"--config", "/conf/vector.toml"}, mountConf: true,
		ready: []string{"Starting file server"},
	},
	{
		name: "fluent-bit", image: "fluent/fluent-bit:latest", entry: "/fluent-bit/bin/fluent-bit",
		args: []string{"-c", "/conf/fluent-bit.yaml"}, mountConf: true,
		ready: []string{"inotify_fs_add"},
	},
	{
		name: "otel", image: "otel/opentelemetry-collector-contrib:0.164.0", entry: "/otelcol-contrib",
		args: []string{"--config", "/conf/otel.yaml"}, mountConf: true,
		ready: []string{"Starting stanza receiver"},
	},
}

type result struct {
	name               string
	idlePct, loadPct   float64 // CPU as percent of one core over the actual window
	memMiB             float64
	p50ms, p99ms       float64
	received, distinct int
	dup, badwork       int
}

func main() {
	logs := flag.String("logs", "/tmp/uniform-logs", "shared log directory on the host")
	conf := flag.String("conf", "", "absolute path of the uniform conf directory")
	recvBin := flag.String("recv", "/tmp/uniform-recv", "receiver binary")
	genBin := flag.String("gen", "/tmp/uniform-gen", "generator binary")
	rate := flag.Int("rate", 5000, "lines per second for the load phase")
	loadSec := flag.Int("load", 20, "load phase seconds")
	idleSec := flag.Int("idle", 10, "idle phase seconds")
	only := flag.String("only", "", "run only this approach")
	flag.Parse()
	if *conf == "" {
		fmt.Fprintln(os.Stderr, "driver: -conf is required")
		os.Exit(1)
	}

	var results []result
	for _, a := range approaches {
		if *only != "" && a.name != *only {
			continue
		}
		r, err := run(a, *logs, *conf, *recvBin, *genBin, *rate, *loadSec, *idleSec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", a.name, err)
			continue
		}
		results = append(results, r)
		fmt.Printf("DONE %+v\n", r)
	}

	fmt.Println()
	fmt.Printf("%-12s %8s %8s %8s %8s %8s %8s %6s %6s\n",
		"approach", "idleCPU%", "loadCPU%", "memMiB", "p50ms", "p99ms", "recv", "dup", "bad")
	for _, r := range results {
		fmt.Printf("%-12s %8.2f %8.2f %8.1f %8.3f %8.3f %8d %6d %6d\n",
			r.name, r.idlePct, r.loadPct,
			r.memMiB, r.p50ms, r.p99ms, r.received, r.dup, r.badwork)
	}
}

func run(a approach, logs, conf, recvBin, genBin string, rate, loadSec, idleSec int) (result, error) {
	res := result{name: a.name}

	// Idle phase: start the container, wait, measure CPU over the idle window.
	idle, _, err := measure(a, logs, conf, recvBin, nil, idleSec)
	if err != nil {
		return res, fmt.Errorf("idle: %w", err)
	}
	res.idlePct = cpuPct(idle)

	// Load phase: run the generator during the measurement window.
	gen := func() error {
		cmd := exec.Command(genBin, "-dir", logs, "-rate", strconv.Itoa(rate), "-seconds", strconv.Itoa(loadSec))
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	load, out, err := measure(a, logs, conf, recvBin, gen, loadSec+3)
	if err != nil {
		return res, fmt.Errorf("load: %w", err)
	}
	res.loadPct = cpuPct(load)
	res.memMiB = load.memMiB
	parseRecv(out, &res)
	return res, nil
}

// cpuPct converts the measured CPU over the actual wall window to a percent of
// one core. It uses the real elapsed time of the window, so the idle window and
// the load window are each normalized by their own duration.
func cpuPct(w window) float64 {
	if w.wall <= 0 {
		return 0
	}
	return float64(w.cpu) / float64(w.wall) * 100
}

type window struct {
	cpu    time.Duration
	wall   time.Duration
	memMiB float64
}

// measure starts the approach container, waits for ready, reads the cgroup CPU
// at the window start, runs work (or idles), reads the cgroup CPU and memory at
// the window end, then stops the container. It returns the receiver output.
func measure(a approach, logs, conf, recvBin string, work func() error, windowSec int) (window, string, error) {
	var w window
	if err := resetLogs(logs); err != nil {
		return w, "", err
	}

	name := fmt.Sprintf("spike-uniform-%s-%d", a.name, time.Now().UnixNano())
	args := []string{"run", "--rm", "--name", name}
	for _, e := range a.env {
		args = append(args, "-e", e)
	}
	if a.mountConf {
		// Tools run as the current user, so the state files they write stay owned
		// by the harness. Each run uses a fresh state directory.
		state := fmt.Sprintf("%s-state-%d", logs, time.Now().UnixNano())
		if err := os.MkdirAll(state, 0o777); err != nil {
			return w, "", err
		}
		if err := os.Chmod(state, 0o777); err != nil {
			return w, "", err
		}
		defer os.RemoveAll(state)
		args = append(args, "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
			"-v", logs+":/data/logs:ro", "-v", conf+":/conf:ro", "-v", state+":/data/state")
	} else {
		args = append(args, "-v", logs+":/logs:ro")
	}
	if a.entry != "" {
		args = append(args, "--entrypoint", a.entry)
	}
	args = append(args, a.image)
	args = append(args, a.args...)

	// Receiver reads the container standard output.
	recvCmd := exec.Command(recvBin)
	recvIn, err := recvCmd.StdinPipe()
	if err != nil {
		return w, "", err
	}
	var recvOut bytes.Buffer
	recvCmd.Stdout = &recvOut
	recvCmd.Stderr = os.Stderr
	if err := recvCmd.Start(); err != nil {
		return w, "", err
	}

	cmd := exec.Command("docker", args...)
	cmd.Stdout = recvIn
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return w, "", err
	}
	if err := cmd.Start(); err != nil {
		return w, "", err
	}

	ready := make(chan struct{})
	go watchReady(stderr, a.ready, ready)

	select {
	case <-ready:
	case <-time.After(40 * time.Second):
		stopContainer(name)
		_ = recvIn.Close()
		_ = recvCmd.Wait()
		_ = cmd.Wait()
		return w, "", fmt.Errorf("not ready after 40s")
	}
	time.Sleep(700 * time.Millisecond)

	id, err := containerID(name)
	if err != nil {
		stopContainer(name)
		return w, "", err
	}
	base := "/sys/fs/cgroup/system.slice/docker-" + id + ".scope/"

	cpu0 := readCPU(base)
	t0 := time.Now()
	if work != nil {
		if err := work(); err != nil {
			stopContainer(name)
			return w, "", fmt.Errorf("work: %w", err)
		}
		time.Sleep(2 * time.Second) // drain
	} else {
		time.Sleep(time.Duration(windowSec) * time.Second)
	}
	cpu1 := readCPU(base)
	w.cpu = cpu1 - cpu0
	w.wall = time.Since(t0)
	w.memMiB = readAnonMiB(base)

	stopContainer(name)
	_ = cmd.Wait()
	_ = recvIn.Close()
	_ = recvCmd.Wait()
	return w, recvOut.String(), nil
}

func watchReady(r interface{ Read([]byte) (int, error) }, markers []string, ready chan struct{}) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	closed := false
	for sc.Scan() {
		if os.Getenv("DRIVER_DEBUG") != "" {
			fmt.Fprintln(os.Stderr, "CTR|", sc.Text())
		}
		if closed {
			continue
		}
		for _, m := range markers {
			if strings.Contains(sc.Text(), m) {
				close(ready)
				closed = true
				break
			}
		}
	}
}

func resetLogs(logs string) error {
	if err := os.RemoveAll(logs); err != nil {
		return err
	}
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return err
	}
	f, err := os.Create(logs + "/0.log")
	if err != nil {
		return err
	}
	return f.Close()
}

func containerID(name string) (string, error) {
	out, err := exec.Command("docker", "inspect", "-f", "{{.Id}}", name).Output()
	if err != nil {
		return "", fmt.Errorf("docker inspect: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func stopContainer(name string) {
	if err := exec.Command("docker", "stop", "-t", "5", name).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "stop:", err)
	}
}

func readCPU(base string) time.Duration {
	v := readCgroupKey(base+"cpu.stat", "usage_usec ")
	return time.Duration(v) * time.Microsecond
}

func readAnonMiB(base string) float64 {
	return float64(readCgroupKey(base+"memory.stat", "anon ")) / (1 << 20)
}

func readCgroupKey(path, key string) int64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		panic(path + ": " + err.Error())
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(l, key); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				panic(path + " " + key + ": " + err.Error())
			}
			return n
		}
	}
	panic(path + ": key " + key + " missing")
}

// parseRecv reads the RESULT line the receiver prints.
func parseRecv(out string, res *result) {
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "RESULT ") {
			continue
		}
		for _, f := range strings.Fields(l) {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			switch k {
			case "received":
				res.received, _ = strconv.Atoi(v)
			case "distinct":
				res.distinct, _ = strconv.Atoi(v)
			case "dup":
				res.dup, _ = strconv.Atoi(v)
			case "badwork":
				res.badwork, _ = strconv.Atoi(v)
			case "p50ms":
				res.p50ms, _ = strconv.ParseFloat(v, 64)
			case "p99ms":
				res.p99ms, _ = strconv.ParseFloat(v, 64)
			}
		}
	}
}
