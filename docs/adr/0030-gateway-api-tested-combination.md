# 30. Gateway API tested combination

Date: 2026-11-05

## Status

Accepted.

## Context

kind v0.33.0's default node image runs Kubernetes v1.37.0, and every owned cluster uses it ([15](0015-drive-kind-through-its-go-library.md)). Envoy Gateway v1.9's published compatibility matrix covers Kubernetes 1.33 to 1.36. Pinning `kindest/node:v1.36.x` harness-wide would match the matrix but move every test, M01 included, off kind's default.

Gateway API has a standard channel and an experimental channel. Envoy Gateway's own install bundles experimental. G01 to G03 use only standard resources: Gateway, HTTPRoute, and ReferenceGrant.

S3 slice 0 ran G01 with Envoy Gateway v1.9.3 and Gateway API v1.6.1 standard on Kubernetes v1.37.0 in CI and on Docker Desktop, four runs, with no failure.

## Decision

The `gateway-api` profile's tested combination is kind v0.33.0 with its default Kubernetes v1.37.0, Gateway API v1.6.1 standard channel, and Envoy Gateway v1.9.3. Kubernetes 1.37 is outside Envoy Gateway's published range. We keep it while the e2e and example tests pass.

## Consequences

The harness keeps one node image for every profile. If a later Envoy Gateway or Kubernetes change breaks on 1.37, the fallback is pinning `kindest/node:v1.36.x`, which is a harness-wide change and needs its own record.

Tests that need experimental-channel resources or fields cannot run on this profile without a channel change, which is a pin change and a re-render ([29](0029-render-profile-manifests-offline-and-embed-them.md)).
