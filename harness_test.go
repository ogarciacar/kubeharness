package kubeharness

import (
	"context"
	"errors"
	"fmt"
	"github.com/ogarciacar/kubeharness/internal/kindcluster"
	"github.com/ogarciacar/kubeharness/internal/lifecycle"
	"github.com/ogarciacar/kubeharness/internal/lifecycle/lifecycletest"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeTB struct {
	testing.TB
	dir      string
	cleanups []func()
	failed   bool
	fatals   []string
	errs     []string
	logs     []string
	deadline time.Time
}

func (f *fakeTB) Deadline() (time.Time, bool) { return f.deadline, !f.deadline.IsZero() }

func (f *fakeTB) Helper()                  {}
func (f *fakeTB) TempDir() string          { return f.dir }
func (f *fakeTB) Context() context.Context { return context.Background() }
func (f *fakeTB) Cleanup(fn func())        { f.cleanups = append(f.cleanups, fn) }
func (f *fakeTB) Failed() bool             { return f.failed }
func (f *fakeTB) Log(args ...any)          { f.logs = append(f.logs, fmt.Sprint(args...)) }
func (f *fakeTB) Logf(string, ...any)      {}
func (f *fakeTB) Errorf(format string, args ...any) {
	f.failed = true
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.failed = true
	f.fatals = append(f.fatals, fmt.Sprintf(format, args...))
	runtime.Goexit()
}

func (f *fakeTB) run(body func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		body()
	}()
	<-done
	for _, fn := range slices.Backward(f.cleanups) {
		fn()
	}
}

func TestSetupFailureFailsTestAndStillCleansUp(t *testing.T) {
	f := lifecycletest.NewFake()
	f.CreateAllocatesThenFails = true
	tb := &fakeTB{dir: t.TempDir()}
	tb.run(func() { newHarness(tb, f, readyOK, fastTimeouts) })

	if len(tb.fatals) != 1 || !strings.Contains(tb.fatals[0], "setup failed") {
		t.Fatalf("fatals = %q", tb.fatals)
	}
	if _, _, live := f.Counts(); live != 0 {
		t.Fatal("cluster leaked after setup failure")
	}
	if len(tb.logs) != 1 || !strings.Contains(tb.logs[0], "cause=provisioning cleanup=succeeded") {
		t.Fatalf("report line = %q", tb.logs)
	}
}

func TestCleanupFailureIsItsOwnTestErrorWithRemedy(t *testing.T) {
	f := lifecycletest.NewFake()
	f.DeleteKeepsCluster = true
	f.DeleteErrs = slices.Repeat([]error{errors.New("docker rm failed")}, 1000)
	tb := &fakeTB{dir: t.TempDir()}
	var h *Harness
	to := fastTimeouts
	to.RetryInterval = time.Hour
	to.Cleanup = time.Second
	tb.run(func() { h = newHarness(tb, f, readyOK, to) })

	if len(tb.fatals) != 0 {
		t.Fatalf("setup should have passed: %q", tb.fatals)
	}
	want := kindcluster.Remedy(h.ClusterName())
	if len(tb.errs) != 1 || !strings.Contains(tb.errs[0], want) {
		t.Fatalf("errs = %q, want remedy %q", tb.errs, want)
	}
	if !strings.Contains(tb.logs[0], "cause=ok cleanup=failed remaining=1") {
		t.Fatalf("report line = %q", tb.logs)
	}
}

func TestSetupLeavesCleanupAndMarginBeforeTestDeadline(t *testing.T) {
	f := lifecycletest.NewFake()
	f.CreateBlock = make(chan struct{})
	defer close(f.CreateBlock)
	f.DeleteBlock = make(chan struct{})
	defer close(f.DeleteBlock)
	to := fastTimeouts
	to.Create = time.Hour
	deadline := time.Now().Add(to.Cleanup + to.DeadlineMargin + 300*time.Millisecond)
	tb := &fakeTB{dir: t.TempDir(), deadline: deadline}
	tb.run(func() { newHarness(tb, f, readyOK, to) })

	if left := time.Until(deadline); left < to.DeadlineMargin/2 {
		t.Fatalf("cleanup finished %s before the test deadline, want about %s of margin", left, to.DeadlineMargin)
	}
	if len(tb.logs) != 1 || !strings.Contains(tb.logs[0], "cause=deadline") {
		t.Fatalf("report line = %q", tb.logs)
	}
}

