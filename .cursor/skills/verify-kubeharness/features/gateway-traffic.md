# Gateway traffic

Gateway traffic attaches a programmed Gateway, gives the test an `http.Client` dialed through port-forwards to the Gateway's status address, and polls until an HTTPRoute is ready or until a request/probe check accepts. Use it after `InstallGateway` to prove routing, multi-request sessions (including MCP), and inference routing to one of several models (K01) end-to-end.

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
- Run `TestK01Routing` in `examples/inference` for `AwaitRoute` and `AwaitResponse` through three AgentgatewayBackends that share one kronk Pod (agentgateway only, one cluster).

## Driving it with drive.sh

Preconditions:

- Doctor is ready.
- Docker can build the example backend / calculator images used by those packages.
- For K01, Docker can build `kubeharness.local/kronk-k01:test`. `TestK01Routing` builds it itself with `docker build -q -t kubeharness.local/kronk-k01:test kronk`. The first build pulls the pinned `ghcr.io/ardanlabs/kronk` base image and downloads three GGUF files from huggingface.co, each checked against its sha256, so it needs network and about 3.2 GB of disk. Later builds reuse the cached layers. To pay that cost before the drive, run `docker build -t kubeharness.local/kronk-k01:test examples/inference/kronk`.
- Registry reachable for the chosen profile's controller images.

- **HTTP routing (both profiles).** Run `.cursor/skills/verify-kubeharness/scripts/drive.sh gateway-traffic -tags e2e -count=1 -timeout 30m -v -run '^TestG01Routing$' ./examples/gateway`. Exit code `0`, log contains `--- PASS: TestG01Routing/gateway-api` and `--- PASS: TestG01Routing/gateway-agentgateway`.
- **AwaitProbe (MCP).** Run `.cursor/skills/verify-kubeharness/scripts/drive.sh gateway-traffic-probe -tags e2e -count=1 -timeout 30m -v -run '^TestA01Tools$' ./examples/mcp`. Exit code `0`, log contains `--- PASS: TestA01Tools` and `tools/call sum(40,2)=42`.
- **Inference routing (K01).** Run `.cursor/skills/verify-kubeharness/scripts/drive.sh gateway-traffic-k01 -tags e2e -count=1 -timeout 30m -v -run '^TestK01Routing$' ./examples/inference`. Exit code `0`, and the log contains `--- PASS: TestK01Routing` and these lines in this order:
  - `/m/qwen/ answered as unsloth/Qwen3-0.6B-Q8_0`
  - `/m/smol/ answered as bartowski/SmolLM2-135M-Instruct-Q8_0`
  - `/m/gemma/ answered as ggml-org/gemma-3-270m-it-Q8_0`
  - `/m/unknown/ refused with no model serving`
  - `/m/smol/ answered as unsloth/Qwen3-0.6B-Q8_0 and was rejected as smol` (the crossed route: the request reached Qwen, and the test refused to count it as smol)
  - `/m/smol/ answered as bartowski/SmolLM2-135M-Instruct-Q8_0 again`

  The report line reads `phase=ready cause=ok cleanup=succeeded remaining=0` with `profile=gateway-agentgateway`.
- **Proof.** Artifact logs show the PASS lines; `*.leaks-after.txt` has no new `kh-*` leftovers.

## Gotchas

- Pass `./examples/gateway`, `./examples/mcp` or `./examples/inference` as the package path; `drive.sh` rewrites those nested modules to `go -C <dir> test … .` (same as CI). Do not run them as packages of the root module.
- The per-profile subtests of `TestG01Routing`, `TestG01WrongBackendPort`, `TestG02ReferenceGrant`, and `TestG03WeightFlip` run in parallel, so each drive has two `kh-*` clusters up at once. Both `creating cluster` lines print before either report line, `--- PASS` lines come in finish order, and the parent's own `--- PASS` shows `(0.00s)`. Read each subtest's elapsed time, not the parent's.
- The full `examples/gateway` suite takes about 8.5 minutes on Docker Desktop. Each parallel pair lasts as long as its `gateway-api` subtest. The `*ExplainsItself` tests run their helpers one after the other and take about 2 minutes each, so they are the largest serial cost.
- `TestK01Routing` takes about 70 s on Docker Desktop with the image built, about 25 s of it in `LoadImage`, which copies the 3.2 GB image into the node. It runs one cluster and has no subtests.
- Each alias line names the id that the route's AgentgatewayBackend pins, taken from the reply's `model` field. The test also requires kronk's own `chat_requests_total` counter for that model, and no other, to move, and it fails without that. The log does not print the counter.
- kubeharness does not handle SIGINT or SIGTERM. A drive stopped with Ctrl-C or `kill` leaves its `kh-*` cluster up and prints no report line. Before it creates the cluster, `kubeharness.New` prints `kubeharness: creating cluster kh-…; if this run is killed, remove it with:` followed by a command that lists the cluster's node containers by the label `io.x-k8s.kind.cluster=kh-…` and runs `docker rm -f -v` on each. The line goes to stderr, and `drive.sh` keeps it near the top of the log. Run that command, or `scripts/cleanup.sh kh-…`.
- `AwaitResponse` is for one request; multi-step protocol sessions use `AwaitProbe`.
- Wrong backend port / broken wiring cases are negative proofs — use the dedicated `TestG01Wrong*` / `TestA01Broken*` tests when checking failure evidence, not the happy-path drives above.
