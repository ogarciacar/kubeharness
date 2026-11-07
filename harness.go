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
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/ogarciacar/kubeharness/internal/kindcluster"
	"github.com/ogarciacar/kubeharness/internal/kube"
	"github.com/ogarciacar/kubeharness/internal/lifecycle"
	"github.com/ogarciacar/kubeharness/internal/profile"
)

var stderr io.Writer = os.Stderr

// Harness is a usable Kind cluster owned by one test. Call its methods from
// that test's goroutine: they fail that test, and anything they start, such as
// a Forward, stops when that test ends.
type Harness struct {
	t          testing.TB
	name       string
	kubeconfig string
	rest       *rest.Config
	client     kubernetes.Interface
	images     imageLoader
	env        *lifecycle.Env
	install    func(ctx, evidence context.Context, cfg *rest.Config, client kubernetes.Interface, r *profile.Recipe) error
	gateway    *GatewayController
	// opsEndBy is when every operation must have finished, so evidence and
	// cleanup still fit before go test's -timeout panic. Zero means no bound.
	opsEndBy time.Time
}

type imageLoader interface {
	LoadImage(cluster, ref string) error
}

const (
	applyTimeout    = 30 * time.Second
	readyTimeout    = 3 * time.Minute
	jobTimeout      = 3 * time.Minute
	forwardTimeout  = 30 * time.Second
	loadTimeout     = 2 * time.Minute
	evidenceTimeout = 10 * time.Second
)

const deleteTimeout = time.Minute

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
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("kubeharness: client for cluster %s: %v", e.Name(), err)
	}
	h := &Harness{t: t, name: e.Name(), kubeconfig: kubeconfig, rest: cfg, client: client, env: e, install: profile.Install}
	h.images, _ = p.(imageLoader)
	if hasDeadline {
		h.opsEndBy = disposeBy.Add(-evidenceTimeout)
	}
	return h
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

// op returns a context for one operation, bounded by limit and by opsEndBy,
// and a second context that leaves evidenceTimeout after it for gathering
// failure evidence.
func (h *Harness) op(limit time.Duration) (ctx, evidence context.Context, cancel context.CancelFunc) {
	end := time.Now().Add(limit)
	if !h.opsEndBy.IsZero() && h.opsEndBy.Before(end) {
		end = h.opsEndBy
	}
	ctx, cancelOp := context.WithDeadline(context.Background(), end)
	evidence, cancelEvidence := context.WithDeadline(context.Background(), end.Add(evidenceTimeout))
	return ctx, evidence, func() { cancelOp(); cancelEvidence() }
}

// fatalf stops the test. When ctx ended at opsEndBy rather than at the
// operation's own limit, it says so, so a short go test -timeout is not
// mistaken for a slow workload.
func (h *Harness) fatalf(ctx context.Context, format string, args ...any) {
	h.t.Helper()
	msg := fmt.Sprintf(format, args...)
	if d, ok := ctx.Deadline(); ok && ctx.Err() != nil && d.Equal(h.opsEndBy) {
		msg += " (stopped early so cleanup fits before go test -timeout)"
	}
	h.t.Fatalf("%s", msg)
}

// Apply server-side applies every .yaml and .yml manifest in dir, in file-name
// order. Namespaced objects without a namespace go to "default".
func (h *Harness) Apply(dir string) {
	h.t.Helper()
	h.apply(dir, h.manifests(dir))
}

func (h *Harness) manifests(dir string) []*unstructured.Unstructured {
	h.t.Helper()
	objs, err := kube.ReadManifests(dir)
	if err != nil {
		h.t.Fatalf("kubeharness: read manifests: %v", err)
	}
	return objs
}

func (h *Harness) apply(dir string, objs []*unstructured.Unstructured) {
	h.t.Helper()
	ctx, _, cancel := h.op(applyTimeout)
	defer cancel()
	if _, err := kube.Apply(ctx, h.rest, objs); err != nil {
		h.fatalf(ctx, "kubeharness: apply %s: %v", dir, err)
	}
}

