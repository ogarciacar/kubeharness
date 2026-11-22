# Fig 1: the kubeharness test bench

An interactive isometric figure that explains kubeharness as a physical test bench:

- The base plate is the Kind cluster.
- The cartridge in the socket is the gateway profile.
- The diverter's lever is the HTTPRoute rule.
- The gate valve is the ReferenceGrant.
- The lamps show the lifecycle: the five report phases (`new`, `validating`, `provisioning`, `wait-usable`, `ready`), then `test` and `cleanup`.

Every label and number comes from [`model.mjs`](model.mjs), which names the file each value was read from.

## Build and check

Needs Node.js 20.10 or later. The checks also need Chrome or Chromium.

```sh
node build.mjs            # writes kubeharness.html
node build.mjs --light    # writes kubeharness-light.html
node build.mjs --audit    # geometry audit; must end "audit passed"
node tools/lines.mjs kubeharness.html --out shots/lines.png   # must print "line ends: 0"
./shoot.sh                # every profile × scenario, both themes, into shots/
```

`kit/` and `tools/` are copied from the Anatomy skill (MIT, see `kit/README.md`).

## Drive it from a script

URL parameters freeze the figure on one step, so screenshots are deterministic:

```
kubeharness.html?profile=gateway-api&scenario=G02&step=8
```

- `profile`: `gateway-api` or `gateway-agentgateway`.
- `scenario`: `G01`, `G02`, `G03` or `A01`. A01 switches the profile to agentgateway.
- `step`: an index into the run. Steps 0–5 are the lifecycle and the profile install, then come the scenario's steps, then cleanup.
- Add `still` without `step` to freeze the first step.
- Add `manual` to turn off the animation loop. Then `kubeharnessFigure.advance(seconds)` steps the timeline by exactly that much figure time, which is how recordings get a steady frame rate.

Leave out all parameters and the figure runs a tour of every combination.

In the page, `window.kubeharnessFigure` exposes:

| Call | Does |
| --- | --- |
| `select(profile, scenario, { step })` | Starts a run. With `step` it freezes on that step. |
| `tour()` | Starts the tour. |
| `steps()` | Returns `[{ index, lamp, say, gate, lever }]` for the current run. |
| `state()` | Returns `{ profile, scenario, step, phase, say, report, lever, touring }`. `lever` is `a`, `b` or `none`. |
| `advance(seconds)` | With `?manual`, moves the timeline by `seconds` and returns `state()`. |
| `runSeconds()` | Figure seconds in the current run, from `new` to lamps off. On the replay page this follows the time map and the URL parameters. |

The stage element (`.kh-stage`) carries `data-profile`, `data-scenario`, `data-step` and `data-phase`. A driver can wait on these, for example with `tools/drive.mjs` and `["until", "document.querySelector('.kh-stage').dataset.phase==='cleanup'"]`.

Keys: `←` and `→` change the scenario, and `p` switches the profile.

## Replay a recorded run

A kubeharness e2e run with `-artifacts` leaves a trace, `kubeharness-trace-<cluster>.jsonl`, in each test's artifact directory (schema in [`trace-design.md`](trace-design.md)). `traces/` holds one recorded run per test and profile, named `<scenario>-<profile>.jsonl`: `TestG01Routing`, `TestG02ReferenceGrant` and `TestG03WeightFlip` under both profiles, and `TestA01Tools` under agentgateway.

```sh
node build.mjs --traces traces            # writes kubeharness-replay.html
node build.mjs --traces traces --light    # writes kubeharness-replay-light.html
node tools/replay-check.mjs [dir]         # must print "replay check passed"
```

`--traces` takes a directory (every `*.jsonl` under it) or one file. [`replay.mjs`](replay.mjs) validates each line and fails with `file:line` on an unknown `ev` or a bad field. It then turns the events into the steps `live.js` plays, at their recorded times. The bench facts it needs are `REPLAY` in `model.mjs`, one entry per scenario with the same fields, each citing its testdata file: the test name, which backend stands on which post, the paths each rule sends, the route and path the gate valve gates (G02 only; without one the valve stays open), the weighted route and the post each applied directory selects (G03 only), the MCP path and how to read its answers (A01 only), the bench part of each object, the primary kind of each part, and the body's time map. The reducer reads those tables and has no per-scenario branch. The plate shows the trace's cluster name, the bottom-right corner the running profile's pinned versions and the Kind cluster, and the report line at the end is the one the test logged. Only scenarios with a trace can be picked.

What each scenario moves:
- **G01.** Each request turns the lever to the rule it matched and its pulse lights the post whose backend answered. The unrouted and unknown-host requests stop at the diverter.
- **G02.** The valve follows the `shop/pay` route's `ResolvedRefs`. A non-2xx on `/payments` stops at the valve.
- **G03.** An `h.Apply` of `testdata/g03-route/<blue|green>` arms that directory's weights, and the lever moves when the next `AwaitRoute shop/checkout` reports done. Requests never move the lever. Every pulse of the 50 ms stream goes to the post whose body named its backend, so traffic that still reaches the old backend shows.
- **A01.** A response to `/mcp` names no method, so its body head's result field says which call it answers (`protocolVersion`, `tools`, `content`). The MCP port lights on each MCP request until `AwaitProbe` returns, and the display shows the `tools/call` text, `42`.

The replay card is wider than the scripted one. Its left panel is the figure as on the scripted page. Its right panel lists the Kubernetes objects the trace names, grouped by the bench part they stand on, and a numbered pin on each part matches its group. Only the run's own parts get a pin, numbered in pin order, and the tray shows only when the run lists it. Each group is headed by its primary object as `<Kind> <namespace/name>`, the kind the scenario's `kinds` gives the part. The other objects on the part are its rows. For G02:

