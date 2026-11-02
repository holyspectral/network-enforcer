# File tailer spike: test steps

This spike supports the RFC `docs/rfc/0006-file-tailer.md` (issue #637).
The code is throwaway. Do not commit it to the product source tree.

## Options under test

| Option | Directory | How it runs |
| ------ | --------- | ----------- |
| stanza `fileconsumer` v0.164.1 | `fc/` | Go library in the test process |
| Custom tailer (prototype) | `custom/` | Go code in the test process |
| `nxadm/tail` | `nxadm/` | Go library in the test process |
| OTel Collector `filelog` v0.164.0 | `ext/` | Container `otel/opentelemetry-collector-contrib` |
| Vector 0.50.0 | `ext/` | Container `timberio/vector` |
| Fluent Bit v5.1.5 | `ext/` | Container `fluent/fluent-bit`, pinned by digest `sha256:2740f387f6e68fab66d28e4fa6a853387b9cab6ffb4cb78958f69bba4eb0e7b4` |
| Kubelet log API | `kubelet/` | `kubectl logs` against a kind cluster (Step 5) |

Each directory is a separate Go module. `common/` holds the shared scenarios
and the per-module benchmark driver. `uniform/` holds the one-standard cost
harness (generator, receiver, driver and tool configs). No module changes the
repository `go.mod`.

## Requirements

- Go 1.27 or later.
- Rootful Docker with cgroup v2 and the systemd cgroup driver. The benchmark
  reads `/sys/fs/cgroup/system.slice/docker-<id>.scope/`. Other layouts report
  zero CPU and memory.
- Network access to pull the three container images.
- A kind cluster and `kubectl` for the kubelet test only.

## Step 1: Correctness scenarios

Options 1 to 5b run the same eight scenarios from `common/scenarios.go`.
Option 6 (kubelet) has its own harness (Step 5).
Each scenario writes containerd CRI format lines to a temporary directory.
The glob is `*.log*`, so a renamed file stays tracked.
Each scenario waits for the exact set of lines, then waits again, so a late
duplicate also fails the scenario.

| Scenario | What it checks |
| -------- | -------------- |
| `start_at_end_on_first_run` | 50 old lines are not emitted. 10 new lines are emitted. |
| `no_partial_lines` | Half a line is not emitted. The full line is emitted once. |
| `rename_rotation_no_loss` | 100 lines are written, then the file is renamed, then 100 lines go to a new file. All 200 arrive once. |
| `copytruncate_no_loss` | 100 lines arrive. The file is truncated to zero. 100 new lines arrive. |
| `restart_resumes_from_saved_offset` | The tailer stops. 50 lines are written. A new tailer with the same state emits only the 50 new lines. |
| `restart_after_copytruncate` | The file is truncated, 50 new lines arrive, the tailer stops, 50 more lines are written, and a new tailer emits only the last 50. |
| `container_restart_no_loss` | 100 lines go to `0.log`, then a new `1.log` gets 100 lines while `0.log` stays. The tailer drains `0.log` and reads `1.log` from the start. All 200 arrive once. |
| `pod_deletion_drains_open_file` | 100 lines go to `0.log`, then the file is removed. The tailer drains the open descriptor before it closes. All 100 arrive once. |

The scenarios do not simulate a hard crash (`kill -9`).

Commands:

```bash
(cd spike/fc     && go test -count=1 -v ./...)
(cd spike/custom && go test -count=1 -v ./...)
(cd spike/nxadm  && go test -count=1 -v ./...)
(cd spike/ext    && TOOL=vector     go test -count=1 -v ./...)
(cd spike/ext    && TOOL=fluent-bit go test -count=1 -v ./...)
(cd spike/ext    && TOOL=otel       go test -count=1 -v ./...)
```

Settings that change the result:

- `fileconsumer` and `filelog`: `force_flush_period: 0` and
  `on_truncate: read_new`. The default flush period emits partial lines.
- `fileconsumer`: the test uses a JSON file as the offset store.
- `nxadm/tail`: the test adds its own glob loop and offset file.
- Vector and Fluent Bit: default behavior, with `read_from = "end"` and
  `read_from_head: false`.
- Container tools run as the host user (`--user uid:gid`) with the log
  directory mounted read-only. They write JSON to stdout. The driver reads the
  stdout pipe. The driver treats a log line from the tool as the ready signal.

## Step 2: Cost benchmark (one standard)

The authoritative cost numbers come from the uniform harness in `uniform/`. It
measures every file option the same way, so the CPU, the memory, the latency and
the delivery compare directly across all five file options.

- Every option runs as its own container in its own cgroup.
- Every option does the same per-line work: it appends the constant `ASD`.
- Every option writes each line to one shared receiver over standard output.
- A separate generator process (`uniform/gen`) writes the log file at a fixed
  rate from outside the measured cgroup, so its cost is never charged to a
  tailer.
- The driver (`uniform/driver`) reads CPU from the cgroup `cpu.stat usage_usec`
  field and memory from the cgroup `memory.stat anon` field, the same way for
  the Go options and the tools.
- The receiver (`uniform/recv`) reads the write time and the sequence number
  from each line and reports the end-to-end latency, the missing count and the
  duplicate count. It scans the raw bytes for the `TS=` and `N=` markers and the
  `ASD` suffix, so it tolerates a JSON or batched envelope that a tool adds.

The Go options run the same tailer code in an `export` mode (`<binary> export`),
which appends `ASD` and writes to standard output. The tools append `ASD` with a
native transform (Vector remap, Fluent Bit Lua, OTel transform), so the append
cost stays in the tool cgroup. The tool configs are in `uniform/conf/`.

Commands:

```bash
# Build the Go tailer images (static scratch images).
for m in custom nxadm fc; do
  (cd spike/$m && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" \
     -o ../uniform/images/$m-tailer .)
  (cd spike/uniform/images && docker build -q -f Dockerfile.scratch \
     --build-arg BIN=$m-tailer -t spike-uniform-$m:latest .)
done

# Build the generator, the receiver and the driver.
(cd spike/uniform && go build -o /tmp/uniform-gen ./gen \
  && go build -o /tmp/uniform-recv ./recv \
  && go build -o /tmp/uniform-driver ./driver)

# Run all six file approaches. -only <name> runs one.
/tmp/uniform-driver -conf "$(pwd)/spike/uniform/conf" \
  -recv /tmp/uniform-recv -gen /tmp/uniform-gen \
  -rate 5000 -load 20 -idle 10
```

Notes:

- CPU is a percentage of one core. For both the Go options and the tools it is
  `usage_usec` from the container cgroup. Memory is `anon` from `memory.stat`.
- The poll interval is 200 ms where the tool allows it. Fluent Bit uses
  `flush: 0.2` and inotify.
- Each number in the RFC is the mean of two runs.

The older in-process driver (`common/bench.go`) still backs the per-module mains
and the scenario tests. It is not the source of the RFC cost numbers.

## Step 3: Binary size and modules (Go options)

```bash
(cd spike/fc && go build -trimpath -ldflags "-s -w" -o /tmp/fc-bin . \
  && stat -c%s /tmp/fc-bin \
  && go list -deps -f '{{with .Module}}{{.Path}}{{end}}' . | sort -u | grep -vc '^spike/\|^$')
```

Run the same commands in `custom/` and `nxadm/`. The custom binary is the
baseline. It includes the benchmark driver. For container tools, record the
image size with `docker images`.

## Step 4: Upstream health

```bash
for r in open-telemetry/opentelemetry-collector-contrib vectordotdev/vector \
         fluent/fluent-bit nxadm/tail kubernetes/kubernetes; do
  gh api repos/$r --jq '[.full_name,.pushed_at,.archived]|@tsv'
done
```

## Step 5: Kubelet log API

The `kubelet/` harness needs a kind cluster and `kubectl`. It creates a
throwaway namespace `tailer-spike`, runs a generator pod, follows the log with
`kubectl logs`, and deletes the namespace at the end.

```bash
(cd spike/kubelet && go run .)
```

It runs four steps and prints one line each:

1. `idle`: a quiet pod. It reports the client CPU and memory.
2. `start-at-end`: it checks that a new follow does not replay old lines.
3. `bench`: the pod writes 100000 lines at about 5000 lines/s. It reports the
   received count, the gaps, the client CPU and memory, and the latency.
4. `rotation`: the pod writes 400000 lines, which crosses several rotations. It
   checks for gaps, lists the node log files, then asks the API for an early
   time with `--since-time`. The API returns only the current file, so the
   early lines are lost.

Pass a step name to run one step, for example `go run . bench`. The client CPU
is from `kubectl`, not a tuned Go client, so it is not comparable with the
other options.

## Known limits

- One machine. Synthetic CRI lines. No real containerd rotation.
- The scenarios use `rename` and `truncate`. The kubelet rotates by rename.
- The benchmark does not measure the classifier or the pull API.
- The uniform generator runs outside the measured cgroup, so a tailer is not
  charged for the line writes. The kubelet cost is measured by its own client
  harness, not on the uniform standard.
