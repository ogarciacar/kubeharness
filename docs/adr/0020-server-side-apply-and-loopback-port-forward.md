# 20. Server-side apply and loopback port-forward

Date: 2026-11-04

## Status

Accepted. Recorded in the CONCEPT.md decision table as "Decided (S2)".

## Context

No `kubectl` dependency and no `New` options. Traffic origin is host to API server to one Pod, which proves the app, not a Service, Gateway, or mesh path.

## Decision

Server-side apply through client-go, and host access through client-go port-forward on loopback only.

## Consequences

Not recorded when the decision was made.
