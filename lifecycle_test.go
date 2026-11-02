package kubeharness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProvider struct {
	mu        sync.Mutex
	inventory map[string]bool
	creates   int
	deletes   int

	listErr     error
	listBlock   chan struct{}
	deleteDelay time.Duration
	// createAllocatesThenFails models kind's kubeconfig-export failure: nodes
	// exist but Create returns an error.
	createAllocatesThenFails bool
	createErr                error
	createBlock              chan struct{}
	deleteErrs               []error
	deleteKeepsCluster       bool
	deleteBlock              chan struct{}
}

func newFake() *fakeProvider { return &fakeProvider{inventory: map[string]bool{}} }

const fakeKubeconfig = `apiVersion: v1
kind: Config
clusters: [{name: c, cluster: {server: "https://127.0.0.1:1"}}]
users: [{name: u, user: {}}]
contexts: [{name: x, context: {cluster: c, user: u}}]
current-context: x
`

func (f *fakeProvider) Create(name, kubeconfigPath string) error {
	if filepath.IsAbs(kubeconfigPath) {
		if err := os.WriteFile(kubeconfigPath, []byte(fakeKubeconfig), 0o600); err != nil {
			return err
		}
	}
	f.mu.Lock()
	f.creates++
	block := f.createBlock
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.inventory[name] = true
	if f.createAllocatesThenFails {
		return errors.New("failed to export kubeconfig")
	}
	return nil
}

func (f *fakeProvider) Delete(name, _ string) error {
	f.mu.Lock()
	f.deletes++
	block := f.deleteBlock
	var err error
	if len(f.deleteErrs) > 0 {
		err, f.deleteErrs = f.deleteErrs[0], f.deleteErrs[1:]
	}
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	time.Sleep(f.deleteDelay)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.deleteKeepsCluster {
		delete(f.inventory, name)
	}
	return nil
}

func (f *fakeProvider) List() ([]string, error) {
	f.mu.Lock()
	block := f.listBlock
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var names []string
	for n := range f.inventory {
		names = append(names, n)
	}
	return names, nil
}

func (f *fakeProvider) counts() (creates, deletes, live int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.creates, f.deletes, len(f.inventory)
}

func readyOK(context.Context, string) error { return nil }

var fastTimeouts = timeouts{
	preflight:      200 * time.Millisecond,
	deadlineMargin: 100 * time.Millisecond,
	create:         200 * time.Millisecond,
	ready:          200 * time.Millisecond,
	cleanup:        300 * time.Millisecond,
	retryInterval:  10 * time.Millisecond,
}

func TestHappyPathReportsSuccessAndDisposes(t *testing.T) {
	f := newFake()
	e := newEnv(f, readyOK, "kc", fastTimeouts)
	if err := e.start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	r := e.close(CauseOK)
	if r.Phase != PhaseReady || r.Cause != CauseOK || r.Cleanup != CleanupSucceeded {
		t.Fatalf("report = %s", r)
	}
	if _, _, live := f.counts(); live != 0 {
		t.Fatalf("%d clusters left after cleanup", live)
	}
}

func TestCreateErrorAfterAllocationStillDeletes(t *testing.T) {
	f := newFake()
	f.createAllocatesThenFails = true
	e := newEnv(f, readyOK, "kc", fastTimeouts)
	if err := e.start(context.Background()); err == nil {
		t.Fatal("start succeeded, want provisioning error")
	}
	r := e.close(CauseOK)
	if r.Cause != CauseProvisioning || r.Cleanup != CleanupSucceeded {
		t.Fatalf("report = %s", r)
	}
	if _, _, live := f.counts(); live != 0 {
		t.Fatal("partially created cluster leaked")
	}
}

