package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeStore struct {
	got     []Order
	err     error
	pingErr error
}

func (f *fakeStore) CreateOrder(_ context.Context, o Order) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.got = append(f.got, o)
	return int64(len(f.got)), nil
}

func (f *fakeStore) Ready(context.Context) error { return f.pingErr }

func post(h http.Handler, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(body)))
	return rec
}

func TestCreateOrderStoresItAndReturns201WithTheID(t *testing.T) {
	s := &fakeStore{}
	rec := post(routes(s), `{"item":"book","quantity":2}`)
	if rec.Code != http.StatusCreated || strings.TrimSpace(rec.Body.String()) != `{"id":1}` {
		t.Fatalf("POST /orders = %d %q", rec.Code, rec.Body)
	}
	if len(s.got) != 1 || s.got[0] != (Order{Item: "book", Quantity: 2}) {
		t.Fatalf("stored %v", s.got)
	}
}

func TestInvalidOrdersAreRejectedWithoutTouchingTheStore(t *testing.T) {
	for _, body := range []string{
		`{"item":"","quantity":1}`, `{"item":"   ","quantity":1}`, `{"item":"bo\u0000ok","quantity":1}`,
		`{"item":"book","quantity":0}`, `{"item":"book","quantity":3000000000}`,
		`{"item":"book","quantity":1,"price":9}`, `{"item":"book","quantity":1}garbage`,
		`{"item":"book","quantity":1}{"item":"pen","quantity":1}`, `not json`,
	} {
		s := &fakeStore{}
		if rec := post(routes(s), body); rec.Code != http.StatusBadRequest || len(s.got) != 0 {
			t.Errorf("POST %s = %d, stored %v", body, rec.Code, s.got)
		}
	}
}

func TestStoreFailureIs500AndHealthReflectsTheDatabase(t *testing.T) {
	s := &fakeStore{err: errors.New("connection refused")}
	if rec := post(routes(s), `{"item":"book","quantity":1}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("POST with a failing store = %d", rec.Code)
	}
	for _, c := range []struct {
		ping error
		want int
	}{{nil, http.StatusOK}, {errors.New(`relation "orders" does not exist`), http.StatusServiceUnavailable}} {
		rec := httptest.NewRecorder()
		routes(&fakeStore{pingErr: c.ping}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Code != c.want {
			t.Errorf("GET /healthz with ping error %v = %d, want %d", c.ping, rec.Code, c.want)
		}
	}
}

func TestTheLargestQuantityPostgreSQLStoresIsAccepted(t *testing.T) {
	s := &fakeStore{}
	if rec := post(routes(s), `{"item":"bolt","quantity":2147483647}`); rec.Code != http.StatusCreated {
		t.Fatalf("POST with quantity 2147483647 = %d %q, want 201", rec.Code, rec.Body)
	}
}
