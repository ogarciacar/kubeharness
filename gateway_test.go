package kubeharness

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

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

func TestGatewayProfileName(t *testing.T) {
	if got := EnvoyGateway().Name(); got != "gateway-api" {
		t.Errorf("EnvoyGateway().Name() = %q", got)
	}
	if got := (GatewayProfile{}).Name(); got != "" {
		t.Errorf("zero GatewayProfile Name() = %q", got)
	}
}
