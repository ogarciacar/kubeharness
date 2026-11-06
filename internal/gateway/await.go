package gateway

import (
	"context"
	"fmt"
	"time"
)

// Verdict is one observation's outcome. The zero Verdict is Pending with an
// empty reason. An observe function that forgets to decide keeps polling and
// times out with "no observation".
type Verdict struct {
	done bool
	why  string // pending reason, empty when the observe function forgot to decide
	stop error  // set when waiting cannot help
}

// Done says the awaited state holds.
func Done() Verdict { return Verdict{done: true} }

// Pending says the state does not hold yet. why is the timeout message.
func Pending(format string, args ...any) Verdict {
	return Verdict{why: fmt.Sprintf(format, args...)}
}

// Stop says the state can never hold. A missing object is Stop, and so is a
// Gateway whose class no installed controller serves.
func Stop(err error) Verdict { return Verdict{stop: err} }

// Await calls observe every interval until it returns Done or Stop, or ctx
// ends. On Done it returns observe's value. On Stop it returns the error at
// once. When ctx ends it returns ctx's error with the last pending reason
// observed before ctx ended. A reason caused by ctx's own cancellation does
// not replace that earlier reason. The error then includes
// evidence(evidenceCtx) when evidence is non-nil.
//
// Await is the only poll loop in this package. Gateway, route, forward, and
// response waits differ only in observe.
func Await[T any](ctx, evidenceCtx context.Context, interval time.Duration, observe func(context.Context) (T, Verdict), evidence func(context.Context) string) (T, error) {
	var zero T
	var last string
	seen := false
	for {
		val, v := observe(ctx)
		if v.stop != nil {
			return zero, withEvidence(v.stop, evidenceCtx, evidence)
		}
		if v.done {
			return val, nil
		}
		if ctx.Err() == nil || !seen {
			last = v.why
			seen = true
		}
		select {
		case <-ctx.Done():
			return zero, withEvidence(fmt.Errorf("%s: %w", pendingReason(last), ctx.Err()), evidenceCtx, evidence)
		case <-time.After(interval):
		}
	}
}

func pendingReason(why string) string {
	if why == "" {
		return "no observation"
	}
	return why
}

func withEvidence(err error, ctx context.Context, evidence func(context.Context) string) error {
	if err == nil || evidence == nil {
		return err
	}
	text := evidence(ctx)
	if text == "" {
		return err
	}
	return fmt.Errorf("%w\n%s", err, text)
}

const (
	statusInterval   = 500 * time.Millisecond // matches kube's pollInterval
	responseInterval = 250 * time.Millisecond
)
