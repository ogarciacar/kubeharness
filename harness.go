// Package kubeharness gives each Go test its own disposable Kind cluster.
//
//	func TestSomething(t *testing.T) {
//		h := kubeharness.New(t)
//		client := kubernetes.NewForConfigOrDie(h.RESTConfig())
//		...
//	}
//
// The cluster is deleted through t.Cleanup. Every run logs one report line
// with setup, test, and cleanup outcomes as separate fields.
package kubeharness

import (
	"context"
	"fmt"
	"github.com/ogarciacar/kubeharness/internal/kindcluster"
	"github.com/ogarciacar/kubeharness/internal/kube"
	"github.com/ogarciacar/kubeharness/internal/lifecycle"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var stderr io.Writer = os.Stderr

// Harness is a usable Kind cluster owned by one test.
type Harness struct {
	name       string
	kubeconfig string
	rest       *rest.Config
}

// New creates a Kind cluster owned by t and waits until it accepts workloads.
// A setup failure fails the test immediately. Cleanup always runs, and a
// cleanup failure is reported as its own test error.
func New(t testing.TB) *Harness {
	t.Helper()
	return newHarness(t, kindcluster.New(), kube.WaitUsable, lifecycle.DefaultTimeouts)
}

func newHarness(t testing.TB, p lifecycle.Provider, ready lifecycle.ReadyFunc, to lifecycle.Timeouts) *Harness {
	t.Helper()
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	e := lifecycle.NewEnv(p, ready, kubeconfig, to)
	t.Cleanup(func() {
		end := lifecycle.CauseOK
		if t.Failed() {
			end = lifecycle.CauseTestFailed
		}
		r := e.Close(end)
		t.Log(r.String())
		if r.Cleanup == lifecycle.CleanupFailed || r.Cleanup == lifecycle.CleanupUncertain {
			t.Errorf("kubeharness: cleanup %s for cluster %s: %v; remove it with: %s",
				r.Cleanup, r.Cluster, r.CleanupErr, kindcluster.Remedy(r.Cluster))
		}
	})

	ctx := t.Context()
	// go test's -timeout panic skips cleanups, so everything must finish before it.
	deadline, hasDeadline := testDeadline(t)
	disposeBy := deadline.Add(-(to.Cleanup + to.DeadlineMargin))
	if hasDeadline && !time.Now().Before(disposeBy) {
		err := e.Fail(lifecycle.CauseDeadline, fmt.Errorf("go test -timeout ends in %s, which leaves no time to set up a cluster: cleanup needs %s plus a %s margin",
			time.Until(deadline).Round(time.Second), to.Cleanup, to.DeadlineMargin))
		t.Fatalf("kubeharness: %v", err)
	}
	// testing buffers t.Log until the test ends, so a killed run would never show it.
	fmt.Fprintf(stderr, "kubeharness: creating cluster %s; if this run is killed, remove it with: %s\n", e.Name(), kindcluster.Remedy(e.Name()))
	if hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, disposeBy)
		defer cancel()
	}
	if err := e.Start(ctx); err != nil {
		t.Fatalf("kubeharness: setup failed for cluster %s: %v", e.Name(), err)
	}
	if hasDeadline {
		fired := make(chan struct{})
		watchdog := time.AfterFunc(time.Until(disposeBy), func() {
			defer close(fired)
			r := e.Close(lifecycle.CauseDeadline)
			outcome := fmt.Sprintf("disposed cluster %s early so cleanup could finish", r.Cluster)
			if r.Cleanup != lifecycle.CleanupSucceeded {
				outcome = fmt.Sprintf("cleanup %s for cluster %s; remove it with: %s", r.Cleanup, r.Cluster, kindcluster.Remedy(r.Cluster))
			}
			fmt.Fprintf(stderr, "kubeharness: test still running near -timeout; %s: %s\n", outcome, r)
			t.Errorf("kubeharness: test still running %s before -timeout; %s", time.Until(deadline).Round(time.Second), outcome)
		})
		t.Cleanup(func() {
			if !watchdog.Stop() {
				<-fired
			}
		})
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("kubeharness: load kubeconfig for cluster %s: %v", e.Name(), err)
	}
	return &Harness{name: e.Name(), kubeconfig: kubeconfig, rest: cfg}
}

// testDeadline reads -timeout from *testing.T; testing.TB does not expose it.
func testDeadline(t testing.TB) (time.Time, bool) {
	if d, ok := t.(interface{ Deadline() (time.Time, bool) }); ok {
		return d.Deadline()
	}
	return time.Time{}, false
}

// ClusterName is the Kind cluster name, useful with kind and docker CLIs.
func (h *Harness) ClusterName() string { return h.name }

// KubeconfigPath points at a kubeconfig that only this cluster uses. The
// developer's own kubeconfig is never read or modified.
func (h *Harness) KubeconfigPath() string { return h.kubeconfig }

// RESTConfig returns a fresh copy of the client configuration for the cluster.
func (h *Harness) RESTConfig() *rest.Config { return rest.CopyConfig(h.rest) }
