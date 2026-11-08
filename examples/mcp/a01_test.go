//go:build e2e

package mcp_test

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ogarciacar/kubeharness"
	"github.com/ogarciacar/kubeharness/examples/mcp/internal/mcpprobe"
)

const (
	calculatorImage = "kubeharness.local/mcp-calculator:test"
	endpoint        = "http://mcp.example/mcp"
	goodBackend     = "testdata/a01-backend/good"

	// attemptLimit matches the library's per-attempt bound. probeBudget matches
	// AwaitRoute. awaitSum is the stand-in for Gateway.AwaitProbe.
	attemptLimit = 5 * time.Second
	probeBudget  = time.Minute
	probeGap     = 250 * time.Millisecond
)

var buildOnce = sync.OnceValues(func() ([]byte, error) {
	return exec.Command("docker", "build", "-q", "-t", calculatorImage, "calculator").CombinedOutput()
})

func buildCalculator(t *testing.T) {
	t.Helper()
	if out, err := buildOnce(); err != nil {
		t.Fatalf("docker build %s: %v\n%s", calculatorImage, err, out)
	}
}

func TestA01Tools(t *testing.T) {
	h := kubeharness.New(t)
	gc := h.InstallGateway(kubeharness.AgentGateway())
	buildCalculator(t)
	h.LoadImage(calculatorImage)
	h.Apply("testdata/a01")
	h.Apply(goodBackend)
	h.WaitReady("calculator")
	gw := gc.Gateway("edge")
	gw.AwaitRoute("mcp", routeReadyLogged(t))
	awaitSum(t, gw)
}

// routeReadyLogged is RouteReady, and on success it prints the parent
// conditions. That line is the ResolvedRefs measurement for this proof.
func routeReadyLogged(t *testing.T) func(kubeharness.RouteStatus) error {
	t.Helper()
	return func(s kubeharness.RouteStatus) error {
		if err := kubeharness.RouteReady(s); err != nil {
			return err
		}
		t.Logf("HTTPRoute mcp parent Accepted=%s reason=%s message=%q ResolvedRefs=%s reason=%s message=%q",
			s.Accepted.Status, s.Accepted.Reason, s.Accepted.Message,
			s.ResolvedRefs.Status, s.ResolvedRefs.Reason, s.ResolvedRefs.Message)
		return nil
	}
}

// awaitSum retries sumIs42 through the Gateway client. Gateway.AwaitProbe is
// not in the library yet. Delete this loop when that method lands.
func awaitSum(t *testing.T, gw *kubeharness.Gateway) {
	t.Helper()
	deadline := time.Now().Add(probeBudget)
	var last error
	var lastLog time.Time
	for time.Now().Before(deadline) {
		if err := t.Context().Err(); err != nil {
			t.Fatalf("mcp sum through gateway: %v", err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), attemptLimit)
		names, err := sumIs42(ctx, gw.Client())
		cancel()
		if err == nil {
			t.Logf("tools/list=%q tools/call sum(40,2)=42", names)
			return
		}
		last = err
		if time.Since(lastLog) >= 5*time.Second {
			t.Logf("mcp attempt: %v", err)
			lastLog = time.Now()
		}
		select {
		case <-t.Context().Done():
			t.Fatalf("mcp sum through gateway: %v", last)
		case <-time.After(probeGap):
		}
	}
	t.Fatalf("mcp sum through gateway: %v", last)
}

func sumIs42(ctx context.Context, c *http.Client) ([]string, error) {
	s, err := mcpprobe.Open(ctx, c, endpoint)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	names, err := s.ToolNames(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(names, "sum") {
		return names, fmt.Errorf("tools/list = %q, want it to include sum", names)
	}
	got, err := s.CallText(ctx, "sum", map[string]int{"a": 40, "b": 2})
	if err != nil {
		return names, err
	}
	if got != "42" {
		return names, fmt.Errorf("tools/call sum(40,2) = %q, want 42", got)
	}
	return names, nil
}
