---
name: verify-kubeharness-figure
description: Drive the kubeharness test-bench figure (docs/figure, a standalone HTML page) in headless Chrome through window.kubeharnessFigure and docs/figure/tools/drive.mjs. Use it to prove the figure still shows each profile and scenario correctly (lifecycle lamps, cartridge swap, G01 routing, G02 ReferenceGrant gate, G03 weight flip, A01 MCP, K01 model lamps), to prove the replay page plays a recorded e2e trace (docs/figure/traces, replay.mjs) with setup played fast and the test body slowed, to capture screenshots, or to record an MP4 of a run after changing docs/figure or the facts in docs/figure/model.mjs.
---

# Verify the kubeharness figure

The figure is `docs/figure/kubeharness.html`, an isometric test bench that explains kubeharness. A user opens the page and clicks a profile button (`Envoy Gateway`, `agentgateway`) and a scenario button (`G01`, `G02`, `G03`, `A01`, `K01`, `tour`). Each choice plays one run: lifecycle lamps, the profile cartridge dropping into its socket, the scenario's requests as pulses along the lines, then cleanup.

There is no server. The page is a generated static file opened as a `file://` URL. Each drive starts its own headless Chrome with a throwaway profile, so drives never share state with each other or with the user's browser. Two drives can run at once. Only `cleanup.sh` is global, so don't run it while another drive is live.

A second page, `docs/figure/kubeharness-replay.html`, replays recorded e2e runs (`TestG01Routing`, `TestG02ReferenceGrant`, `TestG03WeightFlip` under both profiles, `TestA01Tools` and `TestK01Routing` under agentgateway) from `docs/figure/traces/*.jsonl` instead of scripted steps. `docs/figure/replay.mjs` validates each trace and turns it into steps. Its card is wider. The right panel lists the trace's real object names and API versions under numbered pins on the bench parts, with the profile's pinned versions and the cluster's Kubernetes version below. See [features/replay.md](features/replay.md).

This skill does not verify the kubeharness Go library. For that, use `.cursor/skills/verify-kubeharness/`.

## Launch

Launching means building the pages. From the repository root:

```bash
(cd docs/figure && node build.mjs && node build.mjs --light)
(cd docs/figure && node build.mjs --traces traces && node build.mjs --traces traces --light)
```

The figure is ready when the commands print `wrote kubeharness.html …`, `wrote kubeharness-light.html …`, `wrote kubeharness-replay.html …` and `wrote kubeharness-replay-light.html …`. `--traces` takes a directory (every `*.jsonl` under it) or one file, and fails with `file:line` on any schema error. Needs Node.js 20.10+ and Chrome or Chromium; set `CHROME_PATH` if `drive.mjs` cannot find it. `record.sh` also needs `ffmpeg`.

There is nothing to tear down after a launch. The built HTML files are the figure's own outputs. Rebuild them after any edit to `docs/figure/*.mjs`, `live.js` or `kubeharness.css`.

## Doctor

```bash
.cursor/skills/verify-kubeharness-figure/scripts/doctor.sh
```

