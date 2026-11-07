// Command backend is the gateway example's backend. A request gets a 200
// that names the backend and echoes the path and host it saw, so a test can
// tell which backend a Gateway routed the request to. GET /requests is the
// exception. It returns this process's boot and the count of requests that
// carried X-Kubeharness-Count equal to 1, and it does not increment that count.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

const countHeader = "X-Kubeharness-Count"

type Reply struct {
	Backend string `json:"backend"`
	Path    string `json:"path"`
	Host    string `json:"host"`
}

type reading struct {
	Boot  string `json:"boot"`
	Count int64  `json:"count"`
}

func handler(name, boot string, requests *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/requests" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(reading{Boot: boot, Count: requests.Load()})
			return
		}
		if r.Header.Get(countHeader) == "1" {
			requests.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Backend", name)
		_ = json.NewEncoder(w).Encode(Reply{Backend: name, Path: r.URL.Path, Host: r.Host})
	})
}

func processBoot() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		log.Fatal(err)
	}
	return hex.EncodeToString(b[:])
}

func main() {
	name := os.Getenv("BACKEND_NAME")
	if name == "" {
		log.Fatal("BACKEND_NAME is required")
	}
	var requests atomic.Int64
	srv := &http.Server{
		Addr:              ":8080",
		Handler:           handler(name, processBoot(), &requests),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("backend %s listening on %s", name, srv.Addr)
	log.Fatal(srv.ListenAndServe())
}
