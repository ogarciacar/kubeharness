# 15. Drive Kind through its Go library

Date: 2026-11-04

## Status

Accepted. Recorded in the CONCEPT.md decision table as "Decided (S1)".

## Context

Docker is the only prerequisite. kind's `Create` can block forever, so the harness stops waiting at its deadline instead of interrupting it.

## Decision

Drive Kind through its Go library, not the `kind` CLI.

## Consequences

Not recorded when the decision was made.
