package main

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestEveryRequestIsAnsweredWithTheBackendNameAndWhatItSaw(t *testing.T) {
	var requests atomic.Int64
	h := handler("orders", "boot", &requests)
	for _, c := range []struct{ method, target string }{
		{http.MethodGet, "http://shop.example/orders/42"},
		{http.MethodGet, "http://shop.example/"},
		{http.MethodPost, "http://payments.example/"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(c.method, c.target, nil)
		h.ServeHTTP(rec, req)
		var got Reply
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s %s: body %q: %v", c.method, c.target, rec.Body, err)
		}
		want := Reply{Backend: "orders", Path: req.URL.Path, Host: req.Host}
		if rec.Code != http.StatusOK || got != want || rec.Header().Get("X-Backend") != "orders" || requests.Load() != 0 {
			t.Errorf("%s %s = %d %+v X-Backend=%q count=%d, want 200 %+v X-Backend=orders count=0",
				c.method, c.target, rec.Code, got, rec.Header().Get("X-Backend"), requests.Load(), want)
		}
	}
}

func TestRequestsReportsBootAndCountWithoutIncrementing(t *testing.T) {
	var requests atomic.Int64
	const boot = "bbbbbbbbbbbbbbbb"
	h := handler("payments", boot, &requests)

	read := func() reading {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://payments.example/requests", nil)
		req.Header.Set("X-Kubeharness-Count", "1")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /requests = %d, want 200", rec.Code)
		}
		var got reading
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("GET /requests body %q: %v", rec.Body, err)
		}
		if rec.Header().Get("X-Backend") != "" {
			t.Fatalf("GET /requests set X-Backend=%q", rec.Header().Get("X-Backend"))
		}
		return got
	}

	if got := read(); got != (reading{Boot: boot, Count: 0}) || requests.Load() != 0 {
		t.Fatalf("first read = %+v stored %d, want boot %s count 0", got, requests.Load(), boot)
	}

	send := func(value string) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://shop.example/payments", nil)
		if value != "" {
			req.Header.Set("X-Kubeharness-Count", value)
		}
		h.ServeHTTP(rec, req)
		var got Reply
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("header %q: body %q: %v", value, rec.Body, err)
		}
		want := Reply{Backend: "payments", Path: "/payments", Host: "shop.example"}
		if rec.Code != http.StatusOK || got != want || rec.Header().Get("X-Backend") != "payments" {
			t.Fatalf("header %q = %d %+v X-Backend=%q, want 200 %+v X-Backend=payments",
				value, rec.Code, got, rec.Header().Get("X-Backend"), want)
		}
	}

	send("")
	send("0")
	if got := read(); got.Count != 0 || got.Boot != boot || requests.Load() != 0 {
		t.Fatalf("after uncounted requests = %+v stored %d, want count 0", got, requests.Load())
	}
	send("1")
	send("1")
	if got := read(); got != (reading{Boot: boot, Count: 2}) || requests.Load() != 2 {
		t.Fatalf("after two counted requests = %+v stored %d, want count 2", got, requests.Load())
	}
}

func TestProcessBootIsEightRandomBytes(t *testing.T) {
	got := processBoot()
	raw, err := hex.DecodeString(got)
	if err != nil || len(raw) != 8 || len(got) != 16 {
		t.Fatalf("processBoot() = %q, want 16 hex chars for 8 bytes", got)
	}
}
