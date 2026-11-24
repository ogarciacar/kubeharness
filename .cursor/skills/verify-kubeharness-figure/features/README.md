# kubeharness figure verification map

This directory is the maintained source for verifying what a reader sees in the kubeharness test-bench figure (`docs/figure/kubeharness.html`). Read this index before driving, then use the matching feature file as the recipe.

## Baseline preconditions

- Work from the repository root.
- `.cursor/skills/verify-kubeharness-figure/scripts/doctor.sh` exits 0. That also means both pages were just rebuilt.
- Evidence goes in `.cursor/skills/verify-kubeharness-figure/artifacts/<run-id>/`, created by `drive.sh` or `record.sh`.
- Drive only the built file in `docs/figure/`. Never point a journey at a page served by someone else.

## Driving conventions

- Run every journey with `scripts/drive.sh <feature-id> [--light] journeys/<feature-id>.json`. The `replay` and `replay-failing` journeys take `--replay` after `--light`. In the feature files `scripts/…` and `journeys/…` are relative to the skill directory. Run those commands from `.cursor/skills/verify-kubeharness-figure/`, or from the repository root with that prefix on both paths, as `SKILL.md` writes them.
- Select by clicking `button[data-profile="gateway-api"|"gateway-agentgateway"]` and `button[data-scenario="G01"|"G02"|"G03"|"A01"|"K01"]`. A click ends the tour and restarts the run from `new`.
- Wait on `.kh-stage` `data-step` or `data-phase` with `until`. Never use a fixed sleep longer than one step.
- Treat `[data-say]` and `[data-report]` text as literal. It comes from `docs/figure/model.mjs`.
- After a failed drive, run `scripts/cleanup.sh`, keep the artifacts, rerun doctor, then retry.

## Proof and skip reporting

- Each proof has the journey log (the `eval` values), the named PNGs, and the meta file.
- Read the PNGs. A state attribute that flipped while the drawing didn't is a failure.
- For motion (install, cleanup, the G03 flips), add a `record.sh` video or a `sheet` contact sheet.
- Report an unreachable path with the attempted command and the unmet precondition.
- Do not report a frozen `?step=` still as proof of a feature that the map drives through the buttons.

## Feature entry contract

Each feature file starts with an H1 title and one paragraph describing what the reader sees. It then has exactly four H2 sections in this order:

1. `Sub-features`
2. `How to get to it (user POV)`
3. `Driving it with drive.sh`, which starts with `Preconditions:`
4. `Gotchas`

## Features

- [Lifecycle](./lifecycle.md) covers the lamp sequence, the cartridge install, cleanup, and the report line.
- [Profile switch](./profile-switch.md) covers the Envoy Gateway and agentgateway cartridges, and A01 and K01 forcing agentgateway.
- [G01 routing](./g01-routing.md) covers host and path routing to `orders` and `payments`, and requests with no matching rule.
- [G02 ReferenceGrant](./g02-referencegrant.md) covers denied, granted and revoked access across namespaces at the gate valve.
- [G03 weight flip](./g03-weight-flip.md) covers the blue, green, blue flips under steady traffic.
- [A01 MCP](./a01-mcp.md) covers the MCP port, `tools/list`, and `sum(40,2) = 42` on the calculator display.
- [K01 models](./k01-models.md) covers the three model lamps and the display tag on the kronk post, `/m/unknown/` stopping at the diverter, the crossed route, and the one-backend bench.
- [Replay](./replay.md) covers the replay page: real recorded G01, G02, G03, A01 and K01 runs with the real cluster name, Pods, object names, lever, gate, MCP port, model lamps, display, pulses and report line at their recorded times.
