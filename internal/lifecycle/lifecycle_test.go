package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ogarciacar/kubeharness/internal/lifecycle/lifecycletest"
)

func readyOK(context.Context, string) error { return nil }

var fastTimeouts = Timeouts{
	Preflight:      200 * time.Millisecond,
	DeadlineMargin: 100 * time.Millisecond,
	Create:         200 * time.Millisecond,
	Ready:          200 * time.Millisecond,
	Cleanup:        300 * time.Millisecond,
	RetryInterval:  10 * time.Millisecond,
}

func TestHappyPathReportsSuccessAndDisposes(t *testing.T) {
	f := lifecycletest.NewFake()
	e := NewEnv(f, readyOK, "kc", fastTimeouts)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	r := e.Close(CauseOK)
	if r.Phase != PhaseReady || r.Cause != CauseOK || r.Cleanup != CleanupSucceeded {
		t.Fatalf("report = %s", r)
	}
	if _, _, live := f.Counts(); live != 0 {
		t.Fatalf("%d clusters left after cleanup", live)
	}
}

func TestCreateErrorAfterAllocationStillDeletes(t *testing.T) {
	f := lifecycletest.NewFake()
	f.CreateAllocatesThenFails = true
	e := NewEnv(f, readyOK, "kc", fastTimeouts)
	if err := e.Start(context.Background()); err == nil {
		t.Fatal("start succeeded, want provisioning error")
	}
	r := e.Close(CauseOK)
	if r.Cause != CauseProvisioning || r.Cleanup != CleanupSucceeded {
		t.Fatalf("report = %s", r)
	}
	if _, _, live := f.Counts(); live != 0 {
		t.Fatal("partially created cluster leaked")
	}
}

func TestSetupAndCleanupErrorsAreBothPreserved(t *testing.T) {
	f := lifecycletest.NewFake()
	f.CreateAllocatesThenFails = true
	f.DeleteKeepsCluster = true
	f.DeleteErrs = slices.Repeat([]error{errors.New("docker rm failed")}, 1000)
	to := fastTimeouts
	to.RetryInterval = time.Hour
	to.Cleanup = time.Second
	e := NewEnv(f, readyOK, "kc", to)
	_ = e.Start(context.Background())
	r := e.Close(CauseOK)
	if r.SetupErr == nil || r.CleanupErr == nil {
		t.Fatalf("lost an error: %s", r)
	}
	if r.Cleanup != CleanupFailed || !slices.Equal(r.Remaining, []string{r.Cluster}) {
		t.Fatalf("report = %s", r)
	}
}

func TestTransientDeleteFailureIsRetried(t *testing.T) {
	f := lifecycletest.NewFake()
	f.DeleteErrs = []error{errors.New("docker rm -f exit status 1")}
	e := NewEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	if r := e.Close(CauseOK); r.Cleanup != CleanupSucceeded {
		t.Fatalf("report = %s", r)
	}
	if _, deletes, _ := f.Counts(); deletes < 2 {
		t.Fatalf("deletes = %d, want a retry", deletes)
	}
}

func TestHungCreateIsAbandonedAndReportedUncertain(t *testing.T) {
	f := lifecycletest.NewFake()
	f.CreateBlock = make(chan struct{})
	defer close(f.CreateBlock)
	e := NewEnv(f, readyOK, "kc", fastTimeouts)
	began := time.Now()
	err := e.Start(context.Background())
	if err == nil || e.report.Cause != CauseDeadline {
		t.Fatalf("start err=%v report=%s", err, e.report)
	}
	if took := time.Since(began); took > fastTimeouts.Create+time.Second {
		t.Fatalf("start blocked for %s", took)
	}
	r := e.Close(CauseOK)
	if r.Cleanup != CleanupUncertain {
		t.Fatalf("cleanup = %s, want uncertain while create is in flight", r.Cleanup)
	}
	if _, deletes, _ := f.Counts(); deletes == 0 {
		t.Fatal("no delete attempted for a possibly allocated cluster")
	}
}

