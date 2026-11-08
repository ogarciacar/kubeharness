//go:build e2e

package mcp_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ogarciacar/kubeharness"
	"github.com/ogarciacar/kubeharness/examples/mcp/internal/mcpprobe"
)

const (
	calculatorImage = "kubeharness.local/mcp-calculator:test"
	endpoint        = "http://mcp.example/mcp"
	goodBackend     = "testdata/a01-backend/good"
	a01Manifests    = "testdata/a01"
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

func setup(t *testing.T, backend string) (*kubeharness.Harness, *kubeharness.Gateway) {
	t.Helper()
	h := kubeharness.New(t)
	gc := h.InstallGateway(kubeharness.AgentGateway())
	buildCalculator(t)
	h.LoadImage(calculatorImage)
	h.Apply(a01Manifests)
	h.Apply(backend)
	h.WaitReady("calculator")
	return h, gc.Gateway("edge")
}

func TestA01Tools(t *testing.T) {
	_, gw := setup(t, goodBackend)
	gw.AwaitRoute("mcp", kubeharness.RouteReady)
	gw.AwaitProbe(sumIs42)
	t.Log("tools/call sum(40,2)=42")
}

func TestA01BrokenWiring(t *testing.T) {
	h, gw := setup(t, goodBackend)
	gw.AwaitRoute("mcp", kubeharness.RouteReady)
	gw.AwaitProbe(sumIs42)
	for _, dir := range brokenVariants(t) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			h.Apply(dir)
			gw.AwaitProbe(mcpRefused)
			h.Apply(goodBackend)
			h.Apply(a01Manifests)
			gw.AwaitProbe(sumIs42)
		})
	}
}

func TestA01BrokenWiringExplainsItself(t *testing.T) {
	out, err := runHelper(t, "TestHelperWrongPathExpectsSum")
	if err == nil {
		t.Fatalf("sumIs42 passed against a wrong MCP target path:\n%s", out)
	}
	for _, want := range []string{
		"route default/mcp",
		"backend agentgateway.dev/AgentgatewayBackend default/calculator",
		"Accepted=",
		"mcp initialize",
		"last exchange: POST /mcp",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("failure output lacks %q:\n%s", want, out)
		}
	}
}

func TestHelperWrongPathExpectsSum(t *testing.T) {
	if os.Getenv("KUBEHARNESS_E2E_HELPER") != "1" {
		t.Skip("helper for TestA01BrokenWiringExplainsItself")
	}
	_, gw := setup(t, "testdata/a01-broken/wrong-path")
	gw.AwaitRoute("mcp", kubeharness.RouteReady)
	gw.AwaitProbe(sumIs42)
}

func sumIs42(ctx context.Context, c *http.Client) error {
	s, err := mcpprobe.Open(ctx, c, endpoint)
	if err != nil {
		return err
	}
	defer s.Close()
	names, err := s.ToolNames(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(names, "sum") {
		return fmt.Errorf("tools/list = %q, want it to include sum", names)
	}
	got, err := s.CallText(ctx, "sum", map[string]int{"a": 40, "b": 2})
	if err != nil {
		return err
	}
	if got != "42" {
		return fmt.Errorf("tools/call sum(40,2) = %q, want 42", got)
	}
	return nil
}

// mcpRefused accepts an attempt where the Gateway answered HTTP but the MCP
// exchange failed. A dial error at the port-forward does not count.
func mcpRefused(ctx context.Context, c *http.Client) error {
	s, err := mcpprobe.Open(ctx, c, endpoint)
	if err == nil {
		defer s.Close()
		if _, err = s.CallText(ctx, "sum", map[string]int{"a": 40, "b": 2}); err == nil {
			return errors.New("MCP session and sum call succeeded, want the broken wiring to fail them")
		}
	}
	if !mcpprobe.GatewayAnswered(err) {
		return fmt.Errorf("no HTTP response from the Gateway: %w", err)
	}
	return nil
}

// brokenVariants lists testdata/a01-broken/*, sorted. Each directory is one
// break. Zero directories fails the test, so an emptied testdata cannot pass.
func brokenVariants(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("testdata/a01-broken")
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join("testdata/a01-broken", e.Name()))
		}
	}
	slices.Sort(dirs)
	if len(dirs) == 0 {
		t.Fatal("testdata/a01-broken has no variants")
	}
	return dirs
}

func runHelper(t *testing.T, test string, extraArgs ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^" + test + "$", "-test.v"}, extraArgs...)...)
	cmd.Env = append(os.Environ(), "KUBEHARNESS_E2E_HELPER=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}
