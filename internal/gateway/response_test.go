package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ogarciacar/kubeharness/internal/kube"
)

func TestSummarize(t *testing.T) {
	long := bytes.Repeat([]byte("a"), 250)
	cases := []struct {
		name          string
		resp          *http.Response
		body          []byte
		err, checkErr error
		want          string
	}{
		{
			name: "transport error",
			err:  errors.New("dial: gateway default/edge has no listener on port 8080; listeners: [80]"),
			want: `last attempt: dial: gateway default/edge has no listener on port 8080; listeners: [80]`,
		},
		{
			name: "transport error is one line",
			err:  errors.New("dial failed\nmore detail"),
			want: "last attempt: dial failed more detail",
		},
		{
			name:     "status and check are whatever arrived",
			resp:     &http.Response{Status: "502 Bad Gateway", StatusCode: 502},
			body:     []byte("upstream"),
			checkErr: errors.New(`502 from "", want 200 from "orders"`),
			want:     `last response: 502 Bad Gateway; body "upstream"; check: 502 from "", want 200 from "orders"`,
		},
		{
			name:     "a different status uses the same line",
			resp:     &http.Response{Status: "200 OK", StatusCode: 200},
			body:     []byte("ok"),
			checkErr: errors.New("want a response the backend did not serve"),
			want:     `last response: 200 OK; body "ok"; check: want a response the backend did not serve`,
		},
		{
			name:     "body quote keeps the first 200 bytes",
			resp:     &http.Response{Status: "404 Not Found", StatusCode: 404},
			body:     long,
			checkErr: errors.New("no"),
			want:     `last response: 404 Not Found; body "` + strings.Repeat("a", 200) + `"; check: no`,
		},
		{
			name:     "empty body",
			resp:     &http.Response{Status: "204 No Content", StatusCode: 204},
			checkErr: errors.New("empty"),
			want:     `last response: 204 No Content; body ""; check: empty`,
		},
		{
			name: "no check clause when check accepted",
			resp: &http.Response{StatusCode: 418},
			body: []byte("teapot"),
			want: `last response: 418; body "teapot"`,
		},
		{
			name: "neither a response nor an error",
			want: "last attempt: no response",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := summarize(c.resp, c.body, c.err, c.checkErr)
			if got != c.want {
				t.Fatalf("summarize = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAttemptReplaysTheBodyAndBuffersTheResponse(t *testing.T) {
	const payload = "payload"
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = append(seen, string(b))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "from-"+string(b))
	}))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	resp, body, err := attempt(context.Background(), srv.Client(), req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "from-"+payload || !bytes.Equal(again, body) || len(seen) != 1 || seen[0] != payload {
		t.Fatalf("body %q again %q seen %q", body, again, seen)
	}
	left, err := io.ReadAll(req.Body)
	if err != nil || string(left) != payload {
		t.Fatalf("original body = %q %v", left, err)
	}
}

func TestAttemptStopsAtOneMiB(t *testing.T) {
	const oneMiB = 1 << 20
	payload := bytes.Repeat([]byte("x"), oneMiB+32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, body, err := attempt(context.Background(), srv.Client(), req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := io.ReadAll(resp.Body)
	if err != nil || len(body) != oneMiB || !bytes.Equal(again, body) || !bytes.Equal(body, payload[:oneMiB]) {
		t.Fatalf("len %d err %v", len(body), err)
	}
}

func TestAwaitResponseStopsWhenTheBodyCannotBeReplayed(t *testing.T) {
	hit := false
	req, err := http.NewRequest(http.MethodPost, "http://shop.example/orders", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Body = io.NopCloser(strings.NewReader("once"))
	req.GetBody = nil
	req.ContentLength = 4

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	start := time.Now()
	tr := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			hit = true
			return nil, errors.New("dialed")
		},
	}
	got := AwaitResponse(ctx, context.Background(), responseCluster(), responseTarget, tr, req, func(*http.Response) error {
		t.Error("check ran")
		return nil
	})
	if hit || got == nil || errors.Is(got, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("hit %v after %s: %v", hit, time.Since(start), got)
	}
	for _, want := range []string{"request body cannot be replayed", "proxy envoy-gateway-system/envoy-edge"} {
		if !strings.Contains(got.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, got)
		}
	}
}

func TestAwaitResponseRetriesUntilCheckAccepts(t *testing.T) {
	const payload = "payload"
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = append(seen, string(b))
		if len(seen) == 1 {
			http.Error(w, "later", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("X-Backend", "orders")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	tr := &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) { return nil, nil },
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}
	req, err := http.NewRequest(http.MethodPost, "http://shop.example/orders", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err = AwaitResponse(ctx, context.Background(), responseCluster(), responseTarget, tr, req, func(resp *http.Response) error {
		if resp.StatusCode == http.StatusNoContent && resp.Header.Get("X-Backend") == "orders" {
			return nil
		}
		buf, _ := io.ReadAll(resp.Body)
		return errors.New(resp.Status + " " + string(buf))
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != payload || seen[1] != payload {
		t.Fatalf("bodies = %q", seen)
	}
	left, err := io.ReadAll(req.Body)
	if err != nil || string(left) != payload {
		t.Fatalf("original body = %q %v", left, err)
	}
}

func TestAwaitResponseReportsWhateverStatusArrived(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(418)
		_, _ = io.WriteString(w, "teapot")
	}))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/orders", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	got := AwaitResponse(ctx, context.Background(), responseCluster(), responseTarget, dialTransport(srv.Listener.Addr().String()), req, func(resp *http.Response) error {
		return errors.New("status " + resp.Status)
	})
	if got == nil || !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("418 = %v", got)
	}
	for _, want := range []string{
		`last response: 418 I'm a teapot; body "teapot"; check: status 418 I'm a teapot`,
		"proxy envoy-gateway-system/envoy-edge",
	} {
		if !strings.Contains(got.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, got)
		}
	}
}