func TestHungDeleteIsBoundedByCleanupBudget(t *testing.T) {
	f := lifecycletest.NewFake()
	f.DeleteBlock = make(chan struct{})
	defer close(f.DeleteBlock)
	e := NewEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	began := time.Now()
	r := e.Close(CauseOK)
	if took := time.Since(began); took > fastTimeouts.Cleanup+time.Second {
		t.Fatalf("close blocked for %s", took)
	}
	if r.Cleanup == CleanupSucceeded {
		t.Fatalf("report = %s, want unconfirmed cleanup", r)
	}
}

func TestCancellationDuringReadinessStillCleansUp(t *testing.T) {
	f := lifecycletest.NewFake()
	ctx, cancel := context.WithCancel(context.Background())
	ready := func(ctx context.Context, _ string) error {
		cancel()
		<-ctx.Done()
		return fmt.Errorf("nodes not Ready; last observed: NotReady: %w", ctx.Err())
	}
	e := NewEnv(f, ready, "kc", fastTimeouts)
	if err := e.Start(ctx); err == nil {
		t.Fatal("start succeeded after cancellation")
	}
	r := e.Close(CauseOK)
	if r.Cause != CauseCanceled || r.Phase != PhaseWaitUsable || r.Cleanup != CleanupSucceeded {
		t.Fatalf("report = %s", r)
	}
}

func TestReadinessTimeoutKeepsLastObservation(t *testing.T) {
	f := lifecycletest.NewFake()
	ready := func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return fmt.Errorf("last observed: node kh-x NotReady (KubeletNotReady): %w", ctx.Err())
	}
	e := NewEnv(f, ready, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	r := e.Close(CauseOK)
	if r.Cause != CauseDeadline || r.SetupErr == nil || !strings.Contains(r.SetupErr.Error(), "KubeletNotReady") {
		t.Fatalf("report = %s", r)
	}
}

func TestPreflightFailureAllocatesNothing(t *testing.T) {
	f := lifecycletest.NewFake()
	f.ListErr = errors.New("cannot connect to the docker daemon")
	e := NewEnv(f, readyOK, "kc", fastTimeouts)
	if err := e.Start(context.Background()); err == nil {
		t.Fatal("start succeeded without a runtime")
	}
	r := e.Close(CauseOK)
	creates, deletes, _ := f.Counts()
	if r.Cause != CausePreflight || r.Cleanup != CleanupNotNeeded || creates != 0 || deletes != 0 {
		t.Fatalf("report=%s creates=%d deletes=%d", r, creates, deletes)
	}
}

func TestNameCollisionNeverDeletesTheOtherCluster(t *testing.T) {
	f := lifecycletest.NewFake()
	f.CreateErr = fmt.Errorf("node(s) already exist: %w", ErrNameTaken)
	e := NewEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	r := e.Close(CauseOK)
	if _, deletes, _ := f.Counts(); deletes != 0 || r.Cleanup != CleanupNotNeeded {
		t.Fatalf("report=%s deletes=%d", r, deletes)
	}
}

func TestCloseTwiceDeletesOnceAndReturnsSameReport(t *testing.T) {
	f := lifecycletest.NewFake()
	e := NewEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	first, second := e.Close(CauseOK), e.Close(CauseTestFailed)
	if first.String() != second.String() {
		t.Fatalf("reports differ:\n%s\n%s", first, second)
	}
	if _, deletes, _ := f.Counts(); deletes != 1 {
		t.Fatalf("deletes = %d", deletes)
	}
}

func TestTestFailureIsReportedApartFromCleanup(t *testing.T) {
	f := lifecycletest.NewFake()
	e := NewEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	if r := e.Close(CauseTestFailed); r.Cause != CauseTestFailed || r.Cleanup != CleanupSucceeded {
		t.Fatalf("report = %s", r)
	}
}

