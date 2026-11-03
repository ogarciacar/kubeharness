package kubeharness

import (
	"fmt"
	"strings"
	"time"
)

// Phase is the furthest lifecycle phase the environment entered.
type Phase string

const (
	PhaseNew          Phase = "new"
	PhaseValidating   Phase = "validating"
	PhaseProvisioning Phase = "provisioning"
	PhaseWaitUsable   Phase = "wait-usable"
	PhaseReady        Phase = "ready"
)

// Cause is the primary reason the run ended the way it did.
type Cause string

const (
	CauseOK           Cause = "ok"
	CausePreflight    Cause = "preflight"
	CauseProvisioning Cause = "provisioning"
	CauseReadiness    Cause = "readiness"
	CauseDeadline     Cause = "deadline"
	CauseCanceled     Cause = "canceled"
	CauseTestFailed   Cause = "test-failed"
)

// CleanupOutcome is reported separately from Cause so neither hides the other.
type CleanupOutcome string

const (
	CleanupNotNeeded CleanupOutcome = "not-needed"
	CleanupSucceeded CleanupOutcome = "succeeded"
	CleanupFailed    CleanupOutcome = "failed"
	// CleanupUncertain means disposal could not be confirmed, for example
	// because a Create call was still in flight when the cleanup budget ran out.
	CleanupUncertain CleanupOutcome = "uncertain"
)

// Report records setup, test, and cleanup outcomes independently.
type Report struct {
	Cluster    string
	Phase      Phase
	Cause      Cause
	SetupErr   error
	Cleanup    CleanupOutcome
	CleanupErr error
	// Remaining lists owned clusters still present after cleanup.
	Remaining      []string
	CreateDuration time.Duration
	ReadyDuration  time.Duration
	DeleteDuration time.Duration
}

// String renders the single report line every run prints.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "kubeharness cluster=%s phase=%s cause=%s cleanup=%s remaining=%d create=%s ready=%s delete=%s",
		r.Cluster, r.Phase, r.Cause, r.Cleanup, len(r.Remaining),
		r.CreateDuration.Round(100*time.Millisecond),
		r.ReadyDuration.Round(100*time.Millisecond),
		r.DeleteDuration.Round(10*time.Millisecond))
	if r.SetupErr != nil {
		fmt.Fprintf(&b, " setup_err=%q", r.SetupErr.Error())
	}
	if r.CleanupErr != nil {
		fmt.Fprintf(&b, " cleanup_err=%q", r.CleanupErr.Error())
	}
	return b.String()
}
