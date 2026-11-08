// Command calculator serves the A01 MCP calculator on :8080.
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/ogarciacar/kubeharness/examples/mcp/calculator"
)

func main() {
	srv := &http.Server{
		Addr:              ":8080",
		Handler:           calculator.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
