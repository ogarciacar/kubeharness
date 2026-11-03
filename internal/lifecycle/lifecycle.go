package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// Provider is the cluster backend. Implementations may block without honoring
// cancellation (kind's Create can hang indefinitely), so Env never calls them
// without a bound.
type Provider interface {
	Create(name, kubeconfigPath string) error
	Delete(name, kubeconfigPath string) error
	List() ([]string, error)
}

var ErrNameTaken = errors.New("cluster name already exists")

type ReadyFunc func(ctx context.Context, kubeconfigPath string) error

type Timeouts struct {
	Preflight     time.Duration
	Create        time.Duration
	Ready         time.Duration
	Cleanup       time.Duration
	RetryInterval time.Duration
	// deadlineMargin is kept free before go test's -timeout panic, after cleanup.
	DeadlineMargin time.Duration
}

var DefaultTimeouts = Timeouts{
	Preflight:      30 * time.Second,
	Create:         3 * time.Minute,
	Ready:          2 * time.Minute,
	Cleanup:        30 * time.Second,
	RetryInterval:  500 * time.Millisecond,
	DeadlineMargin: 5 * time.Second,
}

type Env struct {
	p          Provider
	ready      ReadyFunc
	name       ClusterName
	kubeconfig string
	t          Timeouts

	report Report
	// Set before Create: kind can return an error after its nodes already exist.
	mustDispose bool
	createDone  chan error

	closeOnce sync.Once
}

func NewEnv(p Provider, ready ReadyFunc, kubeconfigPath string, t Timeouts) *Env {
	name := NewClusterName()
	return &Env{
		p:          p,
		ready:      ready,
		name:       name,
		kubeconfig: kubeconfigPath,
		t:          t,
		report:     Report{Cluster: string(name), Phase: PhaseNew, Cause: CauseOK},
	}
}

var errAbandoned = errors.New("call still running when its deadline passed")

func runAbandonable(ctx context.Context, f func() error) error {
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		select {
		case err := <-done:
			return err
		default:
			return fmt.Errorf("%w: %w", errAbandoned, ctx.Err())
		}
	}
}

func causeOf(ctxErr error, otherwise Cause) Cause {
	switch {
	case errors.Is(ctxErr, context.DeadlineExceeded):
		return CauseDeadline
	case errors.Is(ctxErr, context.Canceled):
		return CauseCanceled
	}
	return otherwise
}

func (e *Env) Fail(cause Cause, err error) error {
	e.report.Cause = cause
	e.report.SetupErr = fmt.Errorf("%s: %w", e.report.Phase, err)
	return e.report.SetupErr
}

func (e *Env) Start(ctx context.Context) error {
	e.report.Phase = PhaseValidating
	preflightCtx, cancelPreflight := context.WithTimeout(ctx, e.t.Preflight)
	defer cancelPreflight()
	began := time.Now()
	var existing []string
	if err := runAbandonable(preflightCtx, func() error {
		var err error
		existing, err = e.p.List()
		return err
	}); err != nil {
		if ctx.Err() != nil {
			return e.Fail(causeOf(ctx.Err(), CausePreflight), fmt.Errorf("setup deadline reached during preflight: %w", ctx.Err()))
		}
		if preflightCtx.Err() != nil {
			return e.Fail(causeOf(preflightCtx.Err(), CausePreflight), fmt.Errorf("container runtime did not answer after %s: %w", time.Since(began).Round(time.Millisecond), err))
		}
		return e.Fail(CausePreflight, fmt.Errorf("container runtime unavailable: %w", err))
	}
	if slices.Contains(existing, string(e.name)) {
		return e.Fail(CausePreflight, fmt.Errorf("%w: %s", ErrNameTaken, e.name))
	}

	e.report.Phase = PhaseProvisioning
	e.mustDispose = true
	e.createDone = make(chan error, 1)
	go func() { e.createDone <- e.p.Create(string(e.name), e.kubeconfig) }()
	createCtx, cancel := context.WithTimeout(ctx, e.t.Create)
	defer cancel()
	began = time.Now()
	select {
	case err := <-e.createDone:
		e.createDone = nil
		e.report.CreateDuration = time.Since(began)
		if errors.Is(err, ErrNameTaken) {
			e.mustDispose = false
			return e.Fail(CauseProvisioning, err)
		}
		if err != nil {
			return e.Fail(CauseProvisioning, err)
		}
	case <-createCtx.Done():
		e.report.CreateDuration = time.Since(began)
		took := e.report.CreateDuration.Round(time.Millisecond)
		if ctx.Err() != nil {
			return e.Fail(causeOf(ctx.Err(), CauseProvisioning), fmt.Errorf("setup deadline reached after %s of create: %w", took, ctx.Err()))
		}
		return e.Fail(causeOf(createCtx.Err(), CauseProvisioning), fmt.Errorf("create did not return after %s: %w", took, createCtx.Err()))
	}

	e.report.Phase = PhaseWaitUsable
	readyCtx, cancelReady := context.WithTimeout(ctx, e.t.Ready)
	defer cancelReady()
	began = time.Now()
	err := e.ready(readyCtx, e.kubeconfig)
	e.report.ReadyDuration = time.Since(began)
	if err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("setup deadline reached during readiness: %w", err)
		}
		return e.Fail(causeOf(readyCtx.Err(), CauseReadiness), err)
	}
	e.report.Phase = PhaseReady
	return nil
}

