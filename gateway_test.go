package kubeharness

import (
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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/ogarciacar/kubeharness/internal/gateway"
	"github.com/ogarciacar/kubeharness/internal/kube"
	"github.com/ogarciacar/kubeharness/internal/lifecycle/lifecycletest"
	"github.com/ogarciacar/kubeharness/internal/profile"
)

// fakeInstall records which recipes InstallGateway asked to install.
type fakeInstall struct {
	installed []string
	timeLeft  time.Duration
	err       error
}

func (f *fakeInstall) install(ctx, _ context.Context, _ *rest.Config, _ kubernetes.Interface, r *profile.Recipe) error {
	f.installed = append(f.installed, r.Name)
	d, _ := ctx.Deadline()
	f.timeLeft = time.Until(d)
	return f.err
}

func harnessWithInstall(tb *fakeTB, f *fakeInstall) func(body func(h *Harness)) {
	return func(body func(h *Harness)) {
		tb.run(func() {
			h := newHarness(tb, lifecycletest.NewFake(), readyOK, fastTimeouts)
			h.install = f.install
			body(h)
		})
	}
}

func TestInstallGatewayTwiceWithTheSameProfileInstallsOnce(t *testing.T) {
	tb := &fakeTB{dir: t.TempDir()}
	f := &fakeInstall{}
	var first, second *GatewayController
	harnessWithInstall(tb, f)(func(h *Harness) {
		first = h.InstallGateway(EnvoyGateway())
		second = h.InstallGateway(EnvoyGateway())
	})
	if len(tb.fatals) != 0 {
		t.Fatalf("fatals = %q", tb.fatals)
	}
	if len(f.installed) != 1 || first == nil || first != second {
		t.Fatalf("installs = %v, same controller = %v", f.installed, first == second)
	}
	if f.timeLeft > 4*time.Minute || f.timeLeft < 4*time.Minute-10*time.Second {
		t.Fatalf("install got %s, want the 4m install timeout", f.timeLeft)
	}
	if len(tb.logs) != 1 || !strings.Contains(tb.logs[0], " profile=gateway-api install=") {
		t.Fatalf("report line = %q", tb.logs)
	}
}

func TestInstallGatewayRefusesASecondProfile(t *testing.T) {
	tb := &fakeTB{dir: t.TempDir()}
	f := &fakeInstall{}
	other := GatewayProfile{&profile.Recipe{Name: "gateway-agentgateway"}}
	harnessWithInstall(tb, f)(func(h *Harness) {
		h.InstallGateway(EnvoyGateway())
		h.InstallGateway(other)
	})
	if len(f.installed) != 1 || len(tb.fatals) != 1 || !strings.Contains(tb.fatals[0], "install gateway gateway-agentgateway") || !strings.Contains(tb.fatals[0], "already has gateway profile gateway-api") {
		t.Fatalf("installs = %v, fatals = %q", f.installed, tb.fatals)
	}
}

func TestInstallGatewayRejectsTheZeroProfile(t *testing.T) {
	tb := &fakeTB{dir: t.TempDir()}
	f := &fakeInstall{}
	harnessWithInstall(tb, f)(func(h *Harness) { h.InstallGateway(GatewayProfile{}) })
	if len(f.installed) != 0 || len(tb.fatals) != 1 || !strings.Contains(tb.fatals[0], "zero GatewayProfile") {
		t.Fatalf("installs = %v, fatals = %q", f.installed, tb.fatals)
	}
	if strings.Contains(tb.logs[0], "profile=") {
		t.Fatalf("report names a profile that was never installed: %s", tb.logs[0])
	}
}

func TestFailedInstallStopsTheTestWithItsEvidenceAndNoProfileInTheReport(t *testing.T) {
	tb := &fakeTB{dir: t.TempDir()}
	f := &fakeInstall{err: errors.New("controller: deployment envoy-gateway not available\n  pod envoy-gateway-1 phase=Pending")}
	harnessWithInstall(tb, f)(func(h *Harness) { h.InstallGateway(EnvoyGateway()) })
	if len(tb.fatals) != 1 || !strings.Contains(tb.fatals[0], "install gateway gateway-api failed after") || !strings.Contains(tb.fatals[0], "pod envoy-gateway-1 phase=Pending") {
		t.Fatalf("fatals = %q", tb.fatals)
	}
	if len(tb.logs) != 1 || strings.Contains(tb.logs[0], "profile=") || !strings.Contains(tb.logs[0], "cause=test-failed cleanup=succeeded") {
		t.Fatalf("report line = %q", tb.logs)
	}
}

