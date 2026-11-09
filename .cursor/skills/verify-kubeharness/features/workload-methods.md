# Workload methods

Workload methods let a test apply manifests, wait for a Deployment, run a Job to completion, port-forward to a Service on loopback, and load a locally built image into the owned cluster. `WaitReady` and `RunJob` fail the test with Pod evidence when they cannot finish; `Apply`, `Forward`, and `LoadImage` fail with their own errors (no Pod dump).

## Sub-features

- `apply-wait-forward` applies a Deployment, waits until it is available, and GETs through `Forward` on loopback only.
- `run-job` completes a Job and re-runs it so a second Pod executes.
- `load-image` copies a local image for `imagePullPolicy: Never` workloads.
- `job-fail-evidence` stops the test with Job/container logs when a Job fails.

## How to get to it (user POV)

- Call `h.Apply`, `h.WaitReady`, `h.RunJob`, `h.Forward`, and `h.LoadImage` from the test that created `h`.
- Run e2e tests `TestWorkloadMethodsOnRealKind` and `TestFailingJobStopsTheTestWithEvidence`.

## Driving it with drive.sh

Preconditions:

- Doctor is ready.
- Docker can build a small local image (used inside `TestWorkloadMethodsOnRealKind`).

- **Happy path.** Run `.cursor/skills/verify-kubeharness/scripts/drive.sh workload-methods -tags e2e -count=1 -timeout 25m -v -run '^TestWorkloadMethodsOnRealKind$' ./e2e`. Exit code `0` and log contains `--- PASS: TestWorkloadMethodsOnRealKind`.
- **Failed Job evidence.** Run `.cursor/skills/verify-kubeharness/scripts/drive.sh workload-job-fail -tags e2e -count=1 -timeout 20m -v -run '^TestFailingJobStopsTheTestWithEvidence$' ./e2e`. Exit code `0`; the helper failure output retained in the log must show Job failure evidence (not only "did not complete"). Evidence appears when the outer test fails the helper — look for the captured failure text in the log, not a green helper PASS.
- **Proof.** Artifact log shows the PASS lines; `*.leaks-after.txt` has no new `kh-*` leftovers.

## Gotchas

- `WaitReady` is only as strict as the Deployment's readinessProbe.
- Migration Jobs should use `backoffLimit: 0` and `restartPolicy: Never` or failure evidence is poor.
- Forward listens on loopback only; asserting a non-loopback dial failure is part of the e2e test, not an optional check.
- Do not set `ttlSecondsAfterFinished: 0` on Jobs under test — `RunJob` may miss completion.
