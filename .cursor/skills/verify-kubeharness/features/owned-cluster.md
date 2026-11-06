# Owned cluster lifecycle

Owned cluster lifecycle gives each Go test a disposable Kind cluster via `kubeharness.New(t)`, waits until the API accepts workloads, logs a report line, and deletes the cluster through `t.Cleanup` even when the test fails.

## Sub-features

- `new-usable` creates a `kh-*` cluster that accepts a Pod and does not rewrite the developer's `KUBECONFIG`.
- `new-dispose` removes the cluster's containers when the test ends with success.
- `fail-dispose` still removes the cluster when the test body fails.
- `parallel-distinct` gives parallel subtests distinct cluster names that all dispose.

## How to get to it (user POV)

- Write `h := kubeharness.New(t)` in a Go test and run `go test -tags e2e`.
- Run the library e2e suite tests named `TestNewOwnedClusterIsUsableAndDisposed`, `TestFailingTestStillDisposesCluster`, and `TestParallelClustersAreDistinctAndDisposed`.

## Driving it with drive.sh

Preconditions:

- Doctor is ready (Docker up, packages compile).
- No requirement that the machine has zero `kh-*` clusters beforehand; record before/after via `drive.sh`.

- **Usable and disposed.** Run `.cursor/skills/verify-kubeharness/scripts/drive.sh owned-cluster -tags e2e -count=1 -timeout 20m -v -run '^TestNewOwnedClusterIsUsableAndDisposed$' ./e2e`. Exit code `0`, log contains `--- PASS: TestNewOwnedClusterIsUsableAndDisposed`, and `*.leaks-after.txt` shows no new `kh-*` cluster left by the drive.
- **Failing test still disposes.** Run `.cursor/skills/verify-kubeharness/scripts/drive.sh owned-cluster-fail -tags e2e -count=1 -timeout 20m -v -run '^TestFailingTestStillDisposesCluster$' ./e2e`. Exit code `0` (the outer test expects the helper to fail and the cluster to vanish).
- **Parallel distinct.** Run `.cursor/skills/verify-kubeharness/scripts/drive.sh owned-cluster-parallel -tags e2e -count=1 -timeout 20m -v -run '^TestParallelClustersAreDistinctAndDisposed$' ./e2e`. Exit code `0` and three distinct names appear in the log with no leftover containers.
- **Proof.** Keep the `artifacts/<run-id>/owned-cluster.log` (and sibling metas). Confirm `hack/leaks.sh` prints `kubeharness leaks: 0` or only clusters that existed in `*.leaks-before.txt`.

## Gotchas

- First Kind run pulls the node image; budget at least several minutes beyond cluster create.
- `TestNewOwnedClusterIsUsableAndDisposed` may create a decoy cluster named `kind`. Do not delete it as a "leak"; it is not `kh-*`.
- Stopping `go test` with Ctrl-C skips cleanup. Run `scripts/cleanup.sh` afterwards.
- `go test ./...` without `-tags e2e` does not exercise this feature on real Kind.