func TestInstallGatewayFillsTheClusterFromTheRecipe(t *testing.T) {
	tb := &fakeTB{dir: t.TempDir()}
	f := &fakeInstall{}
	var gc *GatewayController
	harnessWithInstall(tb, f)(func(h *Harness) {
		gc = h.InstallGateway(EnvoyGateway())
	})
	if len(tb.fatals) != 0 || gc == nil || gc.h == nil {
		t.Fatalf("fatals = %q controller = %v", tb.fatals, gc)
	}
	if gc.cl.Config != gc.h.rest || gc.cl.Kube != gc.h.client || gc.cl.Dynamic == nil {
		t.Fatal("InstallGateway did not keep the harness clients")
	}
	if gc.cl.Class != GatewayClass || gc.cl.Controller != gc.r.ControllerName || gc.cl.Controller == "" {
		t.Fatalf("class %q controller %q", gc.cl.Class, gc.cl.Controller)
	}
	if len(gc.cl.ControllerPods) != len(gc.r.Controllers) {
		t.Fatalf("controllers = %+v, recipe %+v", gc.cl.ControllerPods, gc.r.Controllers)
	}
	for i, d := range gc.r.Controllers {
		if gc.cl.ControllerPods[i] != (kube.Ref{Namespace: d.Namespace, Name: d.Name}) {
			t.Fatalf("controller %d = %s, recipe %s/%s", i, gc.cl.ControllerPods[i], d.Namespace, d.Name)
		}
	}
}

func TestGatewayStopsWhenTheGatewayIsMissing(t *testing.T) {
	tb := &fakeTB{dir: t.TempDir()}
	f := &fakeInstall{}
	harnessWithInstall(tb, f)(func(h *Harness) {
		gc := h.InstallGateway(EnvoyGateway())
		gc.cl.Dynamic = dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
			{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}:   "GatewayList",
			{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}: "HTTPRouteList",
		})
		gc.cl.Kube = fake.NewClientset()
		gc.Gateway("edge")
	})
	if len(tb.fatals) != 1 || !strings.Contains(tb.fatals[0], "kubeharness: gateway default/edge:") || !strings.Contains(tb.fatals[0], "not found") {
		t.Fatalf("fatals = %q", tb.fatals)
	}
}

func TestGatewayRejectsABadRef(t *testing.T) {
	tb := &fakeTB{dir: t.TempDir()}
	f := &fakeInstall{}
	harnessWithInstall(tb, f)(func(h *Harness) {
		h.InstallGateway(EnvoyGateway()).Gateway("default/edge/extra")
	})
	if len(tb.fatals) != 1 || !strings.Contains(tb.fatals[0], `gateway "default/edge/extra"`) {
		t.Fatalf("fatals = %q", tb.fatals)
	}
}

func TestGatewayClientDialsTheURLPortOnItsOwnTransport(t *testing.T) {
	var dialed string
	g := &Gateway{transport: &http.Transport{
		DialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
			dialed = addr
			return nil, errors.New("stop")
		},
	}}
	first := g.Client()
	second := g.Client()
	if first == second || first.Transport != g.transport || second.Transport != g.transport {
		t.Fatal("Client did not return a new client over the shared transport")
	}
	if _, err := first.Get("http://shop.example/orders"); err == nil || dialed != "shop.example:80" {
		t.Fatalf("dialed %q err %v", dialed, err)
	}
}

