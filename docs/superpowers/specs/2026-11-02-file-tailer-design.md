# File tailer design

Implements the decision in `docs/rfc/0006-file-tailer.md`: a custom, project-owned tailer for CRI container logs.

## Goal

Provide `internal/tailer`, a pure-Go, stdlib-only package. It reads containerd CRI log files from `/var/log/pods/...` and emits complete log lines to a handler. It meets every hard criterion and every "Production scope" item in the RFC.

## Scope

In scope:

- The `internal/tailer` package, its interface, CRI parsing, checkpoint store and tests.

Out of scope:

- The agent binary and the DaemonSet wiring.
- Removal of Fluent Bit.
- Exactly-once delivery. Delivery is at-least-once.

## Assumptions

- The tailer polls. The default interval is 200 ms.
- The kubelet rotates by rename (`0.log` to `0.log.<timestamp>`). It compresses older rotated files to `.gz` through a `.tmp` file. It keeps the newest rotated file uncompressed.
- The spike prototype (`spike/custom/tailer.go`) is a source of scenarios only. Production code is written fresh from tests.

## Interface

```go
type Line struct {
    Path   string
    Stream string    // stdout or stderr
    Time   time.Time // CRI timestamp
    ReadAt time.Time // time of the read
    Text   []byte
}

type Handler func(Line)

type Tailer interface {
    Start(ctx context.Context) error
    Stop() error
}
```

A `Config` struct holds the glob, the checkpoint path, the poll interval, the maximum line size, the missing-file grace polls and the checkpoint retention. All fields except the glob and the checkpoint path have defaults.

## Components

| File            | Purpose                                                                                    |
| --------------- | ------------------------------------------------------------------------------------------ |
| `tailer.go`     | Interface, `Config`, poll loop, start and stop.                                            |
| `cri.go`        | Parse `time stream P\|F content`. Join `P` records until an `F` record.                    |
| `tracker.go`    | Per-file state keyed by inode: descriptor, offset, fingerprint, partial buffer.            |
| `checkpoint.go` | Atomic JSON store: temporary file, `fsync`, rename, directory `fsync`. Corrupt file means start at end. |

## Behavior

- **Match.** The glob accepts live `*.log` files. It rejects `.gz` and `.tmp`. Tracking uses the inode, so a renamed file stays tracked through its open descriptor.
- **First run.** Start at the end of each file, aligned forward to the next line boundary. A file that appears after start is read from offset 0. This covers a new container instance.
- **Read.** Each poll reads every tracked file to EOF. Only lines that end in `\n` are emitted. The checkpoint stores the offset of the last complete line.
- **Truncation.** Detect a size smaller than the offset, or a fingerprint mismatch at offset 0. Restart at offset 0 and refresh the fingerprint.
- **Drain and close.** Close a descriptor when the file is fully read and either a newer file exists or the path is gone. A removed file is read to the end through the open descriptor first.
- **Missing file.** Keep the checkpoint for a grace period of N polls. Prune checkpoints of deleted pods after the retention time.
- **Resume.** Match saved checkpoints by fingerprint. A first line shorter than the fingerprint size refills the fingerprint as the file grows.
- **Line size.** Drop a line above the maximum size, log it and count it.
- **Errors.** A read or permission error on one file does not stop other files.
- **Stop.** Save the checkpoint and close all descriptors.
- **Lag.** If a rotated file is already compressed or removed, log a warning and continue.

## Testing (test-driven)

Rules:

- Each cycle is RED, verify failure, GREEN, verify pass, REFACTOR.
- No production code exists without a failing test first.
- Tests are table-driven, use `testify` and use real temporary directories. No mocks.
- Each scenario asserts the exact line set, waits one more poll and asserts again.
- Run `go test -race ./internal/tailer/...` after each GREEN. Run `make test` and `make lint-fix` at each milestone end.

Cycles:

1. M1 Foundations
   1. CRI parser: `F` line, `P` then `F` join, malformed line error.
   2. Maximum line size.
   3. Checkpoint store: round trip, atomic write, corrupt file.
2. M2 Core poller
   1. Emit appended complete lines.
   2. No partial line.
   3. First run starts at end.
   4. First-run line boundary alignment.
   5. Restart resumes from the saved offset.
   6. Short first line and fingerprint refill.
3. M3 Rotation and lifecycle
   1. Rename rotation, no loss, no duplicate.
   2. Rotation under load.
   3. `copytruncate`.
   4. Truncate and regrow past the old offset between polls.
   5. Restart after `copytruncate`.
   6. Container restart: drain the old file, read the new file from offset 0.
   7. Pod deletion: drain the removed file.
   8. Close a fully read rotated file.
   9. Glob ignores `.gz` and `.tmp`.
   10. Lag across rotations or downtime.
4. M4 Resilience
   1. Missing file keeps its checkpoint.
   2. Inode reuse inside the grace period.
   3. Prune checkpoints of deleted pods.
   4. Unreadable file does not stop other files.
   5. Stop saves the checkpoint and closes descriptors.
5. M5 Wrap-up
   1. Update the RFC production-scope status. Run the full suite with `-race` and lint.

## Coverage of RFC production scope

| RFC item                                 | Cycle       |
| ---------------------------------------- | ----------- |
| Close descriptors of read rotated files  | M3.8        |
| Keep checkpoint of briefly absent file   | M4.1        |
| First-run line boundary                  | M2.4        |
| Detect truncate and regrow               | M3.4        |
| Remove checkpoints of deleted pods       | M4.3        |
| Match live log only                      | M3.9        |
| Read rotated file before compress/remove | M3.1, M3.10 |
| New container instance from start        | M3.6        |
| Drain removed file                       | M3.7        |
| Inode reuse grace                        | M4.2        |
| First line shorter than fingerprint      | M2.6        |
| Limit line size                          | M1.2        |
| Atomic checkpoint, corrupt recovery      | M1.3        |
| Per-file error isolation                 | M4.4        |
| CRI partial flag                         | M1.1        |
| Unit tests from RFC scenarios            | M2 to M4    |
