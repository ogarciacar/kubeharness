package gateway

import (
	"context"
	"net/http"
)

// AwaitProbe calls probe with a fresh client every responseInterval until
// probe returns nil. A probe error is Pending, described as
// "last attempt: <probe error on one line>". probe cannot stop the wait.
// The caller's deadline bounds it.
//
// On failure the error carries that line and Evidence for t.
func AwaitProbe(ctx, evidenceCtx context.Context, cl Cluster, t Target, transport *http.Transport, probe func(context.Context, *http.Client) error) error {
	return awaitAttempts(ctx, evidenceCtx, cl, t, transport, func(ctx context.Context, c *http.Client) (string, bool) {
		if err := probe(ctx, c); err != nil {
			return "last attempt: " + oneLine(err.Error()), false
		}
		return "", true
	})
}

// awaitAttempts polls try through t until try reports done or ctx ends.
// AwaitProbe and AwaitResponse both use it. Each writes its own failure
// line, so AwaitResponse still says "last response: ...".
//
// Each attempt's context ends after attemptTimeout. The client's transport
// is a clone of transport, closed when the attempt ends. net/http dials with
// a context that ignores request cancellation, so a re-forward would otherwise
// run until the dialer's own budget. The clone's dial ends with the attempt.
// The caller's transport is left unchanged.
func awaitAttempts(ctx, evidenceCtx context.Context, cl Cluster, t Target, transport *http.Transport, try func(context.Context, *http.Client) (why string, done bool)) error {
	evidence := func(ctx context.Context) string {
		return Evidence(ctx, cl, t.Gateway, t.Service)
	}
	_, err := Await(ctx, evidenceCtx, responseInterval, func(ctx context.Context) (struct{}, Verdict) {
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		defer cancel()
		tr := transportForAttempt(transport, attemptCtx)
		defer tr.CloseIdleConnections()
		why, done := try(attemptCtx, &http.Client{Transport: tr})
		if done {
			return struct{}{}, Done()
		}
		return struct{}{}, Pending("%s", why)
	}, evidence)
	return err
}