func TestSetupAndCleanupErrorsAreBothPreserved(t *testing.T) {
	f := newFake()
	f.createAllocatesThenFails = true
	f.deleteKeepsCluster = true
	f.deleteErrs = slices.Repeat([]error{errors.New("docker rm failed")}, 1000)
	to := fastTimeouts
	to.retryInterval = time.Hour
	to.cleanup = time.Second
	e := newEnv(f, readyOK, "kc", to)
	_ = e.start(context.Background())
	r := e.close(CauseOK)
	if r.SetupErr == nil || r.CleanupErr == nil {
		t.Fatalf("lost an error: %s", r)
	}
	if r.Cleanup != CleanupFailed || !slices.Equal(r.Remaining, []string{r.Cluster}) {
		t.Fatalf("report = %s", r)
	}
}

func TestTransientDeleteFailureIsRetried(t *testing.T) {
	f := newFake()
	f.deleteErrs = []error{errors.New("docker rm -f exit status 1")}
	e := newEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.start(context.Background())
	if r := e.close(CauseOK); r.Cleanup != CleanupSucceeded {
		t.Fatalf("report = %s", r)
	}
	if _, deletes, _ := f.counts(); deletes < 2 {
		t.Fatalf("deletes = %d, want a retry", deletes)
	}
}

func TestHungCreateIsAbandonedAndReportedUncertain(t *testing.T) {
	f := newFake()
	f.createBlock = make(chan struct{})
	defer close(f.createBlock)
	e := newEnv(f, readyOK, "kc", fastTimeouts)
	began := time.Now()
	err := e.start(context.Background())
	if err == nil || e.report.Cause != CauseDeadline {
		t.Fatalf("start err=%v report=%s", err, e.report)
	}
	if took := time.Since(began); took > fastTimeouts.create+time.Second {
		t.Fatalf("start blocked for %s", took)
	}
	r := e.close(CauseOK)
	if r.Cleanup != CleanupUncertain {
		t.Fatalf("cleanup = %s, want uncertain while create is in flight", r.Cleanup)
	}
	if _, deletes, _ := f.counts(); deletes == 0 {
		t.Fatal("no delete attempted for a possibly allocated cluster")
	}
}

func TestHungDeleteIsBoundedByCleanupBudget(t *testing.T) {
	f := newFake()
	f.deleteBlock = make(chan struct{})
	defer close(f.deleteBlock)
	e := newEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.start(context.Background())
	began := time.Now()
	r := e.close(CauseOK)
	if took := time.Since(began); took > fastTimeouts.cleanup+time.Second {
		t.Fatalf("close blocked for %s", took)
	}
	if r.Cleanup == CleanupSucceeded {
		t.Fatalf("report = %s, want unconfirmed cleanup", r)
	}
}

func TestCancellationDuringReadinessStillCleansUp(t *testing.T) {
	f := newFake()
	ctx, cancel := context.WithCancel(context.Background())
	ready := func(ctx context.Context, _ string) error {
		cancel()
		<-ctx.Done()
		return fmt.Errorf("nodes not Ready; last observed: NotReady: %w", ctx.Err())
	}
	e := newEnv(f, ready, "kc", fastTimeouts)
	if err := e.start(ctx); err == nil {
		t.Fatal("start succeeded after cancellation")
	}
	r := e.close(CauseOK)
	if r.Cause != CauseCanceled || r.Phase != PhaseWaitUsable || r.Cleanup != CleanupSucceeded {
		t.Fatalf("report = %s", r)
	}
}

func TestReadinessTimeoutKeepsLastObservation(t *testing.T) {
	f := newFake()
	ready := func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return fmt.Errorf("last observed: node kh-x NotReady (KubeletNotReady): %w", ctx.Err())
	}
	e := newEnv(f, ready, "kc", fastTimeouts)
	_ = e.start(context.Background())
	r := e.close(CauseOK)
	if r.Cause != CauseDeadline || r.SetupErr == nil || !strings.Contains(r.SetupErr.Error(), "KubeletNotReady") {
		t.Fatalf("report = %s", r)
	}
}

