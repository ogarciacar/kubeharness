//go:build e2e

// Package gateway_test runs G01: HTTP through a Gateway reaches the backend
// its HTTPRoute names, chosen by host and path. The setup functions differ;
// assertRouting is shared.
package gateway_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/ogarciacar/kubeharness"
)

const backendImage = "kubeharness.local/gateway-backend:test"

var buildOnce = sync.OnceValues(func() ([]byte, error) {
	return exec.Command("docker", "build", "-q", "-t", backendImage, "backend").CombinedOutput()
})

func buildBackendImage(t *testing.T) {
	t.Helper()
	if out, err := buildOnce(); err != nil {
		t.Fatalf("docker build %s: %v\n%s", backendImage, err, out)
	}
}

// g01Routes lists requests and the backend testdata/g01 routes each to. An
// empty backend means no route matches, so no backend may answer.
var g01Routes = []struct{ url, backend string }{
	{"http://shop.example/orders", "orders"},
	{"http://shop.example/orders/42", "orders"},
	{"http://shop.example/payments", "payments"},
	{"http://payments.example/refunds", "payments"},
	{"http://shop.example/unrouted", ""},
	{"http://unknown.example/orders", ""},
}

// assertRouting sends each g01Routes request through client, whose transport
// must deliver every request to the Gateway whatever the URL host, and checks
// which backend answered and what it saw.
func assertRouting(t *testing.T, client *http.Client) {
	t.Helper()
	for _, r := range g01Routes {
		req, _ := http.NewRequest(http.MethodGet, r.url, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Errorf("GET %s: %v", r.url, err)
			continue
		}
		var got struct{ Backend, Path, Host string }
		decodeErr := json.NewDecoder(resp.Body).Decode(&got)
		resp.Body.Close()
		servedBy := resp.Header.Get("X-Backend")
		if r.backend == "" {
			if servedBy != "" || got.Backend != "" {
				t.Errorf("GET %s reached backend %q (X-Backend %q), want no backend", r.url, got.Backend, servedBy)
			}
			continue
		}
		switch {
		case decodeErr != nil:
			t.Errorf("GET %s = %d, body is not a backend reply: %v", r.url, resp.StatusCode, decodeErr)
		case resp.StatusCode != http.StatusOK || servedBy != r.backend || got.Backend != r.backend:
			t.Errorf("GET %s = %d from %q (X-Backend %q), want 200 from %q", r.url, resp.StatusCode, got.Backend, servedBy, r.backend)
		case got.Host != req.URL.Host || got.Path != req.URL.Path:
			t.Errorf("GET %s: backend saw host %q path %q", r.url, got.Host, got.Path)
		}
	}
}

var profiles = []kubeharness.GatewayProfile{kubeharness.EnvoyGateway()}

// setupWithHarness is the G01 setup with kubeharness. setuplines counts it
// against setupBaseline.
func setupWithHarness(t *testing.T, p kubeharness.GatewayProfile) (*kubeharness.Harness, *kubeharness.Gateway) {
	h := kubeharness.New(t)
	gc := h.InstallGateway(p)
	buildBackendImage(t)
	h.LoadImage(backendImage)
	h.Apply("testdata/g01")
	h.WaitReady("orders")
	h.WaitReady("payments")
	gw := gc.Gateway("edge")
	gw.AwaitRoute("shop", kubeharness.RouteReady)
	gw.AwaitRoute("payments", kubeharness.RouteReady)
	// Route status says the controller accepted the route. The proxy may
	// still be applying that configuration, and assertRouting sends each
	// request once, so the data plane has to answer first.
	gw.AwaitResponse(get("http://shop.example/orders"), servedBy("orders"))
	return h, gw
}

func TestG01Routing(t *testing.T) {
	for _, p := range profiles {
		t.Run(p.Name(), func(t *testing.T) {
			_, gw := setupWithHarness(t, p)
			assertRouting(t, gw.Client())
		})
	}
}

// setupWrongPort installs g01 with the orders Service targetPort set to a
// port the container does not listen on. The readiness probe stays on the
// named container port, so WaitReady can succeed. The HTTPRoute still
// references Service port 80, so the parent can be RouteReady while the
// proxy cannot reach the process.
func setupWrongPort(t *testing.T, p kubeharness.GatewayProfile) *kubeharness.Gateway {
	h := kubeharness.New(t)
	gc := h.InstallGateway(p)
	buildBackendImage(t)
	h.LoadImage(backendImage)
	h.Apply("testdata/g01-wrong-port")
	h.WaitReady("orders")
	h.WaitReady("payments")
	gw := gc.Gateway("edge")
	gw.AwaitRoute("shop", kubeharness.RouteReady)
	return gw
}

// TestG01WrongBackendPort checks that a closed Service targetPort leaves the
// route ready and that the response did not come from the backend.
func TestG01WrongBackendPort(t *testing.T) {
	for _, p := range profiles {
		t.Run(p.Name(), func(t *testing.T) {
			gw := setupWrongPort(t, p)
			gw.AwaitResponse(get("http://shop.example/orders"), notFromBackend())
		})
	}
}

// TestG01WrongPortFailureExplainsItself checks what a test author sees when
// they still expect the backend. The failure names the route and shows the
// proxy section. It does not require a particular status code.
func TestG01WrongPortFailureExplainsItself(t *testing.T) {
	out, err := runHelper(t, "TestHelperWrongPortExpectsOrders")
	if err == nil {
		t.Fatalf("AwaitResponse passed against a closed backend port:\n%s", out)
	}
	for _, want := range []string{"route default/shop", "ResolvedRefs=", "proxy "} {
		if !strings.Contains(out, want) {
			t.Errorf("failure output lacks %q:\n%s", want, out)
		}
	}
}

func TestHelperWrongPortExpectsOrders(t *testing.T) {
	if os.Getenv("KUBEHARNESS_E2E_HELPER") != "1" {
		t.Skip("helper for TestG01WrongPortFailureExplainsItself")
	}
	gw := setupWrongPort(t, kubeharness.EnvoyGateway())
	gw.AwaitResponse(get("http://shop.example/orders"), servedBy("orders"))
}

func get(url string) *http.Request {
	r, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		panic(err)
	}
	return r
}

func servedBy(backend string) func(*http.Response) error {
	return func(r *http.Response) error {
		if got := r.Header.Get("X-Backend"); r.StatusCode != http.StatusOK || got != backend {
			return fmt.Errorf("%d from %q, want 200 from %q", r.StatusCode, got, backend)
		}
		return nil
	}
}

// notFromBackend accepts a response the orders process did not serve.
// HTTP 200 or an X-Backend header is the backend. Any other response passes.
func notFromBackend() func(*http.Response) error {
	return func(r *http.Response) error {
		got := r.Header.Get("X-Backend")
		if r.StatusCode == http.StatusOK || got != "" {
			return fmt.Errorf("%d from %q, want a response the backend did not serve", r.StatusCode, got)
		}
		return nil
	}
}

// runHelper runs one helper test in a subprocess. The failure text the parent
// asserts is that process's output, which includes the harness evidence.
func runHelper(t *testing.T, test string, extraArgs ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^" + test + "$", "-test.v"}, extraArgs...)...)
	cmd.Env = append(os.Environ(), "KUBEHARNESS_E2E_HELPER=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}
