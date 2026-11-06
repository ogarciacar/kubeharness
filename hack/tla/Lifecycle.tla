---- MODULE Lifecycle ----
\* Models internal/lifecycle Env.Start allocation and Env.Close / dispose
\* (lifecycle.go). Names match the Go types in report.go and ADR 0019.
\*
\* Covered Go symbols:
\*   Env.Start, Env.Close, Env.dispose, Env.createInFlight, Env.Fail
\*   mustDispose, CleanupSucceeded|Failed|Uncertain|NotNeeded
\*
\* Out of scope: real Kind/Docker, readiness polls, gateway profiles,
\* report durations, concurrent Close vs RecordProfile locking.

EXTENDS Naturals, TLC

CONSTANTS
  MaxDisposeSteps,   \* bound on dispose loop iterations (cleanup budget)
  Mutate             \* TRUE enables a known-bad Succeeded report (for skill proof)

ASSUME MaxDisposeSteps \in Nat /\ MaxDisposeSteps >= 1
ASSUME Mutate \in BOOLEAN

Phases == {"new", "validating", "provisioning", "wait-usable", "ready"}
Causes == {"ok", "preflight", "provisioning", "readiness", "deadline", "canceled", "test-failed"}
Cleanups == {"unset", "not-needed", "succeeded", "failed", "uncertain"}

VARIABLES
  phase,           \* Report.Phase
  cause,           \* Report.Cause
  mustDispose,     \* Env.mustDispose
  createInFlight,  \* createDone channel still open
  clusterPresent,  \* Provider.List contains this Env's name
  foreignPresent,  \* another cluster that must never be deleted by this Env
  cleanup,         \* Report.Cleanup
  remaining,       \* Report.Remaining nonempty <=> failed with cluster still listed
  closed,          \* Close has run (closeOnce)
  disposeSteps     \* how many dispose iterations remain in the budget

vars == <<phase, cause, mustDispose, createInFlight, clusterPresent,
          foreignPresent, cleanup, remaining, closed, disposeSteps>>

TypeOK ==
  /\ phase \in Phases
  /\ cause \in Causes
  /\ mustDispose \in BOOLEAN
  /\ createInFlight \in BOOLEAN
  /\ clusterPresent \in BOOLEAN
  /\ foreignPresent \in BOOLEAN
  /\ cleanup \in Cleanups
  /\ remaining \in BOOLEAN
  /\ closed \in BOOLEAN
  /\ disposeSteps \in 0..MaxDisposeSteps

----

Init ==
  /\ phase = "new"
  /\ cause = "ok"
  /\ mustDispose = FALSE
  /\ createInFlight = FALSE
  /\ clusterPresent = FALSE
  /\ foreignPresent \in BOOLEAN
  /\ cleanup = "unset"
  /\ remaining = FALSE
  /\ closed = FALSE
  /\ disposeSteps = MaxDisposeSteps

----
\* Start / Fail paths (lifecycle.go Start)

PreflightFail ==
  /\ ~closed
  /\ phase = "new"
  /\ phase' = "validating"
  /\ cause' = "preflight"
  /\ mustDispose' = FALSE
  /\ UNCHANGED <<createInFlight, clusterPresent, foreignPresent, cleanup, remaining, closed, disposeSteps>>

\* Name taken before Create: never sets mustDispose (Start preflight Contains check).
PreflightNameTaken ==
  /\ ~closed
  /\ phase = "new"
  /\ foreignPresent = TRUE
  /\ phase' = "validating"
  /\ cause' = "preflight"
  /\ mustDispose' = FALSE
  /\ UNCHANGED <<createInFlight, clusterPresent, foreignPresent, cleanup, remaining, closed, disposeSteps>>

BeginCreate ==
  /\ ~closed
  /\ phase \in {"new", "validating"}
  /\ cause = "ok"
  /\ phase' = "provisioning"
  /\ mustDispose' = TRUE
  /\ createInFlight' = TRUE
  /\ UNCHANGED <<cause, clusterPresent, foreignPresent, cleanup, remaining, closed, disposeSteps>>

\* Create returns. Kind may leave nodes even when Create errors.
CreateReturns(ok, leavesNodes) ==
  /\ ~closed
  /\ createInFlight
  /\ phase = "provisioning"
  /\ createInFlight' = FALSE
  /\ clusterPresent' = leavesNodes
  /\ IF ok
     THEN /\ cause' = cause
          /\ phase' = "wait-usable"
          /\ mustDispose' = mustDispose
     ELSE /\ cause' = "provisioning"
          /\ phase' = phase
          /\ mustDispose' = mustDispose
  /\ UNCHANGED <<foreignPresent, cleanup, remaining, closed, disposeSteps>>

\* Create returns ErrNameTaken: mustDispose cleared, never delete (Start + createInFlight).
CreateNameTaken ==
  /\ ~closed
  /\ createInFlight
  /\ phase = "provisioning"
  /\ createInFlight' = FALSE
  /\ mustDispose' = FALSE
  /\ cause' = "provisioning"
  /\ clusterPresent' = FALSE
  /\ UNCHANGED <<phase, foreignPresent, cleanup, remaining, closed, disposeSteps>>

ReadyOK ==
  /\ ~closed
  /\ phase = "wait-usable"
  /\ cause = "ok"
  /\ ~createInFlight
  /\ clusterPresent
  /\ phase' = "ready"
  /\ UNCHANGED <<cause, mustDispose, createInFlight, clusterPresent, foreignPresent, cleanup, remaining, closed, disposeSteps>>