func TestPreflightFailureAllocatesNothing(t *testing.T) {
	f := newFake()
	f.listErr = errors.New("cannot connect to the docker daemon")
	e := newEnv(f, readyOK, "kc", fastTimeouts)
	if err := e.start(context.Background()); err == nil {
		t.Fatal("start succeeded without a runtime")
	}
	r := e.close(CauseOK)
	creates, deletes, _ := f.counts()
	if r.Cause != CausePreflight || r.Cleanup != CleanupNotNeeded || creates != 0 || deletes != 0 {
		t.Fatalf("report=%s creates=%d deletes=%d", r, creates, deletes)
	}
}

func TestNameCollisionNeverDeletesTheOtherCluster(t *testing.T) {
	f := newFake()
	f.createErr = fmt.Errorf("node(s) already exist: %w", errNameTaken)
	e := newEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.start(context.Background())
	r := e.close(CauseOK)
	if _, deletes, _ := f.counts(); deletes != 0 || r.Cleanup != CleanupNotNeeded {
		t.Fatalf("report=%s deletes=%d", r, deletes)
	}
}

func TestCloseTwiceDeletesOnceAndReturnsSameReport(t *testing.T) {
	f := newFake()
	e := newEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.start(context.Background())
	first, second := e.close(CauseOK), e.close(CauseTestFailed)
	if first.String() != second.String() {
		t.Fatalf("reports differ:\n%s\n%s", first, second)
	}
	if _, deletes, _ := f.counts(); deletes != 1 {
		t.Fatalf("deletes = %d", deletes)
	}
}

func TestTestFailureIsReportedApartFromCleanup(t *testing.T) {
	f := newFake()
	e := newEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.start(context.Background())
	if r := e.close(CauseTestFailed); r.Cause != CauseTestFailed || r.Cleanup != CleanupSucceeded {
		t.Fatalf("report = %s", r)
	}
}

func TestParallelEnvsGetDistinctClusters(t *testing.T) {
	f := newFake()
	var wg sync.WaitGroup
	var mu sync.Mutex
	names := map[string]bool{}
	for range 50 {
		wg.Go(func() {
			e := newEnv(f, readyOK, "kc", fastTimeouts)
			if err := e.start(context.Background()); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			names[e.report.Cluster] = true
			mu.Unlock()
			e.close(CauseOK)
		})
	}
	wg.Wait()
	if _, _, live := f.counts(); len(names) != 50 || live != 0 {
		t.Fatalf("distinct=%d live=%d", len(names), live)
	}
}

func TestHungPreflightIsBounded(t *testing.T) {
	f := newFake()
	f.listBlock = make(chan struct{})
	defer close(f.listBlock)
	e := newEnv(f, readyOK, "kc", fastTimeouts)
	began := time.Now()
	err := e.start(context.Background())
	if took := time.Since(began); err == nil || took > fastTimeouts.preflight+time.Second {
		t.Fatalf("start err=%v after %s", err, took)
	}
	if r := e.close(CauseOK); r.Cause != CauseDeadline || r.Phase != PhaseValidating || r.Cleanup != CleanupNotNeeded {
		t.Fatalf("report = %s", r)
	}
}

// lateCreateProvider lets a hung Create finish, with nodes, right after the
// first cleanup List has taken its snapshot.
type lateCreateProvider struct {
	*fakeProvider
	lists    int
	release  chan struct{}
	finished chan struct{}
}

func (p *lateCreateProvider) Create(name, kc string) error {
	<-p.release
	err := p.fakeProvider.Create(name, kc)
	close(p.finished)
	return err
}

func (p *lateCreateProvider) List() ([]string, error) {
	names, err := p.fakeProvider.List()
	p.lists++
	if p.lists == 2 {
		close(p.release)
		<-p.finished
		time.Sleep(10 * time.Millisecond)
	}
	return names, err
}

func TestCreateFinishingDuringCleanupIsNotReportedClean(t *testing.T) {
	p := &lateCreateProvider{fakeProvider: newFake(), release: make(chan struct{}), finished: make(chan struct{})}
	e := newEnv(p, readyOK, "kc", fastTimeouts)
	_ = e.start(context.Background())
	r := e.close(CauseOK)
	_, _, live := p.counts()
	if r.Cleanup == CleanupSucceeded && live != 0 {
		t.Fatalf("cleanup=succeeded with %d clusters present: %s", live, r)
	}
	if r.Cleanup != CleanupSucceeded || live != 0 {
		t.Fatalf("want the late cluster deleted on retry, got live=%d report=%s", live, r)
	}
}

