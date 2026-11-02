package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"spike/common"
)

type tool struct {
	name   string
	image  string
	entry  string
	args   []string
	ready  string // text on stderr that shows the tool watches the file
	parse  func(line []byte) []string
	config string
}

var tools = map[string]tool{
	"vector": {
		name:  "vector",
		image: "timberio/vector:0.50.0-alpine",
		entry: "/usr/local/bin/vector",
		args: []string{
			"--config",
			"/conf/vector.toml",
		},
		ready:  "Found new file to watch|Resuming to watch file",
		config: "conf/vector.toml",
		parse: func(l []byte) []string {
			var m struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(l, &m) != nil || m.Message == "" {
				return nil
			}
			return []string{m.Message}
		},
	},
	"fluent-bit": {
		name:   "fluent-bit",
		image:  "fluent/fluent-bit@sha256:2740f387f6e68fab66d28e4fa6a853387b9cab6ffb4cb78958f69bba4eb0e7b4",
		entry:  "/fluent-bit/bin/fluent-bit",
		args:   []string{"-c", "/conf/fluent-bit.yaml"},
		ready:  "inotify_fs_add",
		config: "conf/fluent-bit.yaml",
		parse: func(l []byte) []string {
			var m struct {
				Log string `json:"log"`
			}
			if json.Unmarshal(l, &m) != nil || m.Log == "" {
				return nil
			}
			return []string{m.Log}
		},
	},
	"otel": {
		name:  "otel",
		image: "otel/opentelemetry-collector-contrib:0.164.0",
		entry: "/otelcol-contrib",
		args: []string{
			"--config",
			"/conf/otel.yaml",
		},
		ready:  "Started watching file|Resuming from previously known",
		config: "conf/otel.yaml",
		parse: func(l []byte) []string {
			var m struct {
				ResourceLogs []struct {
					ScopeLogs []struct {
						LogRecords []struct {
							Body struct {
								StringValue string `json:"stringValue"`
							} `json:"body"`
						} `json:"logRecords"`
					} `json:"scopeLogs"`
				} `json:"resourceLogs"`
			}
			if json.Unmarshal(l, &m) != nil {
				return nil
			}
			var out []string
			for _, r := range m.ResourceLogs {
				for _, s := range r.ScopeLogs {
					for _, lr := range s.LogRecords {
						out = append(out, lr.Body.StringValue)
					}
				}
			}
			return out
		},
	},
}

var seq atomic.Int64

// current holds the container id of the latest started tool, for the benchmark probe.
var (
	curMu sync.Mutex
	curID string
)

func factory(t tool) common.Factory {
	return func(glob, state string, onLine common.Emit) (func(), error) {
		logs := filepath.Dir(glob)
		stateDir := ""
		if state != "" {
			stateDir = filepath.Dir(state)
		} else {
			d, err := os.MkdirTemp("", "extstate")
			if err != nil {
				return nil, err
			}
			stateDir = d
		}
		if err := os.Chmod(logs, 0o755); err != nil {
			return nil, err
		}
		confAbs, err := filepath.Abs(filepath.Dir(t.config))
		if err != nil {
			return nil, err
		}
		name := fmt.Sprintf("spike-%s-%d-%d", t.name, os.Getpid(), seq.Add(1))
		args := []string{"run", "--rm", "--name", name, "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
			"-v", logs + ":/data/logs:ro", "-v", stateDir + ":/data/state", "-v", confAbs + ":/conf:ro",
			"--entrypoint", t.entry, t.image}
		args = append(args, t.args...)
		cmd := exec.Command("docker", args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		ready := make(chan struct{})
		var once sync.Once
		go func() {
			sc := bufio.NewScanner(stderr)
			sc.Buffer(make([]byte, 1<<20), 1<<20)
			for sc.Scan() {
				for _, m := range strings.Split(t.ready, "|") {
					if strings.Contains(sc.Text(), m) {
						once.Do(func() { close(ready) })
					}
				}
			}
		}()
		done := make(chan struct{})
		go func() {
			defer close(done)
			sc := bufio.NewScanner(stdout)
			sc.Buffer(make([]byte, 4<<20), 4<<20)
			for sc.Scan() {
				for _, l := range t.parse(sc.Bytes()) {
					// A container tool is a black box. The harness cannot see its
					// read time, so it passes the zero value and measures only the
					// end-to-end latency.
					onLine([]byte(l), time.Time{})
				}
			}
		}()
		select {
		case <-ready:
		case <-time.After(30 * time.Second):
			if err := exec.Command("docker", "rm", "-f", name).Run(); err != nil {
				fmt.Fprintln(os.Stderr, "rm:", err)
			}
			<-done
			if err := cmd.Wait(); err != nil {
				fmt.Fprintln(os.Stderr, "wait:", err)
			}
			if state == "" {
				if err := os.RemoveAll(stateDir); err != nil {
					fmt.Fprintln(os.Stderr, "rm state:", err)
				}
			}
			return nil, fmt.Errorf("%s: not ready after 30s", t.name)
		}
		idOut, err := exec.Command("docker", "inspect", "-f", "{{.Id}}", name).Output()
		if err != nil {
			if rmErr := exec.Command("docker", "rm", "-f", name).Run(); rmErr != nil {
				fmt.Fprintln(os.Stderr, "rm:", rmErr)
			}
			return nil, fmt.Errorf("%s: docker inspect: %w", t.name, err)
		}
		curMu.Lock()
		curID = strings.TrimSpace(string(idOut))
		curMu.Unlock()
		time.Sleep(500 * time.Millisecond)
		return func() {
			if err := exec.Command("docker", "stop", "-t", "10", name).Run(); err != nil {
				fmt.Fprintln(os.Stderr, "stop:", err)
			}
			<-done
			if err := cmd.Wait(); err != nil {
				fmt.Fprintln(os.Stderr, "wait:", err)
			}
			if state == "" {
				if err := os.RemoveAll(stateDir); err != nil {
					fmt.Fprintln(os.Stderr, "rm state:", err)
				}
			}
		}, nil
	}
}

// probe reads CPU time and anonymous memory of the tool container from cgroup v2.
// It panics when a value is missing, so the benchmark never reports a silent 0.
func probe() (time.Duration, float64) {
	curMu.Lock()
	id := curID
	curMu.Unlock()
	base := "/sys/fs/cgroup/system.slice/docker-" + id + ".scope/"
	cpu, ok := readCgroupKey(base+"cpu.stat", "usage_usec ")
	if !ok {
		panic("cpu.stat usage_usec missing for " + id)
	}
	mem, ok := readCgroupKey(base+"memory.stat", "anon ")
	if !ok {
		panic("memory.stat anon missing for " + id)
	}
	return time.Duration(cpu) * time.Microsecond, float64(mem) / (1 << 20)
}

func readCgroupKey(path, key string) (int64, bool) {
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
			return n, true
		}
	}
	return 0, false
}