// close disposes of the cluster. end is recorded as the cause only when
// setup succeeded, so a setup failure is never overwritten.
func (e *Env) Close(end Cause) Report {
	e.closeOnce.Do(func() {
		if e.report.Cause == CauseOK {
			e.report.Cause = end
		}
		if !e.mustDispose {
			e.report.Cleanup = CleanupNotNeeded
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), e.t.Cleanup)
		defer cancel()
		began := time.Now()
		e.report.Cleanup, e.report.CleanupErr, e.report.Remaining = e.dispose(ctx)
		e.report.DeleteDuration = time.Since(began)
	})
	return e.report
}

// dispose deletes until a listing taken after Create returned no longer shows
// the cluster. Only a listing taken after the latest Delete attempt can confirm
// the cluster is present: kind's Delete can remove the nodes and still return
// a kubeconfig error.
func (e *Env) dispose(ctx context.Context) (CleanupOutcome, error, []string) {
	var deleteErr, listErr error
	present := false
	for {
		inFlight := e.createInFlight()
		if !e.mustDispose {
			return CleanupNotNeeded, nil, nil
		}
		present = false
		if err := runAbandonable(ctx, func() error { return e.p.Delete(string(e.name), e.kubeconfig) }); err != nil && !errors.Is(err, errAbandoned) {
			deleteErr = err
		}
		if ctx.Err() == nil {
			var names []string
			err := runAbandonable(ctx, func() error {
				var err error
				names, err = e.p.List()
				return err
			})
			switch {
			case err != nil:
				if !errors.Is(err, errAbandoned) {
					listErr = fmt.Errorf("list clusters: %w", err)
				}
			case slices.Contains(names, string(e.name)):
				present = true
			case !inFlight:
				return CleanupSucceeded, nil, nil
			}
		}
		select {
		case <-ctx.Done():
			err := errors.Join(fmt.Errorf("cleanup budget of %s ran out", e.t.Cleanup), deleteErr, listErr)
			if present {
				return CleanupFailed, err, []string{string(e.name)}
			}
			if e.createInFlight() {
				err = errors.Join(err, errors.New("create still in flight; it may still start nodes"))
			}
			return CleanupUncertain, err, nil
		case <-time.After(e.t.RetryInterval):
		}
	}
}

func (e *Env) createInFlight() bool {
	if e.createDone == nil {
		return false
	}
	select {
	case err := <-e.createDone:
		e.createDone = nil
		if errors.Is(err, ErrNameTaken) {
			e.mustDispose = false
		}
		return false
	default:
		return true
	}
}

// Name is the cluster name this Env owns.
func (e *Env) Name() string { return string(e.name) }