func TestCleanupErrorKeepsRealDeleteCause(t *testing.T) {
	f := newFake()
	f.deleteKeepsCluster = true
	f.deleteDelay = 120 * time.Millisecond
	f.deleteErrs = slices.Repeat([]error{errors.New("docker rm -f: permission denied")}, 1000)
	e := newEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.start(context.Background())
	r := e.close(CauseOK)
	if r.CleanupErr == nil || !strings.Contains(r.CleanupErr.Error(), "permission denied") {
		t.Fatalf("real delete failure lost: %s", r)
	}
}

func TestUnconfirmedDisposalIsUncertainNotFailed(t *testing.T) {
	f := newFake()
	e := newEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.start(context.Background())
	f.mu.Lock()
	f.listErr = errors.New("docker ps: daemon not responding")
	f.mu.Unlock()
	r := e.close(CauseOK)
	if r.Cleanup != CleanupUncertain || len(r.Remaining) != 0 || !strings.Contains(r.CleanupErr.Error(), "daemon not responding") {
		t.Fatalf("report = %s", r)
	}
}

func TestCreateDeadlineErrorNamesTheBudgetThatFired(t *testing.T) {
	f := newFake()
	f.createBlock = make(chan struct{})
	defer close(f.createBlock)
	e := newEnv(f, readyOK, "kc", timeouts{preflight: time.Second, create: time.Hour, ready: time.Hour, cleanup: 100 * time.Millisecond, retryInterval: 10 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := e.start(ctx)
	if err == nil || strings.Contains(err.Error(), "1h0m0s") || !strings.Contains(err.Error(), "setup deadline") {
		t.Fatalf("error names the 1h create budget, but the 150ms setup deadline fired: %v", err)
	}
	e.close(CauseOK)
}

// flakyListProvider lists once successfully during cleanup, then every List
// fails, while Delete eventually succeeds.
type flakyListProvider struct {
	*fakeProvider
	lists int
}

func (p *flakyListProvider) List() ([]string, error) {
	p.lists++
	if p.lists > 2 {
		return nil, errors.New("docker ps: i/o timeout")
	}
	return p.fakeProvider.List()
}

func TestClusterGoneAfterLaterDeleteIsUncertainNotFailed(t *testing.T) {
	p := &flakyListProvider{fakeProvider: newFake()}
	p.deleteErrs = []error{errors.New("docker rm -f: device or resource busy")}
	e := newEnv(p, readyOK, "kc", fastTimeouts)
	_ = e.start(context.Background())
	r := e.close(CauseOK)
	if _, _, live := p.counts(); live != 0 {
		t.Fatalf("fixture broken: %d clusters left", live)
	}
	if r.Cleanup != CleanupUncertain || len(r.Remaining) != 0 {
		t.Fatalf("cluster is gone but report = %s", r)
	}
}

// slowRetryDeleteProvider fails the first Delete, then makes the retry run
// past the cleanup budget before it removes the cluster.
type slowRetryDeleteProvider struct {
	*fakeProvider
	deletes int
}

func (p *slowRetryDeleteProvider) Delete(name, kc string) error {
	p.deletes++
	if p.deletes == 1 {
		return errors.New("docker rm -f: device or resource busy")
	}
	time.Sleep(fastTimeouts.cleanup)
	return p.fakeProvider.Delete(name, kc)
}

func TestDeleteStillRunningWhenBudgetEndsIsUncertainNotFailed(t *testing.T) {
	p := &slowRetryDeleteProvider{fakeProvider: newFake()}
	e := newEnv(p, readyOK, "kc", fastTimeouts)
	_ = e.start(context.Background())
	if r := e.close(CauseOK); r.Cleanup != CleanupUncertain || len(r.Remaining) != 0 {
		t.Fatalf("a delete was still running, so presence is unconfirmed, but report = %s", r)
	}
}

func TestHungPreflightStopsAtTheSetupDeadline(t *testing.T) {
	f := newFake()
	f.listBlock = make(chan struct{})
	defer close(f.listBlock)
	to := fastTimeouts
	to.preflight = time.Hour
	e := newEnv(f, readyOK, "kc", to)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	began := time.Now()
	err := e.start(ctx)
	if took := time.Since(began); err == nil || took > time.Second || !strings.Contains(err.Error(), "setup deadline") {
		t.Fatalf("preflight ignored the setup deadline: took %s, err %v", took, err)
	}
}

func TestReadinessCutBySetupDeadlineSaysSo(t *testing.T) {
	f := newFake()
	ready := func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return fmt.Errorf("cluster not usable; last observed: node NotReady: %w", ctx.Err())
	}
	to := fastTimeouts
	to.ready = time.Hour
	e := newEnv(f, ready, "kc", to)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := e.start(ctx)
	e.close(CauseOK)
	if err == nil || !strings.Contains(err.Error(), "setup deadline") {
		t.Fatalf("readiness error does not name the setup deadline: %v", err)
	}
}

// kindDeleteProvider behaves like kind's delete.Cluster: the second Delete
// removes the nodes and still returns the kubeconfig error. After that,
// listing fails.
type kindDeleteProvider struct {
	*fakeProvider
	deletes, lists int
}

func (p *kindDeleteProvider) Delete(name, kc string) error {
	p.deletes++
	if p.deletes == 1 {
		return errors.New("docker rm -f: device or resource busy")
	}
	_ = p.fakeProvider.Delete(name, kc)
	return errors.New("failed to lock config file")
}

func (p *kindDeleteProvider) List() ([]string, error) {
	p.lists++
	if p.lists > 2 {
		return nil, errors.New("docker ps: i/o timeout")
	}
	return p.fakeProvider.List()
}

func TestDeleteThatRemovedNodesButErroredIsNotReportedFailed(t *testing.T) {
	p := &kindDeleteProvider{fakeProvider: newFake()}
	e := newEnv(p, readyOK, "kc", fastTimeouts)
	_ = e.start(context.Background())
	r := e.close(CauseOK)
	if _, _, live := p.counts(); live != 0 || r.Cleanup != CleanupUncertain || len(r.Remaining) != 0 {
		t.Fatalf("live=%d report=%s", live, r)
	}
	if !strings.Contains(r.CleanupErr.Error(), "i/o timeout") {
		t.Fatalf("uncertain cleanup does not say why: %v", r.CleanupErr)
	}
}

func TestUncertainCleanupNamesTheInFlightCreate(t *testing.T) {
	f := newFake()
	f.createBlock = make(chan struct{})
	defer close(f.createBlock)
	e := newEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.start(context.Background())
	if r := e.close(CauseOK); r.CleanupErr == nil || !strings.Contains(r.CleanupErr.Error(), "create still in flight") {
		t.Fatalf("report = %s", r)
	}
}

func TestPreflightCutBySetupDeadlineIsCauseDeadline(t *testing.T) {
	f := newFake()
	f.listBlock = make(chan struct{})
	defer close(f.listBlock)
	to := fastTimeouts
	to.preflight = time.Hour
	e := newEnv(f, readyOK, "kc", to)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = e.start(ctx)
	if r := e.close(CauseOK); r.Cause != CauseDeadline {
		t.Fatalf("report = %s", r)
	}
}

func TestReportRecordsPhaseDurations(t *testing.T) {
	f := newFake()
	f.deleteDelay = 20 * time.Millisecond
	ready := func(context.Context, string) error { time.Sleep(20 * time.Millisecond); return nil }
	e := newEnv(f, ready, "kc", fastTimeouts)
	_ = e.start(context.Background())
	r := e.close(CauseOK)
	if r.CreateDuration <= 0 || r.ReadyDuration < 20*time.Millisecond || r.DeleteDuration < 20*time.Millisecond {
		t.Fatalf("durations not recorded: %s", r)
	}
}

func TestReadinessErrorOutsideAnyDeadlineIsCauseReadiness(t *testing.T) {
	ready := func(context.Context, string) error { return errors.New("load kubeconfig: no such file") }
	e := newEnv(newFake(), ready, "kc", fastTimeouts)
	_ = e.start(context.Background())
	if r := e.close(CauseOK); r.Cause != CauseReadiness || r.Phase != PhaseWaitUsable {
		t.Fatalf("report = %s", r)
	}
}

func TestNameAlreadyListedAtPreflightIsNeverCreatedOrDeleted(t *testing.T) {
	f := newFake()
	e := newEnv(f, readyOK, "kc", fastTimeouts)
	f.inventory[string(e.name)] = true
	if err := e.start(context.Background()); !errors.Is(err, errNameTaken) {
		t.Fatalf("start err = %v, want errNameTaken", err)
	}
	r := e.close(CauseOK)
	creates, deletes, live := f.counts()
	if creates != 0 || deletes != 0 || live != 1 || r.Cleanup != CleanupNotNeeded {
		t.Fatalf("creates=%d deletes=%d live=%d report=%s", creates, deletes, live, r)
	}
}

// slowCollisionProvider: the name is not taken at preflight, but another
// owner creates it before kind's own check answers, after our deadline.
type slowCollisionProvider struct {
	*fakeProvider
	release chan struct{}
}

func (p *slowCollisionProvider) Create(name, _ string) error {
	p.mu.Lock()
	p.inventory[name] = true
	p.mu.Unlock()
	<-p.release
	return fmt.Errorf("node(s) already exist: %w", errNameTaken)
}

func TestCollisionReportedAfterTheCreateDeadlineStopsCleanup(t *testing.T) {
	p := &slowCollisionProvider{fakeProvider: newFake(), release: make(chan struct{})}
	e := newEnv(p, readyOK, "kc", fastTimeouts)
	_ = e.start(context.Background())
	close(p.release)
	time.Sleep(20 * time.Millisecond)
	r := e.close(CauseOK)
	if _, deletes, live := p.counts(); deletes != 0 || live != 1 || r.Cleanup != CleanupNotNeeded {
		t.Fatalf("deleted another owner's cluster: deletes=%d live=%d report=%s", deletes, live, r)
	}
}

func TestDeleteRunningAtBudgetEndIsAlwaysUncertain(t *testing.T) {
	to := fastTimeouts
	to.cleanup = 2 * time.Millisecond
	outcomes := map[CleanupOutcome]int{}
	for range 500 {
		f := newFake()
		e := newEnv(f, readyOK, "kc", to)
		_ = e.start(context.Background())
		f.deleteBlock = make(chan struct{})
		outcomes[e.close(CauseOK).Cleanup]++
		close(f.deleteBlock)
	}
	if outcomes[CleanupUncertain] != 500 {
		t.Fatalf("outcomes = %v, want uncertain every time", outcomes)
	}
}

// countingProvider counts List calls that start after a deadline.
type countingProvider struct {
	*fakeProvider
	after     atomic.Pointer[time.Time]
	lateLists atomic.Int32
}

func (p *countingProvider) List() ([]string, error) {
	if after := p.after.Load(); after != nil && time.Now().After(*after) {
		p.lateLists.Add(1)
	}
	return p.fakeProvider.List()
}

func TestNoListStartsAfterTheCleanupBudget(t *testing.T) {
	p := &countingProvider{fakeProvider: newFake()}
	to := fastTimeouts
	to.cleanup = 50 * time.Millisecond
	e := newEnv(p, readyOK, "kc", to)
	_ = e.start(context.Background())
	p.mu.Lock()
	p.deleteBlock = make(chan struct{})
	p.mu.Unlock()
	defer close(p.deleteBlock)
	after := time.Now().Add(to.cleanup)
	p.after.Store(&after)
	e.close(CauseOK)
	time.Sleep(50 * time.Millisecond)
	if n := p.lateLists.Load(); n != 0 {
		t.Fatalf("%d List calls started after the cleanup budget ended", n)
	}
}