func TestAwaitResponseReportsADialError(t *testing.T) {
	tr := &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) { return nil, nil },
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial: gateway default/edge has no listener on port 8080; listeners: [80]")
		},
	}
	req, err := http.NewRequest(http.MethodGet, "http://shop.example/orders", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	got := AwaitResponse(ctx, context.Background(), responseCluster(), responseTarget, tr, req, func(*http.Response) error {
		t.Error("check ran")
		return nil
	})
	if got == nil || !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("dial = %v", got)
	}
	for _, want := range []string{
		"last attempt:",
		"dial: gateway default/edge has no listener on port 8080; listeners: [80]",
	} {
		if !strings.Contains(got.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, got)
		}
	}
}

func TestAwaitResponseCapsAHungDial(t *testing.T) {
	// net/http keeps dialing after the request is canceled. The attempt still
	// has to release a hung dial in about 5s. This wait itself has no deadline.
	finished := make(chan time.Duration, 1)
	tr := &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) { return nil, nil },
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			start := time.Now()
			<-ctx.Done()
			select {
			case finished <- time.Since(start):
			default:
			}
			return nil, ctx.Err()
		},
	}
	req, err := http.NewRequest(http.MethodGet, "http://shop.example/orders", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = AwaitResponse(ctx, context.Background(), responseCluster(), responseTarget, tr, req, func(*http.Response) error {
			return errors.New("unexpected response")
		})
	}()
	select {
	case elapsed := <-finished:
		cancel()
		<-done
		if elapsed < 4*time.Second || elapsed > 8*time.Second {
			t.Fatalf("hung dial returned after %s, want about 5s", elapsed)
		}
	case <-time.After(10 * time.Second):
		cancel()
		<-done
		t.Fatal("hung dial was not capped at 5s")
	}
}

func TestAwaitResponseSurfacesAGetBodyError(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://shop.example/orders", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	req.GetBody = func() (io.ReadCloser, error) { return nil, errors.New("body gone") }
	tr := &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) { return nil, nil },
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			t.Error("dialed after GetBody failed")
			return nil, errors.New("dialed")
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	got := AwaitResponse(ctx, context.Background(), responseCluster(), responseTarget, tr, req, func(*http.Response) error {
		t.Error("check ran")
		return nil
	})
	if got == nil || !errors.Is(got, context.DeadlineExceeded) || !strings.Contains(got.Error(), "last attempt: body gone") {
		t.Fatalf("GetBody = %v", got)
	}
}

func dialTransport(addr string) *http.Transport {
	return &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) { return nil, nil },
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
}

func responseCluster() Cluster {
	cl := clusterWith()
	cl.Controller = envoyController
	return cl
}

var responseTarget = Target{
	Gateway: edge,
	Service: kube.Ref{Namespace: "envoy-gateway-system", Name: "envoy-edge"},
	Ports:   []int32{80},
}