Every line must end in `ready`, and the exit code must be 0. Doctor rebuilds both pages, then checks:
- the geometry audit (`node build.mjs --audit` ends `audit passed`);
- the line-end check (`line ends: 0`);
- that the page loads in headless Chrome with `kubeharnessFigure` defined and a clean console;
- `trace golden`: `replay.mjs` parses `internal/trace/testdata/golden.jsonl`, which has one line per event kind, and knows every kind in it, so a kind added or renamed in Go fails here;
- `replay reducer check`: `docs/figure/tools/replay-check.mjs` feeds a real agentgateway sequence through `replay.mjs`, including the proxy-lag burst, then checks every recorded trace: its time map (setup 10 times faster with at least 0.6 s on each new lamp, `?setup=1` at real time, body and cleanup holds as before), its object registry (each object's `api` as its lines carry it) and per-step `present`, `gone` and `touched` sets against its lines, its group headings, the G02 gateway-api panel markup's `v1beta1` beside ReferenceGrant and `v1` beside HTTPRoute, and what its scenario must show (G01 pulses to the posts `g01Routes` names, the G02 valve, the G03 lever blue, green, blue with each pulse at the post that answered, the A01 display `42`, and in K01 one model lamp and its tag for each answer, held to the `route.yaml` the test applied before the request, with no lamp or tag while the test applies a route or waits for it);
- when `docs/figure/traces/*.jsonl` exist, the replay build, audit, line ends and page load, and that each trace's report line is in `journeys/replay.json`.

A missing `ffmpeg` is a WARN. In that case only `record.sh` is unavailable.

Run doctor before the first drive, after any failed drive, and after any edit to `docs/figure`.

## Drive

Each feature in the map has a scripted journey in `journeys/<feature-id>.json`. Run one with:

```bash
.cursor/skills/verify-kubeharness-figure/scripts/drive.sh <feature-id> [--light] [--replay] .cursor/skills/verify-kubeharness-figure/journeys/<feature-id>.json
```

`--replay` drives `kubeharness-replay*.html`. Only the `replay` and `replay-failing` journeys use it.

`drive.sh` runs `docs/figure/tools/drive.mjs` against the built page. It writes shots into the run directory, or into its `light/` subdirectory with `--light`, and prints the run directory at the end. It exits non-zero if any `until` times out or the page throws. Set `RUN_ID=<name>` to group several drives in one directory.

Journeys act the way a user does. They click `button[data-profile="…"]` and `button[data-scenario="…"]`, then wait on the figure's own state. Stable handles, all from `docs/figure`:

| Handle | Meaning |
| --- | --- |
| `.kh-stage` `data-profile`, `data-scenario` | Current selection |
| `.kh-stage` `data-step` | Index into the run. Steps 0–5 are lifecycle and install. The scenario steps start at 6. Then come cleanup and off. |
| `.kh-stage` `data-phase` | Lit lamp: `new`, `validating`, `provisioning`, `wait-usable`, `ready`, `test`, `cleanup`, `off` |
| `[data-say]` | The step's call, for example `route shop/pay ResolvedRefs=False reason=RefNotPermitted` |
| `[data-report]` | The report line. It is complete only at cleanup and off. |
| `[data-lamp="0".."6"][data-on]` | The lit lifecycle lamp |
| `[data-post="postA"]`, `[data-post="postB"]` with `data-on` | A request reached the back or front backend |
| `[data-kh="gate"][data-block]`, `[data-kh="div"][data-block]` | A request stopped at the closed gate valve, or at the diverter (no matching rule) |
| `[data-route="host|fwd|cart|a|b"][data-hot]` | Line chunks a pulse is in |
| `[data-piece="gate"]` `transform` | Gate stem lift. `translate(0.00 -0.00)` is closed. A non-zero y is open. |
| `[data-piece="cart"]` `style.opacity` | `1` means the cartridge is installed and shown |
| `.kh-bench` `data-profile` | Which cartridge top is drawn |
| `.kh-bench` `data-one-backend` | The run has no second backend (`model.mjs` `BACKENDS[scenario].b` is `null`, as in A01 and K01). The front post, line `b`, the diverter's `b` outlet, the gate valve and the tray are hidden. |
| `[data-part="display"]` | The text in the display window on the back post: `42` in A01, and in K01 the tag of the model that answered (`QWEN3`, `SMOL2`, `GEMMA`). The window is wide enough for five characters. |
| `.kh-bench` `data-models` | The run has model lamps: some step carries a `model` slot, as every K01 step does. Without it the three lamps are not drawn. |
| `[data-model="0".."2"][data-on]` | The lit model lamp on the back post, in the order of `model.mjs` `REPLAY.K01.models` (Qwen, SmolLM2, Gemma). At most one is lit. None is lit when no model answered. |
| `[data-part="plate"]` | Replay page only: the engraved cluster name |
| `[data-corner="fig"]`, `[data-corner="hint"]` `innerText` | Plate corners, top left and bottom left. On the scripted pages `Fig 1` and `PICK A PROFILE AND A SCENARIO`. On the replay page `Replay` and the running trace header's `test`, for example `TestG02ReferenceGrant/gateway-api`, set on every profile or scenario change and shown in the trace's own casing. |
| `.kh-names-k` `innerText` | Replay page only: the name panel heading, `K8S OBJECTS` |
| `[data-post]` `data-ready` | Replay page only: the post's Pod is Ready (steady dim lamp) |
| `.kh-groups[data-on] [data-obj="<Kind>/<ns>/<name>"]` `data-state`, `data-touched` | Replay page only: one object in the name panel, a row (`.kh-obj`) or a group heading (`.kh-group-head`). `data-state` is `absent`, `present` or `gone`. `data-touched` marks a bright row. |
| `.kh-groups[data-on] [data-obj="…"] .kh-api` | Replay page only: the object's API version from its trace lines, for example `gateway.networking.k8s.io/v1beta1` on the ReferenceGrant heading. Absent on the install count and on a Service only a port-forward names. |
| `[data-pins]` `textContent`, `[data-corner="readout"]` `innerText` | Replay page only: the bottom-right corner. `[data-pins]` holds only the running profile's pinned versions from `model.mjs` `PROFILES`, rewritten on every profile change. The corner's text also has the labels `PINNED` and `CLUSTER` and the cluster's `kind v0.33.0 · k8s v1.37.0` from `model.mjs` `CLUSTER`. |
| `[data-pin="cart|div|gate|a|b|tray"][data-on]`, `[data-named][data-touched]` | Replay page only: a lit numbered pin, and the lit outline of its part |
| `kubeharnessFigure.steps()` `gate`, `lever` | Whether the valve is open in that step, and where the lever points (`a`, `b`, `none`) |
| `kubeharnessFigure.state()` `lever` | Where the lever points in the current step |
| `.kh-mcp[data-on]` | The MCP port on the agentgateway cartridge is lit (A01) |
| `[data-pin][data-off]` | Replay page only: a pin the run's name panel does not list, hidden |

Write a new journey in the same JSON action format as `drive.mjs`: `click`, `until`, `eval`, `shot`, `wait`, `key`, `sheet`, `lines`. Wait on `data-step` or `data-phase` with `until`, never a fixed sleep. A step lasts 1.4–3 s of real time.

`kubeharnessFigure.select(profile, scenario, { step })` freezes the figure on one step. Use it, or the URL `kubeharness.html?profile=…&scenario=…&step=N`, only for still screenshots. A journey that proves a feature must click the buttons.

### Record a video

```bash
.cursor/skills/verify-kubeharness-figure/scripts/record.sh <profile> <scenario> [--light] [--replay] [--speed S] [--setup N] [--body N] [--hold S] [--fps 24] [--seconds S]
```

For the K01 video, the recorded `TestK01Routing` run with both panels, run:

```bash
.cursor/skills/verify-kubeharness-figure/scripts/record.sh gateway-agentgateway K01 --replay
```

It films the 46.6 s replay in about two minutes and prints the path of `gateway-agentgateway-K01-replay.mp4` in a new run directory under `artifacts/`.

`--replay` films the replay page. `--speed S` adds `?speed=S`, which divides every hold. On the replay page `--setup N` adds `?setup=N` (setup plays `N` times faster, default 10, and `1` restores real time), `--body N` adds `?body=N` (the test body plays `N` times slower; the default is the scenario's own rate, 20 for G01, G02 and A01, 8 for K01 and 1 for G03) and `--hold S` adds `?hold=S` (each body step holds at least `S` seconds, default 1.2). The file name gains `-replay`, `-xS`, `-setupN`, `-bodyN` and `-holdS`.

This films one full run, from `new` to lamps off, at true figure speed. It loads the page with `?manual`, which turns off the animation loop. Each frame then calls `kubeharnessFigure.advance(1/fps)` and screenshots the card (`.iso-plate`, which on the replay page includes the name panel), so a slow capture never changes the timing. It writes three files:
- `<profile>-<scenario>.mp4`, or `<profile>-<scenario>-replay.mp4` with `--replay`. The option suffixes above come next, and `-light` comes last with `--light`;
- `.poster.png`, the frame three quarters through;
- `.phases.txt`, the frame count spent in each phase.

`.phases.txt` should match `docs/figure/model.mjs` (`hold × fps`). A G01 run takes about 20 s of figure time and about 30 s to record. For a replay, setup plays 10 times faster with each new lamp lit at least 0.6 s, cleanup plays at real time, both divided by the speed, and the `test` phase is longer by the slowed body. With the defaults the Envoy Gateway G02 replay is 51.9 s (8.3 s setup, 41.9 s body, 1.7 s cleanup); `features/replay.md` lists every run's length. Check a replay video against its trace with `node docs/figure/tools/phases-check.mjs <trace.jsonl> <scenario> <video.phases.txt> [fps] [setup]`; it reads the setup factor from `-setupN` in the file name when the argument is missing, else 10.

## Evidence

Everything goes in `.cursor/skills/verify-kubeharness-figure/artifacts/<run-id>/`, which git ignores. Per drive there are `<feature>.actions.json`, `<feature>.log` (every `eval` result and shot readout), `<feature>.meta.txt` (commit, dirty count, page hash, theme, time) and the PNGs the journey names. With `--light` the three files are `<feature>-light.*` and the PNGs go in `light/`. `record.sh` writes its three files there too, for example `gateway-agentgateway-K01-replay.mp4`.

Proof standards:
- **Drive the user path.** Click the profile and scenario buttons and let the timeline run. `select({ step })` or a `?step=` URL proves a still frame, not a feature.
- **Capture the action and the result.** Each journey pairs a shot with an `eval` of `[data-say]` or a state handle at the moment the state should hold. Read the PNGs: the log shows that the state flipped, and the shot shows that the drawing matches it.
- **Check the side effect.** For the gate, that means the stem transform and the `data-block` flash. For routing, the post lamp and the hot chunks. For lifecycle, the cartridge opacity and the report line at `off`.
- **Truth.** Every label the scripted figure shows comes from `docs/figure/model.mjs`, and every label the replay page shows comes from a trace in `docs/figure/traces/`. When a drive shows a different string, the fault is in the page, the model or `replay.mjs`, not the journey.
- **Light theme.** Run the same journey with `--light` when a change touches CSS or tones.

## Cleanup

```bash
.cursor/skills/verify-kubeharness-figure/scripts/cleanup.sh
```

This removes scratch that interrupted runs leave behind:
- `.recording-*` scratch frame directories that `record.sh` leaves under `artifacts/` when interrupted;
- headless Chrome processes started by `drive.mjs` (matched by `--user-data-dir=$TMPDIR/iso-drive-*`, never by process name);
- their temporary profiles.

It keeps every MP4, PNG, log and meta file. A normal drive cleans up its own Chrome, so run this after a failed or interrupted drive.

## Helpers

All under `.cursor/skills/verify-kubeharness-figure/scripts/` and executable:

| Script | Invocation |
| --- | --- |
| `doctor.sh` | `scripts/doctor.sh` |
| `drive.sh` | `scripts/drive.sh <feature-id> [--light] [--replay] <actions.json or inline JSON>` |
| `record.sh` | `scripts/record.sh <profile> <scenario> [--light] [--replay] [--speed S] [--setup N] [--body N] [--hold S] [--fps N] [--seconds S]` |
| `replay-journey.mjs` | `node scripts/replay-journey.mjs [traces-dir] [out.json]` rewrites `journeys/replay.json` from `docs/figure/traces`, or `out.json` from `traces-dir` |
| `cleanup.sh` | `scripts/cleanup.sh` |
| `env.sh` | Sourced by the others. It defines `ROOT`, `FIG`, `ARTIFACTS` and `page_url [--light] [query]`. `PAGE=kubeharness-replay` points `page_url` at the replay page. |

Figure-side tools: `docs/figure/shoot.sh` (stills of every profile × scenario in both themes, into `docs/figure/shots/`) and `docs/figure/tools/lines.mjs` (line-end check). `docs/figure/README.md` documents the page API.

## Feature map

See [features/README.md](features/README.md). Each feature file names its journey. A proof that skips an entry point the map lists is incomplete for that feature.
