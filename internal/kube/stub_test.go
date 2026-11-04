package kube

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// stubAPI serves fixed objects by URL path suffix, like a real API server
// that honors request contexts, which client-go's fake clientset does not.
// A nil object makes that path hang until the client gives up.
func stubAPI(t *testing.T, objects map[string]runtime.Object) (*rest.Config, kubernetes.Interface) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for suffix, obj := range objects {
			if !strings.HasSuffix(r.URL.Path, suffix) {
				continue
			}
			if obj == nil {
				<-r.Context().Done()
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(obj)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/log") {
			_, _ = w.Write([]byte("stub log line"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	cfg := &rest.Config{Host: srv.URL}
	return cfg, kubernetes.NewForConfigOrDie(cfg)
}
