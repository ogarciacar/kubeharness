package kubeharness

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// provider is the cluster backend. Implementations may block without honoring
// cancellation (kind's Create can hang indefinitely), so env never calls them
// without a bound.
type provider interface {
	Create(name, kubeconfigPath string) error
	Delete(name, kubeconfigPath string) error
	List() ([]string, error)
}

var errNameTaken = errors.New("cluster name already exists")

type readyFunc func(ctx context.Context, kubeconfigPath string) error

type timeouts struct {
	preflight     time.Duration
	create        time.Duration
	ready         time.Duration
	cleanup       time.Duration
	retryInterval time.Duration
	// deadlineMargin is kept free before go test's -timeout panic, after cleanup.
	deadlineMargin time.Duration
}

var defaultTimeouts = timeouts{
	preflight:      30 * time.Second,
	create:         3 * time.Minute,
	ready:          2 * time.Minute,
	cleanup:        30 * time.Second,
	retryInterval:  500 * time.Millisecond,
	deadlineMargin: 5 * time.Second,
}

type env struct {
	p          provider
	ready      readyFunc
	name       clusterName
	kubeconfig string
	t          timeouts

	report Report
	// Set before Create: kind can return an error after its nodes already exist.
	mustDispose bool
	createDone  chan error

	closeOnce sync.Once
}

func newEnv(p provider, ready readyFunc, kubeconfigPath string, t timeouts) *env {
	name := newClusterName()
	return &env{
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

func (e *env) fail(cause Cause, err error) error {
	e.report.Cause = cause
	e.report.SetupErr = fmt.Errorf("%s: %w", e.report.Phase, err)
	return e.report.SetupErr
}

func (e *env) start(ctx context.Context) error {
	e.report.Phase = PhaseValidating
	preflightCtx, cancelPreflight := context.WithTimeout(ctx, e.t.preflight)
	defer cancelPreflight()
	began := time.Now()
	var existing []string
	if err := runAbandonable(preflightCtx, func() error {
		var err error
		existing, err = e.p.List()
		return err
	}); err != nil {
		if ctx.Err() != nil {
			return e.fail(causeOf(ctx.Err(), CausePreflight), fmt.Errorf("setup deadline reached during preflight: %w", ctx.Err()))
		}
		if preflightCtx.Err() != nil {
			return e.fail(causeOf(preflightCtx.Err(), CausePreflight), fmt.Errorf("container runtime did not answer after %s: %w", time.Since(began).Round(time.Millisecond), err))
		}
		return e.fail(CausePreflight, fmt.Errorf("container runtime unavailable: %w", err))
	}
	if slices.Contains(existing, string(e.name)) {
		return e.fail(CausePreflight, fmt.Errorf("%w: %s", errNameTaken, e.name))
	}

	e.report.Phase = PhaseProvisioning
	e.mustDispose = true
	e.createDone = make(chan error, 1)
	go func() { e.createDone <- e.p.Create(string(e.name), e.kubeconfig) }()
	createCtx, cancel := context.WithTimeout(ctx, e.t.create)
	defer cancel()
	began = time.Now()
	select {
	case err := <-e.createDone:
		e.createDone = nil
		e.report.CreateDuration = time.Since(began)
		if errors.Is(err, errNameTaken) {
			e.mustDispose = false
			return e.fail(CauseProvisioning, err)
		}
		if err != nil {
			return e.fail(CauseProvisioning, err)
		}
	case <-createCtx.Done():
		e.report.CreateDuration = time.Since(began)
		took := e.report.CreateDuration.Round(time.Millisecond)
		if ctx.Err() != nil {
			return e.fail(causeOf(ctx.Err(), CauseProvisioning), fmt.Errorf("setup deadline reached after %s of create: %w", took, ctx.Err()))
		}
		return e.fail(causeOf(createCtx.Err(), CauseProvisioning), fmt.Errorf("create did not return after %s: %w", took, createCtx.Err()))
	}

	e.report.Phase = PhaseWaitUsable
	readyCtx, cancelReady := context.WithTimeout(ctx, e.t.ready)
	defer cancelReady()
	began = time.Now()
	err := e.ready(readyCtx, e.kubeconfig)
	e.report.ReadyDuration = time.Since(began)
	if err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("setup deadline reached during readiness: %w", err)
		}
		return e.fail(causeOf(readyCtx.Err(), CauseReadiness), err)
	}
	e.report.Phase = PhaseReady
	return nil
}

// close disposes of the cluster. end is recorded as the cause only when
// setup succeeded, so a setup failure is never overwritten.
func (e *env) close(end Cause) Report {
	e.closeOnce.Do(func() {
		if e.report.Cause == CauseOK {
			e.report.Cause = end
		}
		if !e.mustDispose {
			e.report.Cleanup = CleanupNotNeeded
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), e.t.cleanup)
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
func (e *env) dispose(ctx context.Context) (CleanupOutcome, error, []string) {
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
			err := errors.Join(fmt.Errorf("cleanup budget of %s ran out", e.t.cleanup), deleteErr, listErr)
			if present {
				return CleanupFailed, err, []string{string(e.name)}
			}
			if e.createInFlight() {
				err = errors.Join(err, errors.New("create still in flight; it may still start nodes"))
			}
			return CleanupUncertain, err, nil
		case <-time.After(e.t.retryInterval):
		}
	}
}

func (e *env) createInFlight() bool {
	if e.createDone == nil {
		return false
	}
	select {
	case err := <-e.createDone:
		e.createDone = nil
		if errors.Is(err, errNameTaken) {
			e.mustDispose = false
		}
		return false
	default:
		return true
	}
}
