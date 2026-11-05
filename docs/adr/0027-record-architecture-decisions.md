# 27. Record architecture decisions

Date: 2026-11-05

## Status

Accepted.

## Context

D01 to D26 lived as rows in the CONCEPT.md decision table. A row holds a decision, a status, and one cell of rationale. That was enough for most early decisions. The kgateway evaluation ([23](0023-reject-kgateway-for-the-gateway-api-profile.md)) was the first whose main content was a rejected alternative and the evidence against it, and a single cell squeezed it. Rows were also edited in place, so a changed decision left its history only in git.

## Decision

Record each architecture decision as an Architecture Decision Record in `docs/adr/`, in the format Michael Nygard described in "Documenting Architecture Decisions" (2011): a numbered title, Status, Context, Decision, and Consequences.

File numbers match the existing D-numbers, so D23 is `0023-*.md`. The CONCEPT.md decision table stays as the index and links each ID to its record. A changed decision gets a new record that supersedes the old one, and both are marked.

## Consequences

D01 to D22 were converted from table rows. Their Context is the old Rationale cell, and their Consequences were not recorded at the time.

New decisions need a file and an index row. The index table no longer carries rationale.
