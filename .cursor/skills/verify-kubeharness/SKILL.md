---
name: verify-kubeharness
description: Drive kubeharness (Go Kind-cluster test library) via go test and hack scripts; use when proving owned-cluster lifecycle, workload methods, Gateway install, or leak detection against a real Docker/Kind stack.
---

# Verify kubeharness

KubeHarness is a Go library: the user surface is `go test` against an owned Kind cluster, plus `hack/leaks.sh` / `hack/screen.sh`. There is no web UI or long-lived app process. Each drive is a short-lived `go test` process that creates and (on success) deletes its own `kh-*` cluster(s). Docker is shared host infrastructure — never stop the daemon and never delete clusters that are not `kh-*`.

## Launch

From the repository root:

1. Ensure Go can satisfy `go.mod` (`go 1.27` / `toolchain go1.27.3`) and Docker answers `docker info`.
2. Source the toolchain helper once per shell (or let the other helpers source it):

```bash
source .cursor/skills/verify-kubeharness/scripts/goenv.sh
```

3. There is nothing to keep alive. "Ready" means doctor passes (packages compile, Docker up, leak listing works). Kind node images may still need a first pull during the first e2e drive.

Teardown of a drive is the test's own `t.Cleanup` plus, if anything leaked, `scripts/cleanup.sh`. Final host teardown after a verification run: run cleanup, leave `artifacts/` intact.

## Doctor

```bash
.cursor/skills/verify-kubeharness/scripts/doctor.sh
```

Expect lines ending in `ready`. Failures mean: wrong module, Go/toolchain compile error, Docker down, or `hack/leaks.sh` cannot list containers. A WARN about leftover `kh-*` clusters is not a hard fail — account for them when judging post-drive leaks.

Run doctor before the first drive, after any failed drive, and before trusting a new shell.

## Drive

Prefer the drive helper so evidence lands in a named run directory:

```bash
.cursor/skills/verify-kubeharness/scripts/drive.sh <feature-id> \
  -tags e2e -count=1 -timeout 20m -v -run '<TestName>' ./e2e
```

Stable handles are Go test names and report-line fields, not UI selectors:

| Signal | Meaning |
| --- | --- |
| `--- PASS: Test…` | Feature assertion passed |
| `kubeharness cluster=kh-… phase=ready cause=ok cleanup=succeeded remaining=0` | Owned cluster became usable and was disposed |
| `profile=gateway-api install=…` | Gateway profile recorded on the report line |
| `hack/leaks.sh` → `kubeharness leaks: 0` | No leftover `kh-*` containers |

Unit / fake-provider drives (no Docker Kind create) omit `-tags e2e` and target packages such as `.` or `./internal/...`. Feature files say which.

Only drive clusters this run creates. Do not delete a foreign Kind cluster named `kind` (e2e decoy) or any non-`kh-*` name.

## Evidence

Proof artifacts go under:

```text
.cursor/skills/verify-kubeharness/artifacts/<run-id>/
```

`drive.sh` writes `<feature>.log`, `<feature>.meta.txt`, and leaks before/after snapshots. Proof standards:

- Exercise the real user path: `kubeharness.New` / workload methods / `InstallGateway` inside `go test -tags e2e`, not internal fakes, when the feature map says Kind.
- Capture the action (the `go test -run` invocation and log) and the resulting state (PASS line, report line, post-drive `leaks: 0`).
- Side effects: Kind containers gone after cleanup; for Forward, HTTP responses in the log; for gateway, Accepted GatewayClass in the log.
- Keep artifacts after cleanup. Never `rm -rf artifacts/` as part of teardown.

## Cleanup

```bash
.cursor/skills/verify-kubeharness/scripts/cleanup.sh          # all kh-*
.cursor/skills/verify-kubeharness/scripts/cleanup.sh kh-abc…  # named only
```

Removes only `kh-*` Kind clusters (via `kind delete` when present, else labeled `docker rm -f`). Does not touch Docker Desktop, does not touch evidence. After a failed drive, always run cleanup so ports and node containers do not strand the next attempt.

## Helpers

All under `.cursor/skills/verify-kubeharness/scripts/` (executable):

| Script | Invocation |
| --- | --- |
| `goenv.sh` | `source .cursor/skills/verify-kubeharness/scripts/goenv.sh` |
| `doctor.sh` | `.cursor/skills/verify-kubeharness/scripts/doctor.sh` |
| `drive.sh` | `.cursor/skills/verify-kubeharness/scripts/drive.sh <feature-id> <go test args…>` |
| `cleanup.sh` | `.cursor/skills/verify-kubeharness/scripts/cleanup.sh [kh-name…]` |

Repo helpers used by drives: `hack/leaks.sh [pattern]`, `hack/screen.sh [runs] [pattern]` (screening is optional soak, not required for a single-feature proof).

## Feature map

See [features/README.md](features/README.md). Cover mapped features from that index; a proof that skips a listed entry point is incomplete for that feature.
