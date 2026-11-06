# Install Gateway

Install Gateway installs pinned Gateway API CRDs and Envoy Gateway into the owned cluster, waits until GatewayClass `kubeharness` is Accepted, and records `profile=gateway-api` on the report line. A second call with the same profile is a no-op; a different profile fails the test. Sending HTTP through a Gateway from the test is not implemented yet.

## Sub-features

- `install-envoy` reaches Accepted GatewayClass `kubeharness` and returns a controller handle.
- `install-report` adds `profile=gateway-api` and an install duration to the report line.
- `install-idempotent` second same-profile call installs nothing further.
- `install-fail-evidence` a broken controller image fails the test with controller evidence.

## How to get to it (user POV)

- Call `h.InstallGateway(kubeharness.EnvoyGateway())` after `kubeharness.New(t)`.
- Run e2e tests `TestInstallGateway` and `TestInstallGatewayFailureCarriesControllerEvidence`.

## Driving it with drive.sh

Preconditions:

- Doctor is ready.
- Cluster can pull the pinned Envoy Gateway images (network to the registry).

- **Install succeeds.** Run `.cursor/skills/verify-kubeharness/scripts/drive.sh install-gateway -tags e2e -count=1 -timeout 25m -v -run '^TestInstallGateway$' ./e2e`. Exit code `0`, log contains `--- PASS: TestInstallGateway` and a report line with `profile=gateway-api`.
- **Failure evidence.** Run `.cursor/skills/verify-kubeharness/scripts/drive.sh install-gateway-fail -tags e2e -count=1 -timeout 25m -v -run '^TestInstallGatewayFailureCarriesControllerEvidence$' ./e2e`. Exit code `0`; helper failure output in the log carries controller evidence.
- **Proof.** Retain the artifact log; confirm post-drive leaks show no new `kh-*` clusters.

## Gotchas

- Install budget is 4 minutes (less if `go test -timeout` ends sooner). Cold image pulls can consume most of that.
- Name Gateways with `gatewayClassName: kubeharness` (constant `kubeharness.GatewayClass`).
- `gc.Gateway("…").Client()` traffic driving is planned only — do not treat its absence as a regression of this feature.
- Unit tests in `gateway_test.go` (no e2e tag) use fakes; they do not prove a real Accepted GatewayClass.