// Delete removes every manifest in dir and waits until each object is gone.
// Order is the reverse of Apply. NotFound is success, so a second Delete of
// the same directory is done. A kind the API server does not serve is already
// gone, and Delete skips it instead of waiting for the kind to appear.
// Gone means GET returns NotFound. Propagation is background.
// Namespaced objects with an empty namespace are read from "default".
// On timeout the test stops with each remaining object and its finalizers.
// Pass a directory that contains only the objects you mean to remove.
// A Namespace in that directory waits on the Namespace finalizers.
func (h *Harness) Delete(dir string) {
	h.t.Helper()
	objs := h.manifests(dir)
	ctx, _, cancel := h.op(deleteTimeout)
	defer cancel()
	if err := kube.Delete(ctx, h.rest, objs); err != nil {
		h.fatalf(ctx, "kubeharness: delete %s: %v", dir, err)
	}
}

func (h *Harness) ref(kind, s string) kube.Ref {
	h.t.Helper()
	r, err := kube.ParseRef(s, kube.DefaultNamespace)
	if err != nil {
		h.t.Fatalf("kubeharness: %s %q: %v", kind, s, err)
	}
	return r
}

// WaitReady waits until the Deployment named ref is rolled out and every
// replica is available. ref is "name" in the default namespace, or
// "namespace/name". On failure the test stops with the last observed rollout
// state and the state and logs of the Deployment's Pods.
func (h *Harness) WaitReady(ref string) {
	h.t.Helper()
	r := h.ref("deployment", ref)
	ctx, evidence, cancel := h.op(readyTimeout)
	defer cancel()
	if err := kube.WaitDeployment(ctx, evidence, h.client, r.Namespace, r.Name); err != nil {
		h.fatalf(ctx, "kubeharness: wait ready %s: %v", r, err)
	}
}

// RunJob applies the manifests in dir, which must contain exactly one Job, and
// waits for that Job to complete. An earlier run of the same Job is deleted
// first, so every call runs it again. If the Job fails or does not finish in
// time, the test stops with the Job's conditions and its Pods' states and logs.
func (h *Harness) RunJob(dir string) {
	h.t.Helper()
	objs := h.manifests(dir)
	job, err := kube.OnlyJob(objs)
	if err != nil {
		h.t.Fatalf("kubeharness: run job from %s: %v", dir, err)
	}
	ctx, evidence, cancel := h.op(jobTimeout)
	defer cancel()
	if err := kube.ResetJob(ctx, h.client, job.Namespace, job.Name); err != nil {
		h.fatalf(ctx, "kubeharness: run job: %v", err)
	}
	h.apply(dir, objs)
	if err := kube.WaitJob(ctx, evidence, h.client, job.Namespace, job.Name); err != nil {
		h.fatalf(ctx, "kubeharness: run job: %v", err)
	}
}

// Forward forwards a local port to port of the Service named ref and returns
// the local address, such as 127.0.0.1:53017. ref is "name" in the default
// namespace, or "namespace/name". Use the address as a TCP address or as
// "http://" plus the address. The forward stops when the test ends.
func (h *Harness) Forward(ref string, port int) string {
	h.t.Helper()
	r := h.ref("service", ref)
	ctx, _, cancel := h.op(forwardTimeout)
	defer cancel()
	f, err := kube.StartForward(ctx, h.rest, h.client, r.Namespace, r.Name, int32(port))
	if err != nil {
		h.fatalf(ctx, "kubeharness: forward %s:%d: %v", r, port, err)
	}
	h.t.Cleanup(func() {
		if err := f.Close(); err != nil {
			h.t.Logf("kubeharness: forward %s:%d ended with: %v", r, port, err)
		}
	})
	return f.Addr
}

// LoadImage copies ref from the local Docker daemon into the cluster's nodes,
// so manifests can use it with imagePullPolicy: Never. Build the image first;
// LoadImage never builds or pulls.
func (h *Harness) LoadImage(ref string) {
	h.t.Helper()
	if h.images == nil {
		h.t.Fatalf("kubeharness: load image %s: this cluster cannot load local images", ref)
	}
	ctx, _, cancel := h.op(loadTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.images.LoadImage(h.name, ref) }()
	select {
	case err := <-done:
		if err != nil {
			h.t.Fatalf("kubeharness: load image %s: %v", ref, err)
		}
	case <-ctx.Done():
		h.fatalf(ctx, "kubeharness: load image %s did not finish: %v", ref, ctx.Err())
	}
}
