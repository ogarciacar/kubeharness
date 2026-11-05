//go:build e2e

// Package gateway_test runs G01: HTTP through a Gateway reaches the backend
// its HTTPRoute names, chosen by host and path. The setup functions differ;
// assertRouting is shared.
package gateway_test

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"sync"
	"testing"
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
