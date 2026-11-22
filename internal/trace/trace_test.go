package trace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

var update = flag.Bool("update", false, "rewrite testdata/golden.jsonl")

type clock struct {
	mu   sync.Mutex
	t    time.Time
	tick time.Duration
}

func newClock(tick time.Duration) *clock {
	return &clock{t: time.Date(2026, 11, 20, 16, 11, 15, 123e6, time.UTC), tick: tick}
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.t
	c.t = c.t.Add(c.tick)
	return t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type line map[string]any

func lines(t *testing.T, b []byte) []line {
	t.Helper()
	var out []line
	for i, raw := range bytes.SplitAfter(b, []byte("\n")) {
		if len(raw) == 0 {
			continue
		}
		if raw[len(raw)-1] != '\n' {
			t.Fatalf("line %d is not newline-terminated: %q", i+1, raw)
		}
		var l line
		if err := json.Unmarshal(raw, &l); err != nil {
			t.Fatalf("line %d is not JSON: %v\n%s", i+1, err, raw)
		}
		out = append(out, l)
	}
	return out
}

func ofKind(ls []line, ev string) []line {
	var out []line
	for _, l := range ls {
		if l["ev"] == ev {
			out = append(out, l)
		}
	}
	return out
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTheFirstLineIsTheHeader(t *testing.T) {
	var buf bytes.Buffer
	began := time.Now()
	r := Start(&buf, "TestG02ReferenceGrant/gateway-api", "kh-7630e8a319a4-1")
	r.Call(FnNew, "")
	ls := lines(t, buf.Bytes())
	h := ls[0]
	if h["ev"] != "trace" || h["v"] != float64(Version) || h["at"] != float64(0) ||
		h["test"] != "TestG02ReferenceGrant/gateway-api" || h["cluster"] != "kh-7630e8a319a4-1" {
		t.Fatalf("header = %v", h)
	}
	start, err := time.Parse(time.RFC3339, h["start"].(string))
	if err != nil || !strings.HasSuffix(h["start"].(string), "Z") || start.Sub(began).Abs() > time.Second {
		t.Fatalf("start %q is not RFC 3339 UTC near now: %v", h["start"], err)
	}
	if len(ls) != 2 || ls[1]["ev"] != "call" {
		t.Fatalf("lines = %v", ls)
	}
}

func TestAtIsMonotonicAcrossConcurrentWriters(t *testing.T) {
	var buf bytes.Buffer
	r := start(&buf, newClock(time.Millisecond).now, "T", "kh-x")
	const writers, each = 8, 200
	var wg sync.WaitGroup
	for g := range writers {
		wg.Go(func() {
			s := r.Call(FnAwaitResponse, fmt.Sprint(g))
			for i := range each - 2 {
				s.Wait(OnResponse, "GET http://shop.example/payments", fmt.Sprint(i), false)
			}
			s.Return(false)
		})
	}
	wg.Wait()
	ls := lines(t, buf.Bytes())
	if len(ls) != 1+writers*each {
		t.Fatalf("got %d lines, want %d", len(ls), 1+writers*each)
	}
	for i := 1; i < len(ls); i++ {
		if ls[i]["at"].(float64) < ls[i-1]["at"].(float64) {
			t.Fatalf("line %d at %v after line %d at %v", i+1, ls[i]["at"], i, ls[i-1]["at"])
		}
	}
}

func TestNothingSecretReachesTheTrace(t *testing.T) {
	const kubeconfig = "/var/folders/xy/T/TestG02ReferenceGrantgateway-api123/001/kubeconfig"
	var buf bytes.Buffer
	r := Start(&buf, "T", "kh-x", kubeconfig)
	s := r.Call(FnAwaitResponse, "GET https://user:hunter2@shop.example/pay?token=abc#frag")

	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	req, _ := http.NewRequest(http.MethodGet, "https://user:hunter2@shop.example/pay?token=abc#frag", nil)
	req.Header.Set("Authorization", "Bearer hunter2")
	if _, err := (&http.Client{Transport: s.RoundTripper(base, ViaAwait)}).Do(req); err != nil {
		t.Fatal(err)
	}

	long := "conditions:\n  Accepted=True\n  ResolvedRefs=False reason=RefNotPermitted: " +
		`Get "https://127.0.0.1:6443/apis?timeout=5s&token=abc": ` + strings.Repeat("ünïcode ", 60)
	s.Wait(OnRoute, "shop/pay", long, false)
	s.Report("kubeharness cluster=kh-x cleanup=failed cleanup_err=\"remove " + kubeconfig + ".lock: busy\"")

	out := buf.String()
	for _, leaked := range []string{"hunter2", "token", "abc", "frag", "user", "Authorization", "Bearer", kubeconfig, "TestG02ReferenceGrantgateway-api123"} {
		if strings.Contains(out, leaked) {
			t.Errorf("trace contains %q:\n%s", leaked, out)
		}
	}
	ls := lines(t, buf.Bytes())
	if got := ls[1]["arg"]; got != "GET https://shop.example/pay" {
		t.Errorf("call arg = %q", got)
	}
	if got := ofKind(ls, "request")[0]["url"]; got != "https://shop.example/pay" {
		t.Errorf("request url = %q", got)
	}
	state := ofKind(ls, "wait")[0]["state"].(string)
	if strings.ContainsAny(state, "\n\t") || utf8.RuneCountInString(state) != textLimit || !strings.HasSuffix(state, "…") ||
		!strings.Contains(state, `Get "https://127.0.0.1:6443/apis":`) {
		t.Errorf("state is not one line of %d characters with the URL cut: %q", textLimit, state)
	}
	if got := ofKind(ls, "report")[0]["line"]; got != `kubeharness cluster=kh-x cleanup=failed cleanup_err="remove <redacted>.lock: busy"` {
		t.Errorf("report line = %q", got)
	}
}

func TestANilRecorderRecordsNothing(t *testing.T) {
	if Start(nil, "T", "kh-x") != nil {
		t.Fatal("Start(nil) returned a recorder")
	}
	var r *Recorder
	s := r.Call(FnApply, "testdata/g02")
	s.Phase("ready")
	s.Wait(OnRollout, "shop/orders", "0 of 1", false)
	s.Object("v1", "Namespace/shop", Applied)
	s.Pod("Deployment/shop/orders", "orders-1", true)
	s.Forward("shop/orders", 80, "orders-1", "127.0.0.1:1")
	s.Report("kubeharness cluster=kh-x")
	s.Return(false)
	if s != nil || r.Root() != nil || s.Root() != nil || r.Close() != nil {
		t.Fatal("nil recorder handed out a scope or failed to close")
	}
	base := http.DefaultTransport
	if s.RoundTripper(base, ViaClient) != base {
		t.Fatal("nil scope wrapped the transport")
	}
	if From(context.Background()) != nil || From(With(context.Background(), nil)) != nil {
		t.Fatal("From found a scope that was never attached")
	}
}

func TestAScopeTravelsOnTheContext(t *testing.T) {
	var buf bytes.Buffer
	s := Start(&buf, "T", "kh-x").Call(FnWaitReady, "shop/orders")
	From(With(context.Background(), s)).Wait(OnRollout, "shop/orders", "0 of 1 updated replicas available", false)
	ls := lines(t, buf.Bytes())
	if w := ofKind(ls, "wait"); len(w) != 1 || w[0]["in"] != ls[1]["id"] {
		t.Fatalf("wait lines = %v", w)
	}
}

func TestAWaitWritesOnlyWhenItsObservationChanges(t *testing.T) {
	var buf bytes.Buffer
	r := Start(&buf, "T", "kh-x")
	first, second := r.Call(FnAwaitRoute, "shop/pay"), r.Call(FnAwaitRoute, "shop/pay")
	for _, o := range []struct {
		s       *Scope
		subject string
		state   string
		done    bool
	}{
		{first, "shop/pay", "check: ResolvedRefs=False", false},
		{first, "shop/pay", "check: ResolvedRefs=False", false},
		{first, "shop/pay", "check: ResolvedRefs=False", false},
		{first, "shop/other", "check: ResolvedRefs=False", false},
		{first, "shop/pay", "Accepted=True ResolvedRefs=True", false},
		{first, "shop/pay", "Accepted=True ResolvedRefs=True", true},
		{first, "shop/pay", "Accepted=True ResolvedRefs=True", true},
		{second, "shop/pay", "Accepted=True ResolvedRefs=True", true},
	} {
		o.s.Wait(OnRoute, o.subject, o.state, o.done)
	}
	var got []string
	for _, l := range ofKind(lines(t, buf.Bytes()), "wait") {
		got = append(got, fmt.Sprintf("%v %v %v %v", l["in"], l["subject"], l["state"], l["done"]))
	}
	want := []string{
		"1 shop/pay check: ResolvedRefs=False <nil>",
		"1 shop/other check: ResolvedRefs=False <nil>",
		"1 shop/pay Accepted=True ResolvedRefs=True <nil>",
		"1 shop/pay Accepted=True ResolvedRefs=True true",
		"2 shop/pay Accepted=True ResolvedRefs=True true",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("wait lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestTheRoundTripperRecordsEachExchange(t *testing.T) {
	body := `{"backend":"payments","path":"/payments","host":"shop.example"}` + strings.Repeat(" ", 300)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/denied" {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, "denied")
			return
		}
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	var buf bytes.Buffer
	r := Start(&buf, "T", "kh-x")
	s := r.Call(FnAwaitResponse, "GET "+srv.URL+"/payments")
	base := &http.Transport{}
	t.Cleanup(base.CloseIdleConnections)
	client := &http.Client{Transport: s.RoundTripper(base, ViaAwait)}

	resp, err := client.Get(srv.URL + "/payments?n=1")
	if err != nil {
		t.Fatal(err)
	}
	read, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(read) != body {
		t.Fatalf("caller read %q", read)
	}
	resp, err = r.Root().RoundTripper(base.Clone(), ViaClient).RoundTrip(mustGet(t, srv.URL+"/denied"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	srv.Close()
	if _, err := client.Get(srv.URL + "/payments"); err == nil {
		t.Fatal("request to a closed server succeeded")
	}

	ls := lines(t, buf.Bytes())
	reqs, resps, bodies := ofKind(ls, "request"), ofKind(ls, "response"), ofKind(ls, "body")
	if len(reqs) != 3 || len(resps) != 3 || len(bodies) != 1 {
		t.Fatalf("got %d requests, %d responses, %d bodies:\n%s", len(reqs), len(resps), len(bodies), buf.String())
	}
	callID := ls[1]["id"]
	if reqs[0]["in"] != callID || reqs[0]["via"] != "await" || reqs[0]["method"] != "GET" || reqs[0]["url"] != srv.URL+"/payments" {
		t.Errorf("first request = %v", reqs[0])
	}
	if reqs[1]["in"] != nil || reqs[1]["via"] != "client" {
		t.Errorf("root request = %v", reqs[1])
	}
	for i := range reqs {
		if resps[i]["id"] != reqs[i]["id"] || resps[i]["ms"] == nil {
			t.Errorf("response %d = %v for request %v", i, resps[i], reqs[i])
		}
	}
	if resps[0]["status"] != float64(200) || resps[1]["status"] != float64(500) || resps[2]["status"] != nil || resps[2]["err"] == nil {
		t.Errorf("responses = %v", resps)
	}
	if bodies[0]["id"] != reqs[0]["id"] || bodies[0]["head"] != body[:HeadLimit] {
		t.Errorf("body = %v, want the first %d bytes the caller read", bodies[0], HeadLimit)
	}
}

func mustGet(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestAFailedWriteDisablesTheTraceAndCloseReportsIt(t *testing.T) {
	w := &failAfter{n: 2}
	r := Start(w, "T", "kh-x")
	s := r.Call(FnNew, "")
	s.Phase("validating")
	s.Phase("provisioning")
	if err := r.Close(); err == nil || w.writes != 3 {
		t.Fatalf("Close = %v after %d writes, want the write error after 3", err, w.writes)
	}
}

type failAfter struct{ n, writes int }

func (f *failAfter) Write(p []byte) (int, error) {
	f.writes++
	if f.writes > f.n {
		return 0, errors.New("disk full")
	}
	return len(p), nil
}

func golden(w io.Writer) {
	c := newClock(0)
	r := start(w, c.now, "TestG02ReferenceGrant/gateway-api", "kh-7630e8a319a4-1")
	at := func(ms int) { c.advance(time.Duration(ms) * time.Millisecond) }
	s := r.Call(FnGateway, "shop/edge")
	at(1)
	s.Phase("ready")
	at(7)
	s.Wait(OnGateway, "shop/edge", "Programmed=False reason=AddressNotAssigned: No addresses have been assigned to the Gateway", false)
	at(12)
	s.Object("gateway.networking.k8s.io/v1", "HTTPRoute/shop/pay", Applied)
	at(3)
	s.Pod("Deployment/payments/payments", "payments-7c9d5b8f4-x8kqz", true)
	at(70)
	s.Forward("payments/payments", 80, "payments-7c9d5b8f4-x8kqz", "127.0.0.1:53021")
	at(1)
	client := &http.Client{Transport: s.RoundTripper(roundTripFunc(func(*http.Request) (*http.Response, error) {
		at(19)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"backend":"payments","path":"/payments","host":"shop.example"}`))}, nil
	}), ViaAwait)}
	resp, _ := client.Get("http://shop.example/payments")
	io.ReadAll(resp.Body)
	at(1)
	s.Report("kubeharness cluster=kh-7630e8a319a4-1 phase=ready cause=ok cleanup=succeeded remaining=0 create=10.7s ready=17.4s delete=390ms profile=gateway-api install=15.7s")
	s.Return(false)
}

func TestGoldenHoldsOneLineOfEveryKind(t *testing.T) {
	var buf bytes.Buffer
	golden(&buf)
	path := filepath.Join("testdata", "golden.jsonl")
	if *update {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/trace -update)", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("trace differs from %s; if the change is intended, bump Version when a field changes meaning and run go test ./internal/trace -update\ngot:\n%s", path, buf.String())
	}
	kinds := map[string]int{}
	for _, l := range lines(t, want) {
		kinds[l["ev"].(string)]++
	}
	all := []ev{evTrace, evCall, evReturn, evPhase, evWait, evObject, evPod, evForward, evRequest, evResponse, evBody, evReport}
	for _, k := range all {
		if kinds[string(k)] != 1 {
			t.Errorf("golden has %d %q lines, want 1", kinds[string(k)], k)
		}
	}
	if len(kinds) != len(all) {
		t.Errorf("golden kinds = %v", kinds)
	}
}

func BenchmarkWrite1000Events(b *testing.B) {
	for _, sink := range []string{"file", "discard"} {
		b.Run(sink, func(b *testing.B) {
			var w io.Writer = io.Discard
			if sink == "file" {
				f, err := os.Create(filepath.Join(b.TempDir(), FileName("kh-bench")))
				if err != nil {
					b.Fatal(err)
				}
				defer f.Close()
				w = f
			}
			r := Start(w, "BenchmarkWrite1000Events", "kh-bench")
			s := r.Call(FnInstallGateway, "gateway-api")
			rt := s.RoundTripper(roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 500, Body: http.NoBody}, nil
			}), ViaClient)
			req := mustGetB(b, "http://shop.example/payments")
			n := 0
			for b.Loop() {
				for i := 0; i < 1000; i += 4 {
					n++
					s.Wait(OnRollout, "envoy-gateway-system/envoy-gateway", fmt.Sprintf("%d of 1 updated replicas available", n), false)
					s.Object("apiextensions.k8s.io/v1", "CustomResourceDefinition/httproutes.gateway.networking.k8s.io", Applied)
					rt.RoundTrip(req)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*1000), "ns/event")
			if err := r.Close(); err != nil {
				b.Fatal(err)
			}
		})
	}
}

func mustGetB(b *testing.B, url string) *http.Request {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		b.Fatal(err)
	}
	return req
}
