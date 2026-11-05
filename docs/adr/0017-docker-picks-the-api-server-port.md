# 17. Docker picks the API server port

Date: 2026-11-04

## Status

Accepted. Recorded in the CONCEPT.md decision table as "Decided (S1)".

## Context

kind's default probes a free port, releases it, then reuses it, which races between parallel clusters.

## Decision

Docker picks the API server port (kind `apiServerPort: -1`).

## Consequences

Not recorded when the decision was made.
