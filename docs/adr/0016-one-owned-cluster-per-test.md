# 16. One owned cluster per test

Date: 2026-11-04

## Status

Accepted. Recorded in the CONCEPT.md decision table as "Decided (S1, S2)".

## Context

Simplest correct isolation. A cluster shared by a package's tests is still open (question 2). Taking `t` in every method would allow subtests.

## Decision

One owned cluster per test, created by `New(t)` and deleted in `t.Cleanup`. Methods must be called from the test that created the `Harness`.

## Consequences

Not recorded when the decision was made.
