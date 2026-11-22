# Gateway traffic

Gateway traffic attaches a programmed Gateway, gives the test an `http.Client` dialed through port-forwards to the Gateway's status address, and polls until an HTTPRoute is ready or until a request/probe check accepts. Use it after `InstallGateway` to prove routing and multi-request sessions (including MCP) end-to-end.

## Sub-features

- `gateway-attach` returns a `*Gateway` once the named Gateway is Accepted, Programmed, and forwarded on loopback.
- `client` returns `gw.Client()` for host/path routing checks through that Gateway.
- `await-route` polls an HTTPRoute until a check (typically `kubeharness.RouteReady`) accepts.
- `await-response` retries one HTTP request until a response check accepts.
- `await-probe` retries a multi-request probe (fresh client per attempt) until the probe returns nil.

## How to get to it (user POV)

- After `InstallGateway`, call `gc.Gateway("edge")`, then `gw.AwaitRoute`, `gw.AwaitResponse` / `gw.AwaitProbe`, and optionally `gw.Client()`.
- Run `TestG01Routing` in `examples/gateway` (both `gateway-api` and `gateway-agentgateway` subtests).
- Run `TestA01Tools` in `examples/mcp` for `AwaitProbe` through an AgentgatewayBackend.

## Driving it with drive.sh

Preconditions:

- Doctor is ready.
- Docker can build the example backend / calculator images used by those packages.
- Registry reachable for the chosen profile's controller images.

- **HTTP routing (both profiles).** Run `.cursor/skills/verify-kubeharness/scripts/drive.sh gateway-traffic -tags e2e -count=1 -timeout 30m -v -run '^TestG01Routing$' ./examples/gateway`. Exit code `0`, log contains `--- PASS: TestG01Routing/gateway-api` and `--- PASS: TestG01Routing/gateway-agentgateway`.
- **AwaitProbe (MCP).** Run `.cursor/skills/verify-kubeharness/scripts/drive.sh gateway-traffic-probe -tags e2e -count=1 -timeout 30m -v -run '^TestA01Tools$' ./examples/mcp`. Exit code `0`, log contains `--- PASS: TestA01Tools` and `tools/call sum(40,2)=42`.
- **Proof.** Artifact logs show the PASS lines; `*.leaks-after.txt` has no new `kh-*` leftovers.

## Gotchas

- Pass `./examples/gateway` or `./examples/mcp` as the package path; `drive.sh` rewrites those nested modules to `go -C <dir> test … .` (same as CI). Do not run them as packages of the root module.
- The per-profile subtests of `TestG01Routing`, `TestG01WrongBackendPort`, `TestG02ReferenceGrant`, and `TestG03WeightFlip` run in parallel, so each drive has two `kh-*` clusters up at once. Both `creating cluster` lines print before either report line, `--- PASS` lines come in finish order, and the parent's own `--- PASS` shows `(0.00s)`. Read each subtest's elapsed time, not the parent's.
- The full `examples/gateway` suite takes about 8.5 minutes on Docker Desktop. Each parallel pair lasts as long as its `gateway-api` subtest. The `*ExplainsItself` tests run their helpers one after the other and take about 2 minutes each, so they are the largest serial cost.
- `AwaitResponse` is for one request; multi-step protocol sessions use `AwaitProbe`.
- Wrong backend port / broken wiring cases are negative proofs — use the dedicated `TestG01Wrong*` / `TestA01Broken*` tests when checking failure evidence, not the happy-path drives above.
