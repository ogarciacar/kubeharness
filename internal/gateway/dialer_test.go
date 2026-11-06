package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ogarciacar/kubeharness/internal/kube"
)

func TestDialerPicksTheListenerByPortAndIgnoresTheHost(t *testing.T) {
	httpFwd := mustListen(t)
	httpsFwd := mustListen(t)
	d := mustDialer(t, func(_ context.Context, port int32) (forward, error) {
		if port == 443 {
			return httpsFwd, nil
		}
		return httpFwd, nil
	}, 80, 443)
	defer d.Close()

	ctx := context.Background()
	httpConn, err := d.DialContext(ctx, "tcp", "shop.example:80")
	if err != nil {
		t.Fatal(err)
	}
	defer httpConn.Close()
	httpsConn, err := d.DialContext(ctx, "udp", "other.example:443")
	if err != nil {
		t.Fatal(err)
	}
	defer httpsConn.Close()
	if httpConn.RemoteAddr().String() != httpFwd.addr || httpsConn.RemoteAddr().String() != httpsFwd.addr {
		t.Fatalf("dialed %s and %s, listeners are %s and %s", httpConn.RemoteAddr(), httpsConn.RemoteAddr(), httpFwd.addr, httpsFwd.addr)
	}
}

func TestDialerRejectsAnUnknownPort(t *testing.T) {
	d := mustDialer(t, func(context.Context, int32) (forward, error) {
		return mustListen(t), nil
	}, 80, 443)
	defer d.Close()
	_, err := d.DialContext(context.Background(), "tcp", "shop.example:8080")
	if err == nil || err.Error() != "gateway default/edge has no listener on port 8080; listeners: [80 443]" {
		t.Fatalf("dial = %v", err)
	}
}

func TestDialerReplacesAStoppedForwardBeforeDialing(t *testing.T) {
	stopped := mustListen(t)
	stopped.stopped.Store(true)
	var replacement *listenForward
	var opens atomic.Int32
	d := mustDialer(t, func(context.Context, int32) (forward, error) {
		if opens.Add(1) == 1 {
			return stopped, nil
		}
		replacement = mustListen(t)
		return replacement, nil
	}, 80)
	defer d.Close()

	conn, err := d.DialContext(context.Background(), "tcp", "shop.example:80")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if opens.Load() != 2 || conn.RemoteAddr().String() != replacement.addr || conn.RemoteAddr().String() == stopped.addr {
		t.Fatalf("opens %d dialed %s, stopped %s replacement %s", opens.Load(), conn.RemoteAddr(), stopped.addr, replacement.addr)
	}
}

func TestDialerReforwardsARefusedDialOnce(t *testing.T) {
	var opens atomic.Int32
	var live *listenForward
	d := mustDialer(t, func(context.Context, int32) (forward, error) {
		f := mustListen(t)
		if opens.Add(1) == 1 {
			f.refuse()
			return f, nil
		}
		live = f
		return f, nil
	}, 80)
	defer d.Close()

	ctx := context.Background()
	conn, err := d.DialContext(ctx, "tcp", "shop.example:80")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if opens.Load() != 2 || conn.RemoteAddr().String() != live.addr {
		t.Fatalf("opens %d dialed %s, live %v", opens.Load(), conn.RemoteAddr(), live)
	}
	again, err := d.DialContext(ctx, "tcp", "shop.example:80")
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
	if opens.Load() != 2 {
		t.Fatalf("second dial opened another forward: opens %d", opens.Load())
	}
}

func TestConcurrentRefusedDialsOpenOneReplacement(t *testing.T) {
	var opens atomic.Int32
	var mu sync.Mutex
	var live *listenForward
	d := mustDialer(t, func(context.Context, int32) (forward, error) {
		f := mustListen(t)
		if opens.Add(1) == 1 {
			f.refuse()
			return f, nil
		}
		mu.Lock()
		live = f
		mu.Unlock()
		return f, nil
	}, 80)
	defer d.Close()

	start := make(chan struct{})
	var wg sync.WaitGroup
	errc := make(chan error, 2)
	addrs := make(chan string, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			conn, err := d.DialContext(context.Background(), "tcp", "shop.example:80")
			if err != nil {
				errc <- err
				return
			}
			addrs <- conn.RemoteAddr().String()
			conn.Close()
			errc <- nil
		}()
	}
	close(start)
	wg.Wait()
	close(errc)
	close(addrs)
	for err := range errc {
		if err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for addr := range addrs {
		got = append(got, addr)
	}
	mu.Lock()
	want := ""
	if live != nil {
		want = live.addr
	}
	mu.Unlock()
	if opens.Load() != 2 || len(got) != 2 || got[0] != want || got[1] != want {
		t.Fatalf("opens %d dialed %v, live %s", opens.Load(), got, want)
	}
}