func TestRouteReadyRejectsAStatusAwaitRouteDidNotObserve(t *testing.T) {
	const want = "RouteStatus was not observed by AwaitRoute"
	if err := RouteReady(RouteStatus{}); err == nil || err.Error() != want {
		t.Fatalf("zero RouteStatus = %v", err)
	}
	literal := RouteStatus{
		Accepted:     Condition{Status: "True", Reason: "Accepted", Message: "ok"},
		ResolvedRefs: Condition{Status: "True", Reason: "ResolvedRefs", Message: "ok"},
		fromWait:     false,
	}
	if err := RouteReady(literal); err == nil || err.Error() != want {
		t.Fatalf("literal = %v", err)
	}
}

func TestRouteStatusIsWhatRouteReadyAccepts(t *testing.T) {
	ready := routeStatus(gateway.Conditions{
		{Type: "Accepted", Status: "True", Reason: "Accepted", Message: "Route is accepted"},
		{Type: "ResolvedRefs", Status: "True", Reason: "ResolvedRefs", Message: "resolved"},
	})
	if err := RouteReady(ready); err != nil {
		t.Fatal(err)
	}
	if ready.Accepted != (Condition{Status: "True", Reason: "Accepted", Message: "Route is accepted"}) || ready.ResolvedRefs.Status != "True" {
		t.Fatalf("status = %+v", ready)
	}

	partial := routeStatus(gateway.Conditions{
		{Type: "Accepted", Status: "False", Reason: "NotAllowedByListeners", Message: "no matching listener"},
	})
	if partial.ResolvedRefs != (Condition{}) {
		t.Fatalf("missing condition = %+v", partial.ResolvedRefs)
	}
	const want = "Accepted=False reason=NotAllowedByListeners: no matching listener; ResolvedRefs not reported"
	if err := RouteReady(partial); err == nil || err.Error() != want {
		t.Fatalf("partial = %v", err)
	}
}

func TestAwaitRouteStopsWhenTheRouteIsMissing(t *testing.T) {
	tb := awaitStopTB(t)
	dyn := gatewayDynamic(t, nil)
	harnessWithInstall(tb, &fakeInstall{})(func(h *Harness) {
		useGateway(h, dyn).AwaitRoute("shop", RouteReady)
	})
	if len(tb.fatals) != 1 {
		t.Fatalf("fatals = %q", tb.fatals)
	}
	for _, want := range []string{
		"kubeharness: await route default/shop on gateway default/edge:",
		"route default/shop not found; apply it first",
		"proxy envoy-gateway-system/envoy-edge",
	} {
		if !strings.Contains(tb.fatals[0], want) {
			t.Errorf("fatal lacks %q:\n%s", want, tb.fatals[0])
		}
	}
	if strings.Contains(tb.fatals[0], "deadline") || strings.Contains(tb.fatals[0], "stopped early") {
		t.Fatalf("missing route waited: %s", tb.fatals[0])
	}
}

func TestAwaitRouteAcceptsASettledRoute(t *testing.T) {
	tb := awaitStopTB(t)
	dyn := gatewayDynamic(t, settledRoute(EnvoyGateway().r.ControllerName))
	harnessWithInstall(tb, &fakeInstall{})(func(h *Harness) {
		useGateway(h, dyn).AwaitRoute("shop", RouteReady)
	})
	if len(tb.fatals) != 0 {
		t.Fatalf("fatals = %q", tb.fatals)
	}
}

func TestAwaitRouteRejectsABadRef(t *testing.T) {
	tb := awaitStopTB(t)
	dyn := gatewayDynamic(t, nil)
	harnessWithInstall(tb, &fakeInstall{})(func(h *Harness) {
		useGateway(h, dyn).AwaitRoute("default/shop/extra", RouteReady)
	})
	if len(tb.fatals) != 1 || !strings.Contains(tb.fatals[0], `route "default/shop/extra"`) {
		t.Fatalf("fatals = %q", tb.fatals)
	}
}

