// Command backend is the gateway example's backend: every request gets a 200
// that names the backend and echoes the path and host it saw, so a test can
// tell which backend a Gateway routed a request to.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

// Reply is the body of every response.
type Reply struct {
	Backend string `json:"backend"`
	Path    string `json:"path"`
	Host    string `json:"host"`
}

func handler(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Backend", name)
		_ = json.NewEncoder(w).Encode(Reply{Backend: name, Path: r.URL.Path, Host: r.Host})
	})
}

func main() {
	name := os.Getenv("BACKEND_NAME")
	if name == "" {
		log.Fatal("BACKEND_NAME is required")
	}
	srv := &http.Server{Addr: ":8080", Handler: handler(name), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("backend %s listening on %s", name, srv.Addr)
	log.Fatal(srv.ListenAndServe())
}
