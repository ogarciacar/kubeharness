package kubeharness

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

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

func TestGatewayProfileName(t *testing.T) {
	if got := EnvoyGateway().Name(); got != "gateway-api" {
		t.Errorf("EnvoyGateway().Name() = %q", got)
	}
	if got := (GatewayProfile{}).Name(); got != "" {
		t.Errorf("zero GatewayProfile Name() = %q", got)
	}
}
