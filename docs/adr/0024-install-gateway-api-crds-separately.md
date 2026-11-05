# 24. Install Gateway API CRDs separately

Date: 2026-11-05

## Status

Accepted. Enforced since S3 slice 1 by the recipe invariant tests in `internal/profile` ([29](0029-render-profile-manifests-offline-and-embed-them.md)).

## Context

Gateway API controllers pin different Gateway API versions. [agentgateway v1.0](https://agentgateway.dev/blog/2026-03-12-agentgateway-v1.0/) depends on Gateway API 1.5.0. Envoy Gateway 1.7.0 required 1.4.1. Controller Helm charts can install the CRDs themselves. If they do, the cluster's Gateway API version depends on which chart ran, not on the profile's pinned version matrix ([14](0014-pin-profile-versions.md)).

## Decision

The harness installs the Gateway API CRDs as their own pinned layer, before any controller. A controller chart never installs them.

## Consequences

Each profile's version matrix names a Gateway API version next to its controller version.

The harness must turn off CRD installation in each controller chart, or install from a CRD-free chart where one exists.
