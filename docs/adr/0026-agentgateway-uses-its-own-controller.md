# 26. Agentgateway uses its own controller

Date: 2026-11-05

## Status

Accepted. A direction for S4, not yet backed by code. Refines [7. Agentgateway gets its own profile](0007-agentgateway-gets-its-own-profile.md).

## Context

On Kubernetes, agentgateway 2.2 and earlier ran on the kgateway control plane with controller name `kgateway.dev/agentgateway`. The [2.2.x release notes](https://agentgateway.dev/docs/kubernetes/2.2.x/reference/release-notes/) record the change to `agentgateway.dev/agentgateway`. [agentgateway v1.0.0](https://agentgateway.dev/blog/2026-03-12-agentgateway-v1.0/) ships its own controller as `cr.agentgateway.dev/controller` and its Helm charts as `cr.agentgateway.dev/charts/agentgateway` and `cr.agentgateway.dev/charts/agentgateway-crds`.

## Decision

`gateway-agentgateway` installs agentgateway's own controller, v1.0 or later, with controller name `agentgateway.dev/agentgateway`.

## Consequences

Docs and examples written for the kgateway-based releases do not apply. S4 must translate from the v1.0 controller and its `AgentgatewayPolicy` and `AgentgatewayBackend` resources.

The Gateway API CRDs that agentgateway's charts ship are not used ([24](0024-install-gateway-api-crds-separately.md)).
