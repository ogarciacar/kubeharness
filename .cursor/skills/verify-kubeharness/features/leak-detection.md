# Leak detection

Leak detection lists leftover Kind clusters labeled for kubeharness so a developer can see what Ctrl-C or a broken cleanup left behind, without deleting anything. The verification cleanup helper removes only `kh-*` clusters when a drive strands them.

## Sub-features

- `leaks-zero` reports `kubeharness leaks: 0` when no matching containers exist.
- `leaks-list` prints one line per matching container and exits `1` when any exist.
- `leaks-bad` exits `2` when Docker cannot list containers or the pattern is invalid.
- `cleanup-kh` removes stranded `kh-*` clusters without touching evidence or foreign clusters.

## How to get to it (user POV)

- Run `hack/leaks.sh` or `hack/leaks.sh '<regex>'` from the repo root.
- After a stranded verification drive, run `.cursor/skills/verify-kubeharness/scripts/cleanup.sh`.

## Driving it with drive.sh

Preconditions:

- Docker answers `docker info` (doctor covers this).
- For the zero case, either no `kh-*` clusters exist or record the before snapshot and assert the after snapshot did not grow because of this drive.

- **Zero leaks.** Run `hack/leaks.sh > .cursor/skills/verify-kubeharness/artifacts/manual-leaks-zero.txt 2>&1; echo exit:$? >> .cursor/skills/verify-kubeharness/artifacts/manual-leaks-zero.txt` after cleanup. Exit code `0` and the file contains `kubeharness leaks: 0`.
- **Doctor path.** Run `.cursor/skills/verify-kubeharness/scripts/doctor.sh` and confirm it prints the leaks line (or a WARN if leftovers exist) and ends with `ready`.
- **Cleanup refuses foreign names.** Run `.cursor/skills/verify-kubeharness/scripts/cleanup.sh kind` and expect exit `2` with `cleanup refuses non-kh cluster`.
- **Proof.** Keep the captured stdout files under `artifacts/`. Cleanup must not delete those files.

## Gotchas

- `hack/leaks.sh` never deletes; exit `1` means "leaks found", not script failure.
- Default pattern is `^kh-`. A decoy cluster named `kind` is intentionally invisible to the default pattern.
- `hack/screen.sh` repeats e2e and tallies leaks; it is soak tooling, not required for a single-feature proof.