ReadyFail ==
  /\ ~closed
  /\ phase = "wait-usable"
  /\ cause = "ok"
  /\ cause' \in {"readiness", "deadline", "canceled"}
  /\ UNCHANGED <<phase, mustDispose, createInFlight, clusterPresent, foreignPresent, cleanup, remaining, closed, disposeSteps>>

----
\* Close / dispose (lifecycle.go Close, dispose, createInFlight)

\* Drain an abandoned Create during dispose, matching createInFlight().
DrainCreateDuringDispose ==
  /\ closed
  /\ cleanup = "unset"
  /\ mustDispose
  /\ createInFlight
  /\ createInFlight' = FALSE
  /\ \/ /\ clusterPresent' = clusterPresent
        /\ mustDispose' = mustDispose
     \/ /\ mustDispose' = FALSE
        /\ clusterPresent' = FALSE
  /\ UNCHANGED <<phase, cause, foreignPresent, cleanup, remaining, closed, disposeSteps>>

\* dispose returns CleanupNotNeeded when mustDispose clears mid-loop (NameTaken drain).
DisposeNotNeededAfterDrain ==
  /\ closed
  /\ cleanup = "unset"
  /\ ~mustDispose
  /\ cleanup' = "not-needed"
  /\ remaining' = FALSE
  /\ UNCHANGED <<phase, cause, mustDispose, createInFlight, clusterPresent, foreignPresent, closed, disposeSteps>>

CloseNotNeeded ==
  /\ ~closed
  /\ ~mustDispose
  /\ closed' = TRUE
  /\ cleanup' = "not-needed"
  /\ remaining' = FALSE
  /\ UNCHANGED <<phase, cause, mustDispose, createInFlight, clusterPresent, foreignPresent, disposeSteps>>

CloseBeginDispose ==
  /\ ~closed
  /\ mustDispose
  /\ closed' = TRUE
  /\ cleanup' = "unset"
  /\ disposeSteps' = MaxDisposeSteps
  /\ UNCHANGED <<phase, cause, mustDispose, createInFlight, clusterPresent, foreignPresent, remaining>>

\* One dispose iteration: Delete may remove nodes; List observes; outcomes per ADR 0019.
DisposeStep ==
  /\ closed
  /\ cleanup = "unset"
  /\ mustDispose
  /\ disposeSteps > 0
  /\ \E deleted \in BOOLEAN:
       /\ \/ /\ deleted
             /\ clusterPresent' = FALSE
          \/ /\ ~deleted
             /\ clusterPresent' = clusterPresent
       /\ \/ /\ ~clusterPresent' /\ ~createInFlight
             /\ cleanup' = "succeeded"
             /\ remaining' = FALSE
             /\ disposeSteps' = disposeSteps
          \/ /\ disposeSteps = 1
             /\ ~(~clusterPresent' /\ ~createInFlight)
             /\ IF clusterPresent'
                THEN /\ cleanup' = "failed"
                     /\ remaining' = TRUE
                ELSE /\ cleanup' = "uncertain"
                     /\ remaining' = FALSE
             /\ disposeSteps' = 0
          \/ /\ disposeSteps > 1
             /\ ~(~clusterPresent' /\ ~createInFlight)
             /\ cleanup' = "unset"
             /\ remaining' = FALSE
             /\ disposeSteps' = disposeSteps - 1
       /\ UNCHANGED <<phase, cause, mustDispose, createInFlight, foreignPresent, closed>>

\* Known-bad mutant: report succeeded without a confirming List (skill proof only).
MutantSucceededWithoutList ==
  /\ Mutate
  /\ closed
  /\ cleanup = "unset"
  /\ mustDispose
  /\ clusterPresent
  /\ cleanup' = "succeeded"
  /\ remaining' = FALSE
  /\ UNCHANGED <<phase, cause, mustDispose, createInFlight, clusterPresent, foreignPresent, closed, disposeSteps>>

CloseAlreadyDone ==
  /\ closed
  /\ cleanup # "unset"
  /\ UNCHANGED vars

----

Next ==
  \/ PreflightFail
  \/ PreflightNameTaken
  \/ BeginCreate
  \/ CreateReturns(TRUE, TRUE)
  \/ CreateReturns(FALSE, TRUE)
  \/ CreateReturns(FALSE, FALSE)
  \/ CreateNameTaken
  \/ ReadyOK
  \/ ReadyFail
  \/ CloseNotNeeded
  \/ CloseBeginDispose
  \/ DrainCreateDuringDispose
  \/ DisposeNotNeededAfterDrain
  \/ DisposeStep
  \/ MutantSucceededWithoutList
  \/ CloseAlreadyDone

Spec == Init /\ [][Next]_vars

----
\* Properties in plain words:
\* - cleanup=succeeded only when the owned cluster is gone and Remaining is empty.
\* - cleanup=failed only when the owned cluster is still listed.
\* - cleanup=succeeded never while Create is still in flight.

TruthfulCleanup ==
  /\ (cleanup = "succeeded" => (~clusterPresent /\ ~remaining))
  /\ (cleanup = "failed" => (clusterPresent /\ remaining))
  /\ (cleanup = "uncertain" => ~remaining)
  /\ (cleanup = "not-needed" => ~remaining)

NoSuccessWhileCreateInFlight ==
  cleanup = "succeeded" => ~createInFlight

ClosedImpliesCleanupLabel ==
  (closed /\ cleanup # "unset") => cleanup \in {"not-needed", "succeeded", "failed", "uncertain"}

Inv ==
  /\ TypeOK
  /\ TruthfulCleanup
  /\ NoSuccessWhileCreateInFlight
  /\ ClosedImpliesCleanupLabel

====
