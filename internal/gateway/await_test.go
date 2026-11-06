package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestZeroVerdictDoesNotPass(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	got, err := Await(ctx, context.Background(), 10*time.Millisecond, func(context.Context) (string, Verdict) {
		return "should not pass", Verdict{}
	}, func(context.Context) string { return "gw evidence" })
	if got != "" {
		t.Fatalf("zero verdict returned %q", got)
	}
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "no observation") || !strings.Contains(err.Error(), "gw evidence") {
		t.Fatalf("zero verdict error = %v", err)
	}
}

func TestAwaitKeepsTheLastReasonWhenCtxEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	calls := 0
	_, err := Await(ctx, context.Background(), 10*time.Millisecond, func(ctx context.Context) (int, Verdict) {
		calls++
		if calls == 1 {
			return 0, Pending("Programmed=False reason=AddressNotAssigned: no address")
		}
		<-ctx.Done()
		return 0, Pending("list: %s", ctx.Err())
	}, func(context.Context) string { return "evidence block" })
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "AddressNotAssigned") || strings.Contains(err.Error(), "list:") || !strings.Contains(err.Error(), "evidence block") {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestAwaitStopReturnsAtOnceWithEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	stop := errors.New("route default/shop has no parentRef to gateway default/edge")
	evidenceCtx := context.WithValue(context.Background(), evidenceKey{}, "evidence-ctx")
	start := time.Now()
	got, err := Await(ctx, evidenceCtx, time.Hour, func(context.Context) (int, Verdict) {
		return 7, Stop(stop)
	}, func(ctx context.Context) string {
		if ctx.Value(evidenceKey{}) != "evidence-ctx" {
			t.Errorf("evidence saw ctx %v", ctx)
		}
		return "gateway default/edge"
	})
	if got != 0 || !errors.Is(err, stop) || !strings.Contains(err.Error(), "gateway default/edge") || time.Since(start) > time.Second {
		t.Fatalf("Stop = %d %v after %s", got, err, time.Since(start))
	}
}

func TestAwaitDoneReturnsTheValue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	got, err := Await(ctx, context.Background(), time.Hour, func(context.Context) (string, Verdict) {
		return "ready", Done()
	}, func(context.Context) string {
		t.Error("evidence called on Done")
		return "nope"
	})
	if got != "ready" || err != nil {
		t.Fatalf("Done = %q %v", got, err)
	}
}

type evidenceKey struct{}
