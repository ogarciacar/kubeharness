# 19. Truthful cleanup reports

Date: 2026-11-04

## Status

Accepted. Recorded in the CONCEPT.md decision table as "Decided (S1)".

## Context

A report that says `succeeded` must be true, and kind's `Delete` can remove nodes and still return an error.

## Decision

Cleanup is `succeeded` only from a listing taken after `Create` returned; `failed` only from a listing after the latest Delete that still shows the cluster; otherwise `uncertain`. `failed` and `uncertain` fail the test with a Docker-only remove command.

## Consequences

Not recorded when the decision was made.