func TestParallelEnvsGetDistinctClusters(t *testing.T) {
	f := lifecycletest.NewFake()
	var wg sync.WaitGroup
	var mu sync.Mutex
	names := map[string]bool{}
	for range 50 {
		wg.Go(func() {
			e := NewEnv(f, readyOK, "kc", fastTimeouts)
			if err := e.Start(context.Background()); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			names[e.report.Cluster] = true
			mu.Unlock()
			e.Close(CauseOK)
		})
	}
	wg.Wait()
	if _, _, live := f.Counts(); len(names) != 50 || live != 0 {
		t.Fatalf("distinct=%d live=%d", len(names), live)
	}
}

func TestHungPreflightIsBounded(t *testing.T) {
	f := lifecycletest.NewFake()
	f.ListBlock = make(chan struct{})
	defer close(f.ListBlock)
	e := NewEnv(f, readyOK, "kc", fastTimeouts)
	began := time.Now()
	err := e.Start(context.Background())
	if took := time.Since(began); err == nil || took > fastTimeouts.Preflight+time.Second {
		t.Fatalf("start err=%v after %s", err, took)
	}
	if r := e.Close(CauseOK); r.Cause != CauseDeadline || r.Phase != PhaseValidating || r.Cleanup != CleanupNotNeeded {
		t.Fatalf("report = %s", r)
	}
}

// lateCreateProvider lets a hung Create finish, with nodes, right after the
// first cleanup List has taken its snapshot.
type lateCreateProvider struct {
	*lifecycletest.Fake
	lists    int
	release  chan struct{}
	finished chan struct{}
}

func (p *lateCreateProvider) Create(name, kc string) error {
	<-p.release
	err := p.Fake.Create(name, kc)
	close(p.finished)
	return err
}

func (p *lateCreateProvider) List() ([]string, error) {
	names, err := p.Fake.List()
	p.lists++
	if p.lists == 2 {
		close(p.release)
		<-p.finished
		time.Sleep(10 * time.Millisecond)
	}
	return names, err
}

