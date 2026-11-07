//go:build e2e

package gateway_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/ogarciacar/kubeharness"
)

const (
	g03Route           = "shop/checkout"
	g03URL             = "http://shop.example/checkout"
	g03TrafficInterval = 50 * time.Millisecond
	g03RequestTimeout  = 2 * time.Second
)

func TestG03WeightFlip(t *testing.T) {
	for _, p := range profiles {
		t.Run(p.Name(), func(t *testing.T) {
			flips := g03Flips(t)
			h := kubeharness.New(t)
			gc := h.InstallGateway(p)
			buildBackendImage(t)
			h.LoadImage(backendImage)
			h.Apply("testdata/g03")
			h.WaitReady("shop/blue")
			h.WaitReady("shop/green")
			gw := gc.Gateway("shop/edge")

			marks := []mark{flipTo(t, h, gw, flips[0])}
			stop := startTraffic(t, gw.Client(), g03URL)
			for _, f := range flips[1:] {
				time.Sleep(g03Hold)
				marks = append(marks, flipTo(t, h, gw, f))
			}
			time.Sleep(g03Hold)

			samples, end := stop()
			transitions, err := judge(samples, marks, end)
			for _, tr := range transitions {
				t.Log(tr)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func flipTo(t *testing.T, h *kubeharness.Harness, gw *kubeharness.Gateway, f flip) mark {
	t.Helper()
	m := mark{to: f.backend, applied: time.Now()}
	h.Apply(f.dir)
	gw.AwaitRoute(g03Route, kubeharness.RouteReady)
	m.status = time.Now()
	gw.AwaitResponse(get(g03URL), servedBy(f.backend))
	m.ready = time.Now()
	return m
}

func startTraffic(t *testing.T, client *http.Client, url string) (stop func() ([]sample, time.Time)) {
	t.Helper()
	c := *client
	c.Timeout = g03RequestTimeout
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan []sample, 1)
	go func() {
		var out []sample
		tick := time.NewTicker(g03TrafficInterval)
		defer tick.Stop()
		for {
			s := sample{sent: time.Now()}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				s.err = err.Error()
				out = append(out, s)
			} else {
				req.Close = true
				resp, doErr := c.Do(req)
				if ctx.Err() != nil {
					closeBody(resp)
					done <- out
					return
				}
				if doErr != nil {
					s.err = doErr.Error()
					closeBody(resp)
				} else {
					s.status = resp.StatusCode
					backend, readErr := readBackend(resp)
					if readErr != nil {
						s.backend = ""
						s.err = readErr.Error()
					} else {
						s.backend = backend
					}
					closeBody(resp)
				}
				out = append(out, s)
			}
			select {
			case <-ctx.Done():
				done <- out
				return
			case <-tick.C:
			}
		}
	}()
	stop = sync.OnceValues(func() ([]sample, time.Time) {
		end := time.Now()
		cancel()
		return <-done, end
	})
	t.Cleanup(func() {
		samples, end := stop()
		if t.Failed() {
			t.Logf("traffic before failure (last 5s): %s", tail(samples, end, 5*time.Second))
		}
	})
	return stop
}

func closeBody(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
}
