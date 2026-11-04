// Command orders is the M01 example service: POST /orders stores an order in
// PostgreSQL and returns its id.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Order is what a client posts.
type Order struct {
	Item     string `json:"item"`
	Quantity int    `json:"quantity"`
}

type store interface {
	CreateOrder(ctx context.Context, o Order) (int64, error)
	// Ready reports whether orders can be stored: the database answers and
	// the schema migration has created the table with the columns it writes.
	Ready(ctx context.Context) error
}

type pgStore struct{ pool *pgxpool.Pool }

func (s pgStore) CreateOrder(ctx context.Context, o Order) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, "INSERT INTO orders (item, quantity) VALUES ($1, $2) RETURNING id", o.Item, o.Quantity).Scan(&id)
	return id, err
}

func (s pgStore) Ready(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, "SELECT item, quantity FROM orders LIMIT 0")
	return err
}

func parseOrder(r *http.Request) (Order, error) {
	var o Order
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return Order{}, err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Order{}, errors.New("body must hold exactly one JSON object")
	}
	switch {
	case strings.TrimSpace(o.Item) == "":
		return Order{}, errors.New("item is required")
	case strings.ContainsRune(o.Item, 0):
		return Order{}, errors.New("item must not contain NUL")
	case o.Quantity < 1 || o.Quantity > math.MaxInt32:
		return Order{}, fmt.Errorf("quantity must be between 1 and %d", math.MaxInt32)
	}
	return o, nil
}

func routes(s store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		o, err := parseOrder(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		id, err := s.CreateOrder(r.Context(), o)
		if err != nil {
			log.Printf("create order: %v", err)
			http.Error(w, "could not store the order", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]int64{"id": id})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.Ready(ctx); err != nil {
			http.Error(w, "not ready: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func main() {
	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("database config: %v", err)
	}
	defer pool.Close()
	srv := &http.Server{Addr: ":8080", Handler: routes(pgStore{pool}), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("orders listening on %s", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}