func TestCreateFinishingDuringCleanupIsNotReportedClean(t *testing.T) {
	p := &lateCreateProvider{Fake: lifecycletest.NewFake(), release: make(chan struct{}), finished: make(chan struct{})}
	e := NewEnv(p, readyOK, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	r := e.Close(CauseOK)
	_, _, live := p.Counts()
	if r.Cleanup == CleanupSucceeded && live != 0 {
		t.Fatalf("cleanup=succeeded with %d clusters present: %s", live, r)
	}
	if r.Cleanup != CleanupSucceeded || live != 0 {
		t.Fatalf("want the late cluster deleted on retry, got live=%d report=%s", live, r)
	}
}

func TestCleanupErrorKeepsRealDeleteCause(t *testing.T) {
	f := lifecycletest.NewFake()
	f.DeleteKeepsCluster = true
	f.DeleteDelay = 120 * time.Millisecond
	f.DeleteErrs = slices.Repeat([]error{errors.New("docker rm -f: permission denied")}, 1000)
	e := NewEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	r := e.Close(CauseOK)
	if r.CleanupErr == nil || !strings.Contains(r.CleanupErr.Error(), "permission denied") {
		t.Fatalf("real delete failure lost: %s", r)
	}
}

func TestUnconfirmedDisposalIsUncertainNotFailed(t *testing.T) {
	f := lifecycletest.NewFake()
	e := NewEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	f.Mu.Lock()
	f.ListErr = errors.New("docker ps: daemon not responding")
	f.Mu.Unlock()
	r := e.Close(CauseOK)
	if r.Cleanup != CleanupUncertain || len(r.Remaining) != 0 || !strings.Contains(r.CleanupErr.Error(), "daemon not responding") {
		t.Fatalf("report = %s", r)
	}
}

func TestCreateDeadlineErrorNamesTheBudgetThatFired(t *testing.T) {
	f := lifecycletest.NewFake()
	f.CreateBlock = make(chan struct{})
	defer close(f.CreateBlock)
	e := NewEnv(f, readyOK, "kc", Timeouts{Preflight: time.Second, Create: time.Hour, Ready: time.Hour, Cleanup: 100 * time.Millisecond, RetryInterval: 10 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := e.Start(ctx)
	if err == nil || strings.Contains(err.Error(), "1h0m0s") || !strings.Contains(err.Error(), "setup deadline") {
		t.Fatalf("error names the 1h create budget, but the 150ms setup deadline fired: %v", err)
	}
	e.Close(CauseOK)
}

// flakyListProvider lists once successfully during cleanup, then every List
// fails, while Delete eventually succeeds.
type flakyListProvider struct {
	*lifecycletest.Fake
	lists int
}

func (p *flakyListProvider) List() ([]string, error) {
	p.lists++
	if p.lists > 2 {
		return nil, errors.New("docker ps: i/o timeout")
	}
	return p.Fake.List()
}

func TestClusterGoneAfterLaterDeleteIsUncertainNotFailed(t *testing.T) {
	p := &flakyListProvider{Fake: lifecycletest.NewFake()}
	p.DeleteErrs = []error{errors.New("docker rm -f: device or resource busy")}
	e := NewEnv(p, readyOK, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	r := e.Close(CauseOK)
	if _, _, live := p.Counts(); live != 0 {
		t.Fatalf("fixture broken: %d clusters left", live)
	}
	if r.Cleanup != CleanupUncertain || len(r.Remaining) != 0 {
		t.Fatalf("cluster is gone but report = %s", r)
	}
}

// slowRetryDeleteProvider fails the first Delete, then makes the retry run
// past the cleanup budget before it removes the cluster.
type slowRetryDeleteProvider struct {
	*lifecycletest.Fake
	deletes int
}

func (p *slowRetryDeleteProvider) Delete(name, kc string) error {
	p.deletes++
	if p.deletes == 1 {
		return errors.New("docker rm -f: device or resource busy")
	}
	time.Sleep(fastTimeouts.Cleanup)
	return p.Fake.Delete(name, kc)
}

func TestDeleteStillRunningWhenBudgetEndsIsUncertainNotFailed(t *testing.T) {
	p := &slowRetryDeleteProvider{Fake: lifecycletest.NewFake()}
	e := NewEnv(p, readyOK, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	if r := e.Close(CauseOK); r.Cleanup != CleanupUncertain || len(r.Remaining) != 0 {
		t.Fatalf("a delete was still running, so presence is unconfirmed, but report = %s", r)
	}
}

func TestHungPreflightStopsAtTheSetupDeadline(t *testing.T) {
	f := lifecycletest.NewFake()
	f.ListBlock = make(chan struct{})
	defer close(f.ListBlock)
	to := fastTimeouts
	to.Preflight = time.Hour
	e := NewEnv(f, readyOK, "kc", to)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	began := time.Now()
	err := e.Start(ctx)
	if took := time.Since(began); err == nil || took > time.Second || !strings.Contains(err.Error(), "setup deadline") {
		t.Fatalf("preflight ignored the setup deadline: took %s, err %v", took, err)
	}
}

func TestReadinessCutBySetupDeadlineSaysSo(t *testing.T) {
	f := lifecycletest.NewFake()
	ready := func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return fmt.Errorf("cluster not usable; last observed: node NotReady: %w", ctx.Err())
	}
	to := fastTimeouts
	to.Ready = time.Hour
	e := NewEnv(f, ready, "kc", to)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := e.Start(ctx)
	e.Close(CauseOK)
	if err == nil || !strings.Contains(err.Error(), "setup deadline") {
		t.Fatalf("readiness error does not name the setup deadline: %v", err)
	}
}

// kindDeleteProvider behaves like kind's delete.Cluster: the second Delete
// removes the nodes and still returns the kubeconfig error. After that,
// listing fails.
type kindDeleteProvider struct {
	*lifecycletest.Fake
	deletes, lists int
}

func (p *kindDeleteProvider) Delete(name, kc string) error {
	p.deletes++
	if p.deletes == 1 {
		return errors.New("docker rm -f: device or resource busy")
	}
	_ = p.Fake.Delete(name, kc)
	return errors.New("failed to lock config file")
}

func (p *kindDeleteProvider) List() ([]string, error) {
	p.lists++
	if p.lists > 2 {
		return nil, errors.New("docker ps: i/o timeout")
	}
	return p.Fake.List()
}

func TestDeleteThatRemovedNodesButErroredIsNotReportedFailed(t *testing.T) {
	p := &kindDeleteProvider{Fake: lifecycletest.NewFake()}
	e := NewEnv(p, readyOK, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	r := e.Close(CauseOK)
	if _, _, live := p.Counts(); live != 0 || r.Cleanup != CleanupUncertain || len(r.Remaining) != 0 {
		t.Fatalf("live=%d report=%s", live, r)
	}
	if !strings.Contains(r.CleanupErr.Error(), "i/o timeout") {
		t.Fatalf("uncertain cleanup does not say why: %v", r.CleanupErr)
	}
}

func TestUncertainCleanupNamesTheInFlightCreate(t *testing.T) {
	f := lifecycletest.NewFake()
	f.CreateBlock = make(chan struct{})
	defer close(f.CreateBlock)
	e := NewEnv(f, readyOK, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	if r := e.Close(CauseOK); r.CleanupErr == nil || !strings.Contains(r.CleanupErr.Error(), "create still in flight") {
		t.Fatalf("report = %s", r)
	}
}

func TestPreflightCutBySetupDeadlineIsCauseDeadline(t *testing.T) {
	f := lifecycletest.NewFake()
	f.ListBlock = make(chan struct{})
	defer close(f.ListBlock)
	to := fastTimeouts
	to.Preflight = time.Hour
	e := NewEnv(f, readyOK, "kc", to)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = e.Start(ctx)
	if r := e.Close(CauseOK); r.Cause != CauseDeadline {
		t.Fatalf("report = %s", r)
	}
}

func TestReportRecordsPhaseDurations(t *testing.T) {
	f := lifecycletest.NewFake()
	f.DeleteDelay = 20 * time.Millisecond
	ready := func(context.Context, string) error { time.Sleep(20 * time.Millisecond); return nil }
	e := NewEnv(f, ready, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	r := e.Close(CauseOK)
	if r.CreateDuration <= 0 || r.ReadyDuration < 20*time.Millisecond || r.DeleteDuration < 20*time.Millisecond {
		t.Fatalf("durations not recorded: %s", r)
	}
}

func TestReadinessErrorOutsideAnyDeadlineIsCauseReadiness(t *testing.T) {
	ready := func(context.Context, string) error { return errors.New("load kubeconfig: no such file") }
	e := NewEnv(lifecycletest.NewFake(), ready, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	if r := e.Close(CauseOK); r.Cause != CauseReadiness || r.Phase != PhaseWaitUsable {
		t.Fatalf("report = %s", r)
	}
}

func TestNameAlreadyListedAtPreflightIsNeverCreatedOrDeleted(t *testing.T) {
	f := lifecycletest.NewFake()
	e := NewEnv(f, readyOK, "kc", fastTimeouts)
	f.Inventory[string(e.name)] = true
	if err := e.Start(context.Background()); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("start err = %v, want ErrNameTaken", err)
	}
	r := e.Close(CauseOK)
	creates, deletes, live := f.Counts()
	if creates != 0 || deletes != 0 || live != 1 || r.Cleanup != CleanupNotNeeded {
		t.Fatalf("creates=%d deletes=%d live=%d report=%s", creates, deletes, live, r)
	}
}

// slowCollisionProvider: the name is not taken at preflight, but another
// owner creates it before kind's own check answers, after our deadline.
type slowCollisionProvider struct {
	*lifecycletest.Fake
	release chan struct{}
}

func (p *slowCollisionProvider) Create(name, _ string) error {
	p.Mu.Lock()
	p.Inventory[name] = true
	p.Mu.Unlock()
	<-p.release
	return fmt.Errorf("node(s) already exist: %w", ErrNameTaken)
}

func TestCollisionReportedAfterTheCreateDeadlineStopsCleanup(t *testing.T) {
	p := &slowCollisionProvider{Fake: lifecycletest.NewFake(), release: make(chan struct{})}
	e := NewEnv(p, readyOK, "kc", fastTimeouts)
	_ = e.Start(context.Background())
	close(p.release)
	time.Sleep(20 * time.Millisecond)
	r := e.Close(CauseOK)
	if _, deletes, live := p.Counts(); deletes != 0 || live != 1 || r.Cleanup != CleanupNotNeeded {
		t.Fatalf("deleted another owner's cluster: deletes=%d live=%d report=%s", deletes, live, r)
	}
}

func TestDeleteRunningAtBudgetEndIsAlwaysUncertain(t *testing.T) {
	to := fastTimeouts
	to.Cleanup = 2 * time.Millisecond
	outcomes := map[CleanupOutcome]int{}
	for range 500 {
		f := lifecycletest.NewFake()
		e := NewEnv(f, readyOK, "kc", to)
		_ = e.Start(context.Background())
		f.DeleteBlock = make(chan struct{})
		outcomes[e.Close(CauseOK).Cleanup]++
		close(f.DeleteBlock)
	}
	if outcomes[CleanupUncertain] != 500 {
		t.Fatalf("outcomes = %v, want uncertain every time", outcomes)
	}
}

// countingProvider counts List calls that start after a deadline.
type countingProvider struct {
	*lifecycletest.Fake
	after     atomic.Pointer[time.Time]
	lateLists atomic.Int32
}

func (p *countingProvider) List() ([]string, error) {
	if after := p.after.Load(); after != nil && time.Now().After(*after) {
		p.lateLists.Add(1)
	}
	return p.Fake.List()
}

func TestNoListStartsAfterTheCleanupBudget(t *testing.T) {
	p := &countingProvider{Fake: lifecycletest.NewFake()}
	to := fastTimeouts
	to.Cleanup = 50 * time.Millisecond
	e := NewEnv(p, readyOK, "kc", to)
	_ = e.Start(context.Background())
	p.Mu.Lock()
	p.DeleteBlock = make(chan struct{})
	p.Mu.Unlock()
	defer close(p.DeleteBlock)
	after := time.Now().Add(to.Cleanup)
	p.after.Store(&after)
	e.Close(CauseOK)
	time.Sleep(50 * time.Millisecond)
	if n := p.lateLists.Load(); n != 0 {
		t.Fatalf("%d List calls started after the cleanup budget ended", n)
	}
}

func TestReportLineNamesTheProfileOnlyWhenOneWasInstalled(t *testing.T) {
	r := Report{Cluster: "kh-1", Phase: PhaseReady, Cause: CauseOK, Cleanup: CleanupSucceeded,
		CreateDuration: 7900 * time.Millisecond, ReadyDuration: 16800 * time.Millisecond, DeleteDuration: 460 * time.Millisecond}
	const plain = "kubeharness cluster=kh-1 phase=ready cause=ok cleanup=succeeded remaining=0 create=7.9s ready=16.8s delete=460ms"
	if got := r.String(); got != plain {
		t.Fatalf("report without a profile changed:\n got %s\nwant %s", got, plain)
	}
	r.Profile, r.InstallDuration = "gateway-api", 21340*time.Millisecond
	if got, want := r.String(), plain+" profile=gateway-api install=21.3s"; got != want {
		t.Fatalf("report with a profile:\n got %s\nwant %s", got, want)
	}
}

func TestRecordedProfileReachesTheReport(t *testing.T) {
	e := NewEnv(lifecycletest.NewFake(), readyOK, "kc", fastTimeouts)
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.RecordProfile("gateway-api", time.Second)
	if r := e.Close(CauseOK); !strings.Contains(r.String(), " profile=gateway-api install=1s") {
		t.Fatalf("report = %s", r)
	}
}

// The deadline watchdog can Close while the test goroutine records an install.
// Run with -race.
func TestRecordProfileDuringAnEarlyCloseIsSafe(t *testing.T) {
	e := NewEnv(lifecycletest.NewFake(), readyOK, "kc", fastTimeouts)
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() { e.RecordProfile("gateway-api", time.Second) })
	wg.Go(func() { e.Close(CauseDeadline) })
	wg.Wait()
}
