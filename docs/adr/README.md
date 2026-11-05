# Architecture Decision Records

Each file here records one architecture decision in the format Michael Nygard described in "Documenting Architecture Decisions" (2011). The index of all decisions is the decision table in [CONCEPT.md §2](../../CONCEPT.md#2-decisions-and-boundaries). Why the project records decisions this way is itself [decision 27](0027-record-architecture-decisions.md).

## Adding a decision

1. Take the next number after the highest file here. The number is the decision's ID, so `0028-*.md` is D28.
2. Name the file `NNNN-short-title.md`, with the title in lowercase words joined by hyphens.
3. Copy the template below and fill in every section.
4. Add a row to the CONCEPT.md decision table with the ID linking to the file, the decision in one sentence, and the status.

## Template

```markdown
# N. Short title

Date: YYYY-MM-DD

## Status

Proposed, Accepted, Deprecated, or Superseded by [M](MMMM-title.md).

## Context

The forces at play: the problem, the constraints, the evidence, and the options considered. Link sources.

## Decision

What was decided, in full sentences.

## Consequences

What becomes easier or harder, and what follow-up work it creates.
```

## Changing a decision

Do not rewrite an accepted record. Write a new record that supersedes it. Set the old record's status to "Superseded by [M](MMMM-title.md)", and say in the new record's status which record it supersedes. Update both rows in the CONCEPT.md table.

A record may still get small fixes, such as a broken link, or a status change from Proposed to Accepted.

## Converted records

D01 to D22 were converted from rows of the original CONCEPT.md decision table. Their Context is the row's old Rationale cell, and their Consequences were not recorded at the time.
