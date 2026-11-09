# KubeHarness verification map

This directory is the maintained source for verifying user-facing behavior of the kubeharness Go library. Read the index before driving, then use the matching feature file as the recipe.

## Baseline preconditions

- Repository root is the kubeharness module (`github.com/ogarciacar/kubeharness`).
- `source .cursor/skills/verify-kubeharness/scripts/goenv.sh` so Go matches `go.mod`'s toolchain.
- `.cursor/skills/verify-kubeharness/scripts/doctor.sh` exits 0 (Docker up for any e2e feature).
- Evidence directory: `.cursor/skills/verify-kubeharness/artifacts/<run-id>/` (created by `drive.sh`).
- Never delete non-`kh-*` Kind clusters. Never stop the Docker daemon.
- Prefer one e2e drive at a time on a laptop; parallel `kh-*` clusters are supported by the library but share Docker CPU/disk.

## Driving conventions

- Start from doctor-ready state unless a feature lists extra preconditions.
- Drive with `scripts/drive.sh <feature-id> …` so logs and leak snapshots are retained.
- Treat test names, package paths, and report-line field names as literal.
- After every failed drive: run `scripts/cleanup.sh`, keep artifacts, re-run doctor, then retry.
- Fake-provider unit tests prove fault reporting without Kind; they do not replace Kind proofs for features that list e2e entry points.

## Proof and skip reporting

- Capture the `go test` command, full log, PASS/FAIL, and the `kubeharness cluster=…` report line when Kind was used.
- Mutation / disposal proof includes `hack/leaks.sh` (or the drive helper's `*.leaks-after.txt`) showing no new `kh-*` leftovers attributable to the drive.
- Record the feature ID and `-run` pattern with every artifact (`*.meta.txt`).
- Report unreachable paths with the attempted command and unmet precondition (for example Docker down).
- Do not report a skipped Kind entry point as verified via the fake-provider suite alone.

## Feature entry contract

Each feature file starts with an H1 title and one paragraph describing the user-visible behavior. It then uses exactly four H2 sections in this order.

1. `Sub-features` lists short IDs with one line for each behavior.
2. `How to get to it (user POV)` lists every user entry point.
3. `Driving it with drive.sh` starts with `Preconditions:` and uses labeled bullets that pair each user action with an exact command and observable result.
4. `Gotchas` lists traps that can waste or invalidate a verification run.

Keep implementation details out of the map. Name only user paths, stable handles, required state, commands, and observable proof.

## Features

- [Owned cluster lifecycle](./owned-cluster.md) covers `New`, usable API, disposal, and failing-test cleanup on real Kind.
- [Workload methods](./workload-methods.md) covers Apply, WaitReady, RunJob, Forward, and LoadImage.
- [Install Gateway](./install-gateway.md) covers the Envoy Gateway and Agent Gateway profiles and failure evidence.
- [Gateway traffic](./gateway-traffic.md) covers `Gateway`, `Client`, `AwaitRoute`, `AwaitResponse`, and `AwaitProbe`.
- [Leak detection](./leak-detection.md) covers `hack/leaks.sh` listing and the cleanup helper.
