package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEveryRequestIsAnsweredWithTheBackendNameAndWhatItSaw(t *testing.T) {
	for _, c := range []struct{ method, target string }{
		{http.MethodGet, "http://shop.example/orders/42"},
		{http.MethodPost, "http://payments.example/"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(c.method, c.target, nil)
		handler("orders").ServeHTTP(rec, req)
		var got Reply
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s %s: body %q: %v", c.method, c.target, rec.Body, err)
		}
		want := Reply{Backend: "orders", Path: req.URL.Path, Host: req.Host}
		if rec.Code != http.StatusOK || got != want || rec.Header().Get("X-Backend") != "orders" {
			t.Errorf("%s %s = %d %+v X-Backend=%q, want 200 %+v X-Backend=orders", c.method, c.target, rec.Code, got, rec.Header().Get("X-Backend"), want)
		}
	}
}
