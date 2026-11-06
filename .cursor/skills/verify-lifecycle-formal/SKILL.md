---
name: verify-lifecycle-formal
description: Model-check truthful cleanup reporting for kubeharness Env.Start/Close/dispose (ADR 0019). Use when changing internal/lifecycle, harness cleanup, or leak/cleanup report lines.
---

# Verify lifecycle cleanup (formal)

## Property (plain)

A cleanup report must match what a cluster listing shows. `cleanup=succeeded` only when the owned cluster is gone. `cleanup=failed` only when it is still listed. `cleanup=succeeded` never while Create is still in flight.

## Harm if broken

A false `succeeded` hides a leaked Kind cluster. CI and developers treat the test as clean while `kh-*` containers remain. A false `failed`/`uncertain` mix-up also misleads the Docker remove remedy.

## Tool and why

TLA+ with TLC (model checker). The bug class is bad ordering and labeling across Start, abandoned Create, and dispose, not a single-input function. TLC explores those orders on a small model of `internal/lifecycle`. No Go verifier is in use in this repo.

Pinned runner: `hack/tla/run.sh` downloads `tla2tools.jar` **v1.8.0** and checks sha256 `7a0a1e90628ebff2e3e8733375f8b684fa59504d2e02bddc4cb51a7679bb7e04`. Needs Java on `PATH`.

## When this skill applies

Run it when a change touches any of:

- `internal/lifecycle/lifecycle.go` (`Start`, `Close`, `dispose`, `createInFlight`, `Fail`)
- `internal/lifecycle/report.go` (`CleanupOutcome`, `Report`)
- Harness wiring that decides when `Close` runs or how the report line is printed (`harness.go`)

If none of those paths change, skip.

## Coverage record

| Check | Code | Model actions |
| --- | --- | --- |
| Truthful cleanup labels | `dispose` return paths; ADR 0019 | `DisposeStep`, invariants `TruthfulCleanup` |
| No success while Create in flight | `dispose` requires `!inFlight` for success | `NoSuccessWhileCreateInFlight` |
| NameTaken never disposes | `mustDispose=false` on `ErrNameTaken` | `CreateNameTaken`, `PreflightNameTaken`, `DisposeNotNeededAfterDrain` |
| Close once | `closeOnce` | `CloseAlreadyDone` |

**Sync commit:** `91f649fa717094b695e7a0098855b19bcca6c927` (confirmed model matches that tree). Move this forward only after you re-read the covered functions and confirm the model still describes them.

**Bounds:** `MaxDisposeSteps = 3` dispose iterations. One Env, boolean "cluster present", optional foreign cluster flag. No real Docker/Kind, no readiness timing, no gateway profiles, no wall-clock deadlines beyond the step budget.

## How to run

From the repo root:

```bash
./hack/tla/run.sh
```

Expect `hack/tla/run.sh: PASS (Inv holds)` and TLC `No error has been found.`

Read every TLC result with the pstack `/statechart` skill. On a violation it states the shortest path in plain words. On a pass it charts how the system moves between states. Do not copy `/statechart` flags into this skill; they change by release.

## Real bug vs modeling error

- **Real bug:** a counterexample maps to a Go sequence (Start began Create → Close → report `succeeded` while List would still show the name, or `succeeded` while Create has not returned). Fix product code only if the user asked; otherwise report the finding with that sequence.
- **Modeling error:** the counterexample needs a step the Go code cannot take (for example deleting a foreign cluster in one action). Fix `hack/tla/Lifecycle.tla` and re-run. Do not weaken `Inv` to silence it.

## Prove the check can fail

```bash
./hack/tla/run.sh --mutant
```

Expect `PASS (mutant violated Inv as expected)`. The mutant enables `MutantSucceededWithoutList` (`Mutate=TRUE`): Close reports `succeeded` while `clusterPresent` is still true. That is the false-success leak ADR 0019 forbids.

If `--mutant` passes the invariant, the check is broken. Fix the model or `run.sh`, not the mutant flag.

## After an intentional lifecycle change

1. Update `Lifecycle.tla` so each changed Go step still has a matching action.
2. Keep `Mutate=FALSE` in `Lifecycle.cfg`. Re-run `./hack/tla/run.sh`.
3. Re-run `./hack/tla/run.sh --mutant` and confirm it still fails Inv.
4. Only then set **Sync commit** above to `git rev-parse HEAD`.
