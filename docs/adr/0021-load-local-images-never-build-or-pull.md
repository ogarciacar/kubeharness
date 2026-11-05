# 21. Load local images, never build or pull

Date: 2026-11-04

## Status

Accepted. Recorded in the CONCEPT.md decision table as "Decided (S2)".

## Context

Keeps build tooling out of the library. kubelet refuses a loaded tag that it already pulled under another name.

## Decision

The library loads locally built images and never builds or pulls them.

## Consequences

Not recorded when the decision was made.
