package kube

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
)

func TestApplyStopsAtItsDeadlineWhenDiscoveryHangs(t *testing.T) {
	cfg, _ := stubAPI(t, map[string]runtime.Object{"/api": nil, "/apis": nil})
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "a"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	began := time.Now()
	_, err := Apply(ctx, cfg, []*unstructured.Unstructured{obj})
	if took := time.Since(began); err == nil || took > 3*time.Second {
		t.Fatalf("Apply took %s past a 300ms deadline: %v", took, err)
	}
}

// The API server answers discovery normally (Widget not served yet) and then
// stops answering 1.5s in. Apply has a 2s limit.
func TestApplyStopsAtItsDeadlineWhenDiscoveryStallsDuringACRDRetry(t *testing.T) {
	start := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if time.Since(start) > 1500*time.Millisecond {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api":
			_, _ = w.Write([]byte(`{"kind":"APIVersions","versions":["v1"]}`))
		case "/apis":
			_, _ = w.Write([]byte(`{"kind":"APIGroupList","apiVersion":"v1","groups":[]}`))
		case "/api/v1":
			_, _ = w.Write([]byte(`{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"configmaps","namespaced":true,"kind":"ConfigMap","verbs":["get","patch"]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	cfg := &rest.Config{Host: srv.URL}
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "e2e.kubeharness.dev/v1", "kind": "Widget", "metadata": map[string]any{"name": "w"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	began := time.Now()
	_, err := Apply(ctx, cfg, []*unstructured.Unstructured{obj})
	took := time.Since(began)
	t.Logf("Apply with a 2s limit returned after %s: %v", took.Round(10*time.Millisecond), err)
	if took > 2500*time.Millisecond {
		t.Errorf("Apply blocked %s past its 2s limit", (took - 2*time.Second).Round(10*time.Millisecond))
	}
}
