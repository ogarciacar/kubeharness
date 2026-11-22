# Design: replay a real e2e run in the figure

Status: implemented. The figure side is `replay.mjs`, `build.mjs --traces` and `live.js`. This design was written for G02; the replay now also plays G01, G03 and A01 from the same schema, through per-scenario tables in `model.mjs` `REPLAY` (see `README.md` "Replay a recorded run").

## Problem

The goal is a video of what one real `go test -tags e2e` run of `examples/gateway` `TestG02ReferenceGrant` does to its cluster. It should show the real cluster name, phases, profile install, Pods, route conditions, requests, the ReferenceGrant apply and delete, and cleanup, all at real relative times. Today the figure plays scripted steps from `docs/figure/model.mjs`.

Constraints:
- **No exported API change.**
- **No secrets in the trace.** That covers kubeconfig contents and tokens.
- **No slowdown.** The test must not slow down measurably.
- **One trace per profile.** There are two profile subtests, each with its own cluster.
- **`t.Log` doesn't carry timings.** `testing` buffers it until the test ends (`harness.go:102`), so the test log can't be the trace.
- **Text in the drawing.** The figure allows one engraved label and text a machine shows in its own window.

## Usage (caller's view)

```sh
cd examples/gateway
go test -tags e2e -count=1 -run '^TestG02ReferenceGrant$' -v -artifacts
# === ARTIFACTS TestG02ReferenceGrant/gateway-api .../_artifacts/TestG02ReferenceGrant__gateway-api/<n>
# each directory holds kubeharness-trace-<cluster>.jsonl

cd ../../docs/figure
node build.mjs --traces ../../examples/gateway/_artifacts      # writes kubeharness-replay.html
node build.mjs --traces ../../examples/gateway/_artifacts --light
../../.cursor/skills/verify-kubeharness-figure/scripts/record.sh gateway-api G02 --replay [--speed 2]
```

Every Harness writes its trace into `t.ArtifactDir()`. Go's own `-artifacts` flag decides whether the file survives the test. Without the flag, Go deletes the directory when the test ends. kubeharness adds no env var, no build tag and no flag, and the test code doesn't change.

## Trace format, schema v1