1. `Gateway shop/edge` (pin on the cartridge): the controller Pod, the proxy's Service and Pod, and a count of the objects the profile install applied (`+40 install objects`).
2. `HTTPRoute shop/pay` (the diverter): Namespace `shop`.
3. `ReferenceGrant payments/payments` (the gate valve).
4. `Service shop/orders` (post a): its Pod.
5. `Service payments/payments` (post b): its Pod.
6. `Namespace payments` (the tray).

G01 has four groups, `Gateway default/edge`, `HTTPRoute default/shop` (with `HTTPRoute default/payments` as a row), `Service default/orders` and `Service default/payments`. G03 has `Gateway shop/edge`, `HTTPRoute shop/checkout`, `Service shop/blue` and `Service shop/green`. A01 has three, `Gateway default/edge`, `HTTPRoute default/mcp` and `AgentgatewayBackend default/calculator`, whose rows are the calculator's Service and Pod.

`objectsOf(events, facts)` in `replay.mjs` builds this registry, one `{ id, kind, name, api?, part, from, to }` per object. `from` and `to` are trace seconds from the object's own events (applied or Ready, then gone). `api` is the apiVersion the object's `object` or `pod` lines carry; an object only a `forward` names, the install count, and every object of a trace from before the field have none. The part comes from the scenario's `parts`, `owners` and `calls` in `model.mjs`. Objects with no part, such as the backends' Deployments, are left out. Each step carries `present`, `gone` and `touched` ids. A step touches what its own events name: an applied, deleted or Ready object, a forward's Service and Pod, every object on the post a request reaches, the route a wait observes, and the grant and gated backend when the valve changes. A row is hidden until its object exists, muted while it exists, bright while touched, and struck through with `gone` once deleted. `groupsOf(objects, facts)` picks each group's heading, and the heading takes the same states, except that before its object exists it shows the kind alone, muted. A pin and its part's outline light while any of the part's objects is touched. Each row and heading shows its `api` beside the name, smaller, or on the next line when it does not fit. Below the lists, CHECKS has one row per `Await*` call the test made, from `checksOf(events)`: `AwaitRoute shop/pay`, `AwaitResponse GET /orders` (the host dropped) and so on, in call order. Each row's box is empty while the call is open or still to come, and fills when the call returns: green, or red when the return's `failed` says that call failed the test. The lamp row fills as the run goes: every lamp before the lit one stays dim-lit, the whole row stays filled after cleanup, and the lamp a failing run stopped at (`stoppedAt`: `test` for `test-failed`, else the report's `phase`) turns red. The badge beside the title reads `RUNNING` until the report event, then `PASS` when the report's `cause` is `ok` and `FAIL` for any other cause (`verdictOf`). `cause` is `test-failed` whenever `t.Failed()` is true at cleanup, so a failed `t.Errorf` in the test's own code turns the badge red even though no box names it. The bottom-right corner shows the running profile's `PROFILES.<profile>.pins` and `CLUSTER` from `model.mjs`, and `live.js` rewrites the pins on every profile change. Every row is laid out from the start, so the card keeps its height between steps.

Timing comes from a time map that `replay.mjs` derives from each trace. Setup, from the start until the test's `Gateway` call returns, is 38 to 59 s of cluster create, node wait, profile install and Gateway wait, so it plays 10 times faster. Each setup step holds `real gap × 0.1` seconds, except that a step that lights a new lamp holds at least 0.6 s (`phaseHold`), so `new`, `validating`, `provisioning`, `wait-usable`, `ready` and `test` each stay lit long enough to see. A lamp change keeps its own step even when the trace records it in the same millisecond as the next event, as `ready` and `InstallGateway`'s call often are. Setup now lasts 6.8 to 8.9 s per run, about 5 s of it before the `test` lamp. Cleanup, from the `Cleanup` call, plays at real time. The test body between them plays at the scenario's `body` rate. In G01, G02 and A01 the body lasts well under a second, so it plays 20 times slower. Each body step holds `max(real gap × rate, hold)` seconds, and pulse offsets inside a step scale by the same rate. G03's body is about 10 s of traffic every 50 ms, which at 20 times slower would last over 3 minutes and draw 20 pulses a second. It plays at real time (`rate: 1`) with the same 1.2 s minimum hold and draws at most 4 pulses a second (`cap: 4`). No body step holds more than 12 s (`maxHold`): a longer one plays at the slower rate that fits it in 12 s. No passing trace has one, since the longest is a 10.2 s G02 route poll, but a wait that ran to its timeout would otherwise play for 20 minutes, as the failing run in [`traces-failing/`](traces-failing/README.md) does. The say line keeps the full count of each run of identical responses (`×N`). The stage's `data-clock` keeps the trace's clock (`gateway-api · G02 · test · t+52.84s · slowed ×20`) and names the rate (`fast ×10` in setup, `slowed ×20` or `×1` in the body, `×1` in cleanup); the card no longer shows it. The top-right corner shows the replay's own elapsed time instead, `mm:ss.mmm` of video time from the run's start, so a stretch where nothing moves still shows time passing; under `?manual` it equals the seconds passed to `advance()`.

- `?setup=N` plays setup `N` times faster. The default is 10. `?setup=1` restores real time with no phase hold.
- `?body=N` plays the body `N` times slower, overriding the scenario's rate.
- `?hold=S` holds each body step at least `S` seconds. The default is 1.2.
- `?speed=S` divides every hold, setup and cleanup included. The default is 1.