func TestCancelledReforwardLeavesTheSlotRetryable(t *testing.T) {
	var opens atomic.Int32
	var live *listenForward
	d := mustDialerBudget(t, 150*time.Millisecond, func(ctx context.Context, port int32) (forward, error) {
		switch opens.Add(1) {
		case 1:
			f := mustListen(t)
			f.refuse()
			return f, nil
		case 2:
			<-ctx.Done()
			return nil, ctx.Err()
		default:
			live = mustListen(t)
			return live, nil
		}
	}, 80)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := d.DialContext(ctx, "tcp", "shop.example:80"); err == nil {
		t.Fatal("cancelled re-forward returned a connection")
	}
	conn, err := d.DialContext(ctx, "tcp", "shop.example:80")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if opens.Load() != 3 || conn.RemoteAddr().String() != live.addr {
		t.Fatalf("opens %d dialed %s, live %v", opens.Load(), conn.RemoteAddr(), live)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCloseAbortsAnInFlightReforward(t *testing.T) {
	entered := make(chan struct{})
	var opens atomic.Int32
	d := mustDialerBudget(t, 30*time.Second, func(ctx context.Context, port int32) (forward, error) {
		if opens.Add(1) == 1 {
			f := mustListen(t)
			f.refuse()
			return f, nil
		}
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}, 80)

	errc := make(chan error, 1)
	go func() {
		_, err := d.DialContext(context.Background(), "tcp", "shop.example:80")
		errc <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("re-forward did not start")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("in-flight dial succeeded after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not abort the re-forward")
	}
	_, err := d.DialContext(context.Background(), "tcp", "shop.example:80")
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("dial after Close = %v", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	boom := errors.New("listener already shut")
	d := mustDialer(t, func(context.Context, int32) (forward, error) {
		f := mustListen(t)
		return &errForward{listenForward: f, closeErr: boom}, nil
	}, 80)
	first := d.Close()
	second := d.Close()
	if !errors.Is(first, boom) || !errors.Is(second, boom) {
		t.Fatalf("Close = %v then %v", first, second)
	}
}

func TestOpenPortRetriesAMissingPodAndStopsOnOtherErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, err := openPort(ctx, func(context.Context, int32) (forward, error) {
		return nil, errors.New("service envoy has no selector")
	}, 80)
	if err == nil || !strings.Contains(err.Error(), "no selector") || time.Since(start) > time.Second {
		t.Fatalf("openPort = %v after %s", err, time.Since(start))
	}

	var opens atomic.Int32
	f, err := openPort(ctx, func(context.Context, int32) (forward, error) {
		if opens.Add(1) < 3 {
			return nil, errors.New("service envoy has no ready pod")
		}
		return mustListen(t), nil
	}, 80)
	if err != nil || f == nil || opens.Load() != 3 {
		t.Fatalf("openPort opens %d err %v forward %v", opens.Load(), err, f)
	}
	_ = f.Close()
}

func TestTransportDialsTheForwardDespiteTheProxyEnv(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Host + " " + r.URL.Path
		w.Header().Set("X-Backend", "orders")
		w.WriteHeader(http.StatusOK)
	})}
	go srv.Serve(ln)

	d := mustDialer(t, func(context.Context, int32) (forward, error) {
		return &listenForward{addr: ln.Addr().String(), ln: ln}, nil
	}, 80)
	tr := Transport(d)
	tr.DisableKeepAlives = true
	client := &http.Client{Transport: tr}
	resp, err := client.Get("http://shop.example/orders")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Backend") != "orders" || len(body) != 0 {
		t.Fatalf("response %s backend %q body %q", resp.Status, resp.Header.Get("X-Backend"), body)
	}
	select {
	case host := <-got:
		if host != "shop.example /orders" {
			t.Fatalf("request = %q", host)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server saw no request")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get("http://shop.example/orders"); !errors.Is(err, ErrClosed) {
		t.Fatalf("get after Close = %v", err)
	}
}

type listenForward struct {
	addr    string
	ln      net.Listener
	stopped atomic.Bool
}

func (f *listenForward) Address() string { return f.addr }
func (f *listenForward) Stopped() bool   { return f.stopped.Load() }
func (f *listenForward) Close() error {
	f.stopped.Store(true)
	if f.ln == nil {
		return nil
	}
	err := f.ln.Close()
	// refuse() already closed the listener. Closing it again is the dialer
	// dropping a forward it will not dial.
	if err != nil && strings.Contains(err.Error(), "use of closed network connection") {
		return nil
	}
	return err
}

func (f *listenForward) refuse() { _ = f.ln.Close() }

type errForward struct {
	*listenForward
	closeErr error
}

func (f *errForward) Close() error {
	_ = f.listenForward.Close()
	return f.closeErr
}

func mustListen(t *testing.T) *listenForward {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return &listenForward{addr: ln.Addr().String(), ln: ln}
}

func mustDialer(t *testing.T, open opener, ports ...int32) *Dialer {
	t.Helper()
	return mustDialerBudget(t, 2*time.Second, open, ports...)
}

func mustDialerBudget(t *testing.T, budget time.Duration, open opener, ports ...int32) *Dialer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d, err := startForwards(ctx, Target{Gateway: edge, Service: kube.Ref{Namespace: "envoy-gateway-system", Name: "envoy"}, Ports: ports}, budget, open)
	if err != nil || d == nil {
		t.Fatalf("startForwards = %v %v", d, err)
	}
	return d
}
