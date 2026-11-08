package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAwaitProbeRetriesUntilTheProbeAccepts(t *testing.T) {
	var seen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		if r.Header.Get("X-Test") != "1" {
			http.Error(w, "no header", http.StatusBadRequest)
			return
		}
		if seen < 2 {
			http.Error(w, "later", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := AwaitProbe(ctx, context.Background(), responseCluster(), responseTarget, dialTransport(srv.Listener.Addr().String()), func(ctx context.Context, c *http.Client) error {
		base := c.Transport
		c.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			r.Header.Set("X-Test", "1")
			return base.RoundTrip(r)
		})
		resp, err := c.Get(srv.URL)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			buf, _ := io.ReadAll(resp.Body)
			return errors.New(resp.Status + " " + string(buf))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != 2 {
		t.Fatalf("requests = %d, want 2", seen)
	}
}

func TestAwaitProbeReportsTheProbeError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	got := AwaitProbe(ctx, context.Background(), responseCluster(), responseTarget, nil, func(context.Context, *http.Client) error {
		return errors.New("mcp initialize http://mcp.example/mcp: refused\nmore")
	})
	if got == nil || !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("probe = %v", got)
	}
	for _, want := range []string{
		"last attempt: mcp initialize http://mcp.example/mcp: refused more",
		"proxy envoy-gateway-system/envoy-edge",
	} {
		if !strings.Contains(got.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, got)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