The file is JSONL, one event per line. Every line has `at` (integer ms since the trace started, monotonic, stamped under the writer's lock) and `ev`. The first line is always `{"ev":"trace","v":1,...}`. Each line is one unbuffered write, so a killed or hung run still leaves a valid file that stops at the call that hung.

| `ev` | Written by | Fields | Meaning |
| --- | --- | --- | --- |
| `trace` | `newHarness` | `v`, `test`, `cluster`, `start` | Header. `test` is `t.Name()`, and `start` is the wall clock in RFC 3339 UTC. |
| `call` / `return` | every exported cluster method, and New's cleanup | `id`, `fn`, `arg`? / `id`, `failed`? | The test called `fn(arg)`, and later it returned. |
| `phase` | `lifecycle.Env.enter` (the only writer of `report.Phase`) | `phase` | `validating`, `provisioning`, `wait-usable`, `ready`. |
| `wait` | each wait loop | `in`, `on`, `subject`, `state`?, `done`? | A changed observation. `state` is the loop's existing "last observed" text. A line is written only when `(state, done)` changes. |
| `object` | `kube.Apply`, `kube.Delete` | `in`, `api`?, `obj`, `state` | The object was `applied`, is `deleting`, or is `gone`. `api` is its apiVersion, for example `gateway.networking.k8s.io/v1beta1` for a ReferenceGrant or `v1` for a Service. |
| `pod` | `WaitDeployment` once the rollout is done; `StartForward` | `in`, `api`?, `owner`, `pod`, `ready` | A Pod behind a Deployment the test waited on, or the Pod a forward picked. `api` is the Pod's apiVersion, `v1`. *(Grafted from candidate 2.)* |
| `forward` | `kube.StartForward` | `in`?, `svc`, `port`, `pod`, `local` | A port-forward is up. |
| `request` / `response` / `body` | one RoundTripper in `Gateway.Client()` and in each await attempt | `id`, `in`?, `via`, `method`, `url` / `id`, `status`?, `err`?, `ms` / `id`, `head` | One HTTP exchange through the Gateway. `url` is `scheme://host/path` only. `head` holds the first 200 bytes the caller read. |
| `report` | New's cleanup | `in`, `line` | The report line, the same string `t.Log` prints. |

`api` is optional so that traces written before it still parse. The figure shows it beside each object's kind, and an object no line gives an `api` to, such as a Service only a `forward` names, shows none.

`on` is one of `usable`, `crds`, `rollout`, `class`, `job`, `gateway`, `route`, `response` or `probe`, and each value has exactly one emitter. Calls and requests share one id counter, and `in` is the id of the enclosing call, carried on the `ctx`.

What never enters the trace: the kubeconfig path or contents, headers, query strings, request bodies, and failure messages (Pod logs travel inside those). `internal/trace` is the only writer and enforces this. Free text is collapsed to one line and capped at 200 characters.

An illustrative excerpt (not recorded) is in `/tmp/arena-trace/candidate-1/sketch/g02-gateway-api.excerpt.jsonl`.

## Shape

- **`internal/trace`.** Uses only the standard library, so `lifecycle` can import it. A `*Scope` travels on the `ctx`, and `h.op` is the one place that attaches it. The `kube`, `profile` and `lifecycle` signatures stay as they are. Every method is a no-op on a nil value, so unit tests and the fake provider change nothing. *(Sketch: `/tmp/arena-trace/candidate-1/sketch/internal/trace/`. Emit points: `sketch/hooks.go`.)*
- **Pods.** When `WaitDeployment` finishes, it lists the Deployment's Pods once by selector and writes a `pod` line per Pod. That is one API call per wait, and it names the `orders`, `payments` and controller Pods. `StartForward` names the proxy Pod.
- **Requests.** A recording RoundTripper wraps the transport in `Gateway.Client()` and in each await attempt's clone. The base `*http.Transport` stays intact, so `transportForAttempt` can still clone it. The burst of 5 and every `AwaitResponse` retry each become their own request/response pair.
- **Figure.** `docs/figure/replay.mjs` is a pure reducer from trace events to the step list that `live.js` already plays. Steps gain `at`, `report`, `plate` and `pulses: [{to, delay}]`, with exact offsets inside a step (grafted from candidate 2). `build.mjs --traces` validates the trace and inlines it into `kubeharness-replay.html`. `live.js` carries leftover time across steps, so short steps don't drift. A time map from the trace plays setup and cleanup at real time and the test body, from `Gateway`'s return to `Cleanup`'s call, 20 times slower with at least 1.2 s per step (`?body=N`, `?hold=S`). A `?speed=S` parameter divides every hold, and the default is 1.
- **Keeping both sides in step.** `internal/trace/testdata/golden.jsonl` holds one line per event kind. The figure doctor parses it, so a new Go event kind fails the figure check until `replay.mjs` handles it.

What the bench shows:

| Event | Bench part | Text |
| --- | --- | --- |
| `trace` | Engraved plate | The real cluster name, `kh-…`. This is the one label. |
| `phase` | Lamps `new` to `ready` at real times | `[data-say]` reads `kubeharness.New(t) · phase=…` |
| `call`/`return InstallGateway` | Cartridge appears, then seats | `h.InstallGateway(…) · GatewayClass kubeharness Accepted` |
| `return New` | Lamp `test` | |
| `pod` ready | Post lamp turns steady dim | `Pod payments/payments-… Ready` |
| `wait on=route` for `shop/pay` | Gate valve follows `ResolvedRefs` (open on True, closed on False) | `route shop/pay ResolvedRefs=False reason=RefNotPermitted` |
| `object ReferenceGrant/...` | Nothing moves by itself | `h.Apply(testdata/g02-grant) · ReferenceGrant payments/payments applied` |
| `request`/`response` | Pulse at its real time: 2xx from a backend goes to that post; a non-2xx on the gated line stops at the valve; anything else stops at the diverter | `GET http://shop.example/payments → 500 ×5` |
| `call Cleanup`, `report` | Lamp `cleanup`, cartridge lifts, then lamps off | The report line, verbatim |
| (readout, outside the drawing) | | `gateway-api · G02 · test · t+51.4s` |

## Synthesis decision

The arena ran two runners on different models. The judge step was skipped, because every model in the configured judge pool is a Claude model, the same family as the parent. Picking a different-family judge is the skill's rule.

The base is **candidate 1 (trace from inside the library)**:
- It meets the done criteria with observed facts: every phase, call, wait and request, at real times.
- It has no exported API change and no new knob, and it writes about 40 KB per run (an estimate, to be measured).

**Candidate 2 (outside observer)** leaves the library untouched. It lost on fidelity:
- Four boundaries are reconstructed from report durations (±50 ms).
- It sees requests only through proxy access logs, and nobody has confirmed agentgateway writes them.
- It needs a separate module, 5 goroutines, Docker events and per-profile log parsers.

Grafts from candidate 2:
- Real Pod names, through one List per finished rollout.
- Exact pulse offsets inside a replay step.

Candidate 2 also worried that `go test -json` might drop the test's stderr. A quick experiment on Go 1.27 showed it doesn't.

## Tradeoffs accepted

- We accept a trace written on every e2e run, deleted unless `-artifacts` is set, in exchange for no switch and no code path that only runs when someone remembers it.
- We accept that `Gateway.Client().Transport` becomes a wrapper rather than a `*http.Transport`. Its docs don't promise the concrete type.
- We accept that the test's own payments-counter reads are invisible. They go straight to the `Forward` address, which the library never sees. So the video says `200 from payments`, not `counter rises`.
- We accept state carried in a `ctx` value in exchange for no signature changes across four internal packages.

## Alternatives considered

- **An outside observer (candidate 2).** See the synthesis decision.
- **A kubeharness switch** (`KUBEHARNESS_TRACE`, a build tag, or a flag). It adds a knob and a second mode, when `-artifacts` already exists for this.
- **A wrapper around `gw.Client()` in the test.** It misses New's phases and every `AwaitResponse` attempt, and it edits the test the video claims to show.
- **A cluster-wide Pod watch.** It shows kube-system Pods that have no part on the bench, and costs a goroutine per test.
- **Fetching the trace at runtime (`?trace=`).** Chrome blocks `fetch` from `file://`, so the trace is inlined at build time.

## Decisions for you

1. **Is an always-on trace file acceptable?** It is written on every e2e run, deleted unless `-artifacts` is set. The alternative is an explicit env var.
2. **What should the gate valve follow?** This design makes it follow the route's `ResolvedRefs`, which is what the data plane enforces as the test observes it, and puts the grant apply and delete in the say line. The other option is the ReferenceGrant object itself. Then the real lag after delete could show a pulse passing a closed valve.
3. **When should the `test` lamp light?** This design lights it when `New` returns, which is what really happens. The scripted figure lights it after install.
4. **Commit one recorded trace?** This design commits it under `docs/figure/traces/`, so anyone can rebuild and refilm without Docker.
5. **Add an ADR?** This design adds ADR 0031 recording the trace as a diagnostic artifact with a versioned schema. ADR 0013 rules out a configuration schema, not a diagnostic file.

## Next implementation step

Write `internal/trace` with its golden-file test and a write benchmark, add the emit points, and run G02 once with `-artifacts`. That produces the first real trace. The figure side follows from it.