func TestAwaitResponseStopsWhenTheBodyCannotBeReplayed(t *testing.T) {
	tb := awaitStopTB(t)
	dyn := gatewayDynamic(t, nil)
	dialed := false
	req, err := http.NewRequest(http.MethodPost, "http://shop.example/orders", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Body = io.NopCloser(strings.NewReader("once"))
	req.GetBody = nil
	harnessWithInstall(tb, &fakeInstall{})(func(h *Harness) {
		g := useGateway(h, dyn)
		g.transport = &http.Transport{
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("dialed")
			},
		}
		g.AwaitResponse(req, func(*http.Response) error { return nil })
	})
	if dialed {
		t.Fatal("dialed a request that cannot be replayed")
	}
	if len(tb.fatals) != 1 {
		t.Fatalf("fatals = %q", tb.fatals)
	}
	for _, want := range []string{
		"kubeharness: await response POST http://shop.example/orders on gateway default/edge:",
		"request body cannot be replayed",
	} {
		if !strings.Contains(tb.fatals[0], want) {
			t.Errorf("fatal lacks %q:\n%s", want, tb.fatals[0])
		}
	}
	if strings.Contains(tb.fatals[0], "deadline") {
		t.Fatalf("unreplayable body waited: %s", tb.fatals[0])
	}
}

func TestAwaitResponseSendsTheRequestThroughTheGateway(t *testing.T) {
	tb := awaitStopTB(t)
	dyn := gatewayDynamic(t, nil)
	var host string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host = r.Host
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	req, err := http.NewRequest(http.MethodGet, "http://shop.example/orders", nil)
	if err != nil {
		t.Fatal(err)
	}
	harnessWithInstall(tb, &fakeInstall{})(func(h *Harness) {
		g := useGateway(h, dyn)
		g.transport = &http.Transport{
			Proxy: func(*http.Request) (*url.URL, error) { return nil, nil },
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
			},
		}
		g.AwaitResponse(req, func(resp *http.Response) error {
			if resp.StatusCode != http.StatusNoContent {
				return errors.New(resp.Status)
			}
			return nil
		})
	})
	if host != "shop.example" {
		t.Fatalf("Host = %q", host)
	}
	if len(tb.fatals) != 0 {
		t.Fatalf("fatals = %q", tb.fatals)
	}
}

var stopFitsCleanup = evidenceTimeout + fastTimeouts.Cleanup + fastTimeouts.DeadlineMargin + 5*time.Second

func awaitStopTB(t *testing.T) *fakeTB {
	t.Helper()
	return &fakeTB{dir: t.TempDir(), deadline: time.Now().Add(stopFitsCleanup)}
}

func gatewayDynamic(t *testing.T, route *unstructured.Unstructured) dynamic.Interface {
	t.Helper()
	routeGVR := schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}: "GatewayList",
		routeGVR: "HTTPRouteList",
	})
	if route != nil {
		if err := dyn.Tracker().Create(routeGVR, route, route.GetNamespace()); err != nil {
			t.Fatal(err)
		}
	}
	return dyn
}

func useGateway(h *Harness, dyn dynamic.Interface) *Gateway {
	gc := h.InstallGateway(EnvoyGateway())
	gc.cl.Dynamic = dyn
	gc.cl.Kube = fake.NewClientset()
	return &Gateway{
		c: gc,
		target: gateway.Target{
			Gateway: kube.Ref{Namespace: "default", Name: "edge"},
			Service: kube.Ref{Namespace: "envoy-gateway-system", Name: "envoy-edge"},
			Ports:   []int32{80},
		},
	}
}

func settledRoute(controller string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata": map[string]any{
			"name":       "shop",
			"namespace":  "default",
			"generation": int64(1),
		},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"name": "edge"}},
		},
		"status": map[string]any{
			"parents": []any{map[string]any{
				"controllerName": controller,
				"parentRef":      map[string]any{"name": "edge"},
				"conditions": []any{
					map[string]any{"type": "Accepted", "status": "True", "reason": "Accepted", "message": "Route is accepted", "observedGeneration": int64(1)},
					map[string]any{"type": "ResolvedRefs", "status": "True", "reason": "ResolvedRefs", "message": "resolved", "observedGeneration": int64(1)},
				},
			}},
		},
	}}
}

func TestGatewayProfileName(t *testing.T) {
	if got := EnvoyGateway().Name(); got != "gateway-api" {
		t.Errorf("EnvoyGateway().Name() = %q", got)
	}
	if got := (GatewayProfile{}).Name(); got != "" {
		t.Errorf("zero GatewayProfile Name() = %q", got)
	}
}
