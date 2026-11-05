# 18. Deadlines end before the go test timeout

Date: 2026-11-04

## Status

Accepted. Recorded in the CONCEPT.md decision table as "Decided (S1, S2)".

## Context

`go test`'s `-timeout` panic skips cleanups, so everything must finish before it.

## Decision

Setup and every operation end by `-timeout` minus the cleanup budget and a 5s margin. A watchdog disposes of the cluster early if the test body overruns.

## Consequences

Not recorded when the decision was made.
