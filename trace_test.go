package kubeharness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ogarciacar/kubeharness/internal/lifecycle/lifecycletest"
	"github.com/ogarciacar/kubeharness/internal/trace"
)

func recordTo(buf *bytes.Buffer, kubeconfig *string) func(cluster, kc string) *trace.Recorder {
	return func(cluster, kc string) *trace.Recorder {
		*kubeconfig = kc
		return trace.Start(buf, "TestFake", cluster, kc)
	}
}

func traceLines(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, raw := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		var l map[string]any
		if err := json.Unmarshal(raw, &l); err != nil {
			t.Fatalf("trace line is not JSON: %v\n%s", err, raw)
		}
		out = append(out, l)
	}
	return out
}

func outline(ls []map[string]any) []string {
	var out []string
	for _, l := range ls {
		s := fmt.Sprint(l["ev"])
		for _, k := range []string{"fn", "arg", "phase", "failed", "cluster"} {
			if v, ok := l[k]; ok {
				s += fmt.Sprintf(" %v", v)
			}
		}
		out = append(out, s)
	}
	return out
}

func TestTheTraceFollowsTheRunAndEndsWithTheReportLine(t *testing.T) {
	var buf bytes.Buffer
	var kubeconfig string
	tb := &fakeTB{dir: t.TempDir()}
	var name string
	tb.run(func() {
		h := newHarness(tb, lifecycletest.NewFake(), readyOK, fastTimeouts, recordTo(&buf, &kubeconfig))
		name = h.ClusterName()
		h.install = (&fakeInstall{}).install
		h.InstallGateway(EnvoyGateway())
		h.Apply("testdata/missing")
	})

	ls := traceLines(t, buf.Bytes())
	want := []string{
		"trace " + name,
		"call New",
		"phase validating",
		"phase provisioning",
		"phase wait-usable",
		"phase ready",
		"return",
		"call InstallGateway gateway-api",
		"return",
		"call Apply testdata/missing",
		"return true",
		"call Cleanup test-failed",
		"report",
		"return",
	}
	if got := outline(ls); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("trace outline:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(tb.logs) != 1 || ls[12]["line"] != tb.logs[0] {
		t.Fatalf("report line %q, t.Log printed %q", ls[12]["line"], tb.logs)
	}
	if ls[12]["in"] != ls[11]["id"] || ls[13]["id"] != ls[11]["id"] {
		t.Fatalf("report and return do not belong to the Cleanup call: %v", ls[11:])
	}
	if kubeconfig == "" || strings.Contains(buf.String(), kubeconfig) {
		t.Fatalf("trace mentions the kubeconfig path %q:\n%s", kubeconfig, buf.String())
	}
}

func TestGatewayRequestsAreTracedWithTheCallTheyBelongTo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "orders")
		fmt.Fprint(w, `{"backend":"orders"}`)
	}))
	t.Cleanup(srv.Close)
	var buf bytes.Buffer
	var kubeconfig string
	tb := awaitStopTB(t)
	harness := func(body func(h *Harness)) {
		tb.run(func() {
			h := newHarness(tb, lifecycletest.NewFake(), readyOK, fastTimeouts, recordTo(&buf, &kubeconfig))
			h.install = (&fakeInstall{}).install
			body(h)
		})
	}
	harness(func(h *Harness) {
		g := useGateway(h, gatewayDynamic(t, nil))
		g.root = h.rec.Root()
		g.transport = &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
			},
		}
		t.Cleanup(g.transport.CloseIdleConnections)
		req, _ := http.NewRequest(http.MethodGet, "http://shop.example/orders?debug=1", nil)
		g.AwaitResponse(req, func(r *http.Response) error {
			if r.Header.Get("X-Backend") != "orders" {
				return fmt.Errorf("%d", r.StatusCode)
			}
			return nil
		})
		resp, err := g.Client().Get("http://shop.example/payments")
		if err != nil {
			t.Error(err)
			return
		}
		resp.Body.Close()
	})
	if len(tb.fatals) != 0 {
		t.Fatalf("fatals = %q", tb.fatals)
	}

	var await float64
	var got []string
	for _, l := range traceLines(t, buf.Bytes()) {
		switch l["ev"] {
		case "call":
			if l["fn"] == "AwaitResponse" {
				await = l["id"].(float64)
				got = append(got, fmt.Sprintf("call AwaitResponse %v", l["arg"]))
			}
		case "request":
			got = append(got, fmt.Sprintf("request in=%v via=%v %v %v", l["in"] == await, l["via"], l["method"], l["url"]))
		case "response":
			got = append(got, fmt.Sprintf("response %v", l["status"]))
		case "body":
			got = append(got, fmt.Sprintf("body %v", l["head"]))
		case "wait":
			got = append(got, fmt.Sprintf("wait in=%v %v %v %v %v", l["in"] == await, l["on"], l["subject"], l["state"], l["done"]))
		}
	}
	want := []string{
		"call AwaitResponse GET http://shop.example/orders",
		"request in=true via=await GET http://shop.example/orders",
		"response 200",
		`body {"backend":"orders"}`,
		"wait in=true response GET http://shop.example/orders 200 OK true",
		"request in=false via=client GET http://shop.example/payments",
		"response 200",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("gateway lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