func TestOverrunningTestBodyGetsClusterDisposedBeforeDeadline(t *testing.T) {
	f := lifecycletest.NewFake()
	f.DeleteDelay = 250 * time.Millisecond
	var out strings.Builder
	stderr = &out
	defer func() { stderr = os.Stderr }()
	to := fastTimeouts
	deadline := time.Now().Add(to.Cleanup + to.DeadlineMargin + 300*time.Millisecond)
	tb := &fakeTB{dir: t.TempDir(), deadline: deadline}
	var name string
	var liveAtDeadline int
	tb.run(func() {
		h := newHarness(tb, f, readyOK, to)
		name = h.ClusterName()
		time.Sleep(time.Until(deadline))
		_, _, liveAtDeadline = f.Counts()
	})
	if liveAtDeadline != 0 {
		t.Fatal("cluster still present when go test would panic on -timeout")
	}
	if !strings.Contains(out.String(), "disposed cluster "+name+" early") || !strings.Contains(out.String(), "cause=deadline") {
		t.Fatalf("early disposal not reported with the cluster name: %q", out.String())
	}
	if !tb.failed || len(tb.errs) == 0 || !strings.Contains(tb.errs[0], "disposed") {
		t.Fatalf("test whose cluster was deleted mid-run was not failed: errs=%q", tb.errs)
	}
}

// stderrAtCreate records what stderr held when Create was called.
type stderrAtCreate struct {
	*lifecycletest.Fake
	out  *strings.Builder
	seen string
}

func (p *stderrAtCreate) Create(name, kc string) error {
	p.seen = p.out.String()
	return p.Fake.Create(name, kc)
}

func TestClusterNameReachesStderrBeforeCreate(t *testing.T) {
	var out strings.Builder
	stderr = &out
	defer func() { stderr = os.Stderr }()
	p := &stderrAtCreate{Fake: lifecycletest.NewFake(), out: &out}
	tb := &fakeTB{dir: t.TempDir()}
	var name string
	tb.run(func() { name = newHarness(tb, p, readyOK, fastTimeouts).ClusterName() })
	if !strings.Contains(p.seen, kindcluster.Remedy(name)) {
		t.Fatalf("cluster name and remedy not on stderr when Create started: %q", p.seen)
	}
}

func TestUncertainCleanupFailsTheTest(t *testing.T) {
	f := lifecycletest.NewFake()
	f.CreateBlock = make(chan struct{})
	defer close(f.CreateBlock)
	tb := &fakeTB{dir: t.TempDir()}
	tb.run(func() { newHarness(tb, f, readyOK, fastTimeouts) })
	if len(tb.errs) != 1 || !strings.Contains(tb.errs[0], "cleanup uncertain") {
		t.Fatalf("uncertain cleanup did not fail the test: errs=%q", tb.errs)
	}
}

func TestTimeoutTooShortForCleanupFailsBeforeCreating(t *testing.T) {
	f := lifecycletest.NewFake()
	to := fastTimeouts
	tb := &fakeTB{dir: t.TempDir(), deadline: time.Now().Add(to.Cleanup + to.DeadlineMargin - 10*time.Millisecond)}
	tb.run(func() { newHarness(tb, f, readyOK, to) })
	creates, _, _ := f.Counts()
	if creates != 0 || len(tb.fatals) != 1 || !strings.Contains(tb.fatals[0], "-timeout") {
		t.Fatalf("creates=%d fatals=%q", creates, tb.fatals)
	}
	if len(tb.logs) != 1 || !strings.Contains(tb.logs[0], "cause=deadline") || !strings.Contains(tb.logs[0], "setup_err=") {
		t.Fatalf("refused setup blamed on the test body: %q", tb.logs)
	}
}

func TestFailedEarlyDisposalSaysSoAndGivesTheRemedy(t *testing.T) {
	f := lifecycletest.NewFake()
	f.DeleteKeepsCluster = true
	f.DeleteErrs = slices.Repeat([]error{errors.New("docker rm -f: permission denied")}, 1000)
	var out strings.Builder
	stderr = &out
	defer func() { stderr = os.Stderr }()
	to := fastTimeouts
	to.RetryInterval = time.Hour
	deadline := time.Now().Add(to.Cleanup + to.DeadlineMargin + 300*time.Millisecond)
	tb := &fakeTB{dir: t.TempDir(), deadline: deadline}
	var name string
	tb.run(func() {
		name = newHarness(tb, f, readyOK, to).ClusterName()
		time.Sleep(time.Until(deadline))
	})
	if strings.Contains(out.String(), "disposed cluster") || !strings.Contains(out.String(), "cleanup failed for cluster "+name) || !strings.Contains(out.String(), kindcluster.Remedy(name)) {
		t.Fatalf("failed early disposal reported as done: %q", out.String())
	}
}

func TestRESTConfigReturnsACopy(t *testing.T) {
	tb := &fakeTB{dir: t.TempDir()}
	var h *Harness
	tb.run(func() { h = newHarness(tb, lifecycletest.NewFake(), readyOK, fastTimeouts) })
	a := h.RESTConfig()
	a.Host = "https://changed.invalid"
	if h.RESTConfig().Host == a.Host {
		t.Fatal("RESTConfig shares state between callers")
	}
}

func readyOK(context.Context, string) error { return nil }

var fastTimeouts = lifecycle.Timeouts{
	Preflight:      200 * time.Millisecond,
	DeadlineMargin: 100 * time.Millisecond,
	Create:         200 * time.Millisecond,
	Ready:          200 * time.Millisecond,
	Cleanup:        300 * time.Millisecond,
	RetryInterval:  10 * time.Millisecond,
}
