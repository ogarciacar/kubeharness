# Install Gateway

Install Gateway installs pinned Gateway API CRDs and a chosen controller profile into the owned cluster, waits until GatewayClass `kubeharness` is Accepted, and records the profile name on the report line (`profile=gateway-api` for Envoy Gateway, `profile=gateway-agentgateway` for Agent Gateway). A second call with the same profile is a no-op; a different profile fails the test. Sending HTTP through a Gateway is a separate feature — see [gateway-traffic.md](./gateway-traffic.md).

## Sub-features

- `install-envoy` reaches Accepted GatewayClass `kubeharness` under `EnvoyGateway()` and returns a controller handle.
- `install-agent` reaches Accepted GatewayClass `kubeharness` under `AgentGateway()`.
- `install-report` adds `profile=…` and an install duration to the report line.
- `install-idempotent` second same-profile call installs nothing further.
- `install-fail-evidence` a broken controller image fails the test with controller evidence.

## How to get to it (user POV)

- Call `h.InstallGateway(kubeharness.EnvoyGateway())` or `h.InstallGateway(kubeharness.AgentGateway())` after `kubeharness.New(t)`.
- Run library e2e tests `TestInstallGateway` and `TestInstallGatewayFailureCarriesControllerEvidence` (Envoy).
- Run `TestG01Routing` under the `gateway-agentgateway` subtest in `examples/gateway` (Agent Gateway install plus traffic).

## Driving it with drive.sh

Preconditions:

- Doctor is ready.
- Cluster can pull the pinned controller images (network to the registry).

- **Envoy install succeeds.** Run `.cursor/skills/verify-kubeharness/scripts/drive.sh install-gateway -tags e2e -count=1 -timeout 25m -v -run '^TestInstallGateway$' ./e2e`. Exit code `0`, log contains `--- PASS: TestInstallGateway` and a report line with `profile=gateway-api`.
- **Agent Gateway install succeeds.** Run `.cursor/skills/verify-kubeharness/scripts/drive.sh install-gateway-agent -tags e2e -count=1 -timeout 25m -v -run '^TestG01Routing$/gateway-agentgateway$' ./examples/gateway`. Exit code `0`, log contains `--- PASS: TestG01Routing/gateway-agentgateway` and `profile=gateway-agentgateway`.
- **Failure evidence.** Run `.cursor/skills/verify-kubeharness/scripts/drive.sh install-gateway-fail -tags e2e -count=1 -timeout 25m -v -run '^TestInstallGatewayFailureCarriesControllerEvidence$' ./e2e`. Exit code `0`; helper failure output in the log carries controller evidence.
- **Proof.** Retain the artifact log; confirm post-drive leaks show no new `kh-*` clusters.

## Gotchas

- Install budget is 4 minutes (less if `go test -timeout` ends sooner). Cold image pulls can consume most of that.
- Name Gateways with `gatewayClassName: kubeharness` (constant `kubeharness.GatewayClass`).
- A cluster holds one profile; switching from Envoy to Agent Gateway (or the reverse) in the same `Harness` fails the test.
- Unit tests in `gateway_test.go` (no e2e tag) use fakes; they do not prove a real Accepted GatewayClass.
