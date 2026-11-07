//go:build e2e

// Package gateway_test runs G01: HTTP through a Gateway reaches the backend
// its HTTPRoute names, chosen by host and path. The setup functions differ;
// assertRouting is shared.
package gateway_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

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

func TestG01WrongBackendPort(t *testing.T) {
	for _, p := range profiles {
		t.Run(p.Name(), func(t *testing.T) {
			gw := setupWrongPort(t, p)
			gw.AwaitResponse(get("http://shop.example/orders"), notFromBackend())
		})
	}
}

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

func notFromBackend() func(*http.Response) error {
	return func(r *http.Response) error {
		got := r.Header.Get("X-Backend")
		if r.StatusCode == http.StatusOK || got != "" {
			return fmt.Errorf("%d from %q, want a response the backend did not serve", r.StatusCode, got)
		}
		return nil
	}
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

const countHeader = "X-Kubeharness-Count"

type reading struct {
	Boot  string `json:"boot"`
	Count int64  `json:"count"`
}

func TestG02ReferenceGrant(t *testing.T) {
	for _, p := range profiles {
		t.Run(p.Name(), func(t *testing.T) {
			h := kubeharness.New(t)
			gc := h.InstallGateway(p)
			buildBackendImage(t)
			h.LoadImage(backendImage)
			h.Apply("testdata/g02")
			h.WaitReady("shop/orders")
			h.WaitReady("payments/payments")
			gw := gc.Gateway("shop/edge")
			read := paymentsCounter(t, h)

			denied(t, gw, read)
			h.Apply("testdata/g02-grant")
			allowed(t, gw, read)
			h.Delete("testdata/g02-grant")
			denied(t, gw, read)
		})
	}
}

func denied(t *testing.T, gw *kubeharness.Gateway, read func() reading) {
	t.Helper()
	gw.AwaitRoute("shop/pay", refNotPermitted)
	gw.AwaitResponse(get("http://shop.example/orders"), servedBy("orders"))
	flatPayments(t, gw.Client(), read)
}

func allowed(t *testing.T, gw *kubeharness.Gateway, read func() reading) {
	t.Helper()
	gw.AwaitRoute("shop/pay", kubeharness.RouteReady)
	before := read()
	gw.AwaitResponse(counted(get("http://shop.example/payments")), servedBy("payments"))
	after := read()
	if after.Count <= before.Count {
		t.Fatalf("payments counter %d to %d, want an increase", before.Count, after.Count)
	}
}

func refNotPermitted(s kubeharness.RouteStatus) error {
	if s.Accepted.Status != "True" {
		return fmt.Errorf("Accepted=%s reason=%s, want True", s.Accepted.Status, s.Accepted.Reason)
	}
	c := s.ResolvedRefs
	if c.Status == "False" && c.Reason == "RefNotPermitted" {
		return nil
	}
	return fmt.Errorf("ResolvedRefs=%s reason=%s, want False reason=RefNotPermitted", c.Status, c.Reason)
}

func counted(req *http.Request) *http.Request {
	req.Header.Set(countHeader, "1")
	return req
}

func flatPayments(t *testing.T, client *http.Client, read func() reading) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	var before, after reading
	for {
		before = read()
		for i := 0; i < 5; i++ {
			req := counted(get("http://shop.example/payments"))
			resp, err := client.Do(req)
			if resp != nil {
				resp.Body.Close()
			}
			if err != nil && time.Now().After(deadline) {
				t.Fatalf("payments burst: %v", err)
			}
		}
		after = read()
		if after.Count == before.Count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("payments counter %d to %d across a burst of 5", before.Count, after.Count)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// Forward pins one Pod, so the Deployment must say replicas: 1 or the count
// is a different Pod from the one the Service uses.
func paymentsCounter(t *testing.T, h *kubeharness.Harness) func() reading {
	t.Helper()
	if got := paymentsReplicas(t); got != 1 {
		t.Fatalf("payments replicas = %d, want 1", got)
	}
	addr := h.Forward("payments/payments", 80)
	url := "http://" + addr + "/requests"
	var boot string
	return func() reading {
		t.Helper()
		got := readCount(t, url)
		if boot == "" {
			boot = got.Boot
			return got
		}
		if got.Boot != boot {
			t.Fatalf("payments boot %q then %q; the process restarted and the counter is not a delta", boot, got.Boot)
		}
		return got
	}
}

func readCount(t *testing.T, url string) reading {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	var got reading
	if err := json.Unmarshal(body, &got); err != nil || resp.StatusCode != http.StatusOK || got.Boot == "" {
		t.Fatalf("GET %s = %d %q, want boot and count", url, resp.StatusCode, body)
	}
	return got
}

func paymentsReplicas(t *testing.T) int {
	t.Helper()
	objs, err := readManifests("testdata/g02")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range objs {
		if u.GetKind() != "Deployment" || u.GetName() != "payments" {
			continue
		}
		v, found, err := unstructured.NestedFieldNoCopy(u.Object, "spec", "replicas")
		n, ok := yamlInt(v)
		if err != nil || !found || !ok {
			t.Fatal("payments Deployment has no spec.replicas")
		}
		return int(n)
	}
	t.Fatal("payments Deployment not found in testdata/g02")
	return 0
}

func TestG02DeniedRouteExplainsItself(t *testing.T) {
	out, err := runHelper(t, "TestHelperDeniedPayExpectsReady")
	if err == nil {
		t.Fatalf("RouteReady passed with no ReferenceGrant:\n%s", out)
	}
	for _, want := range []string{"route shop/pay", "RefNotPermitted"} {
		if !strings.Contains(out, want) {
			t.Errorf("failure output lacks %q:\n%s", want, out)
		}
	}
}

func TestHelperDeniedPayExpectsReady(t *testing.T) {
	if os.Getenv("KUBEHARNESS_E2E_HELPER") != "1" {
		t.Skip("helper for TestG02DeniedRouteExplainsItself")
	}
	h := kubeharness.New(t)
	gc := h.InstallGateway(kubeharness.EnvoyGateway())
	buildBackendImage(t)
	h.LoadImage(backendImage)
	h.Apply("testdata/g02")
	h.WaitReady("shop/orders")
	h.WaitReady("payments/payments")
	gw := gc.Gateway("shop/edge")
	gw.AwaitRoute("shop/pay", kubeharness.RouteReady)
}
