# 23. Reject kgateway for the gateway-api profile

Date: 2026-11-05

## Status

Accepted. Confirms [6. Envoy Gateway as the default Gateway API controller](0006-envoy-gateway-as-the-default-gateway-api-controller.md).

## Context

S3 installs a Gateway API controller in every owned cluster for the `gateway-api` profile. [kgateway](https://kgateway.dev/) is an Envoy-based Gateway API controller and CNCF sandbox project that began as Solo.io's Gloo. It was worth evaluating because, up to agentgateway 2.2 on Kubernetes, kgateway also ran the agentgateway data plane. One control plane could then have served both `gateway-api` and the next profile, `gateway-agentgateway`.

That is no longer true. The [kgateway README](https://github.com/kgateway-dev/kgateway) says: "Starting with version 2.3.0, the control plane for agentgateway has been migrated to the agentgateway repo." The [agentgateway 2.2.x release notes](https://agentgateway.dev/docs/kubernetes/2.2.x/reference/release-notes/) call 2.2 the last release on the kgateway control plane. [agentgateway v1.0.0](https://agentgateway.dev/blog/2026-03-12-agentgateway-v1.0/) ships its own controller image and Helm charts.

The P2 `gateway-agent-router` profile extends Envoy Gateway ([8](0008-agent-router-extends-envoy-gateway.md)). Experiments G01 to G03 use only standard Gateway API resources.

## Decision

Keep Envoy Gateway as the `gateway-api` controller. Do not adopt kgateway.

## Consequences

`gateway-agentgateway` installs agentgateway's own controller whatever P0 uses ([26](0026-agentgateway-uses-its-own-controller.md)), so this choice costs P1 nothing.

`gateway-api` is the base that `gateway-agent-router` builds on, so P0 and P2 share one controller to pin and debug.

kgateway features beyond standard Gateway API stay out of reach. Revisit only if an experiment needs one.
