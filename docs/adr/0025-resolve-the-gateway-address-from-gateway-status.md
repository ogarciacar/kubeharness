# 25. Resolve the gateway address from Gateway status

Date: 2026-11-05

## Status

Accepted. A direction for S3, not yet backed by code.

## Context

Each controller names and places its proxy Service differently. A public API that knew Envoy Gateway's Service names would need changing for every new gateway profile. CONCEPT.md §5 says controller-specific details stay inside profiles and the public contract expresses observable outcomes.

## Decision

Gateway traffic finds its address from the `Gateway` resource's status. Gateway experiments take their `GatewayClass` from the profile.

## Consequences

G01 to G03 can run unchanged under `gateway-agentgateway`. That rerun checks the profile design cheaply.

Each profile must report its `GatewayClass` and make sure its controller fills in `Gateway` status addresses in a Kind cluster.
