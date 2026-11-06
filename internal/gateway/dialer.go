package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ogarciacar/kubeharness/internal/kube"
)

// ErrClosed is returned by dials after Close, which runs when the test ends.
var ErrClosed = errors.New("gateway forwards closed: the test that opened them has ended")

// Dialer owns one Gateway's port-forwards, one per Target port, and is the
// DialContext of the Gateway's http.Transport. It ignores the dialed host and
// picks the forward by the dialed port.
//
// State is per port. Each slot is written only by the goroutine holding its
// lock, so a re-forward of one port never blocks dials on another. The slot
// map itself is fixed at construction and read without locking.
type Dialer struct {
	gw     kube.Ref
	slots  map[int32]*slot // keys are Target.Ports; never changes after openDialer
	ports  []int32         // the same keys, ascending, for error messages
	budget time.Duration   // cap on one re-forward, the forwardTimeout Attach received
	stop   context.Context // canceled by Close; aborts re-forwards in flight
	cancel context.CancelFunc

	closeOnce sync.Once
	closeErr  error
}

// slot is one listener port's forward.
type slot struct {
	port int32
	open opener
	// lock is a one-token channel instead of a sync.Mutex so that waiting
	// for a concurrent re-forward respects the waiting dial's context.
	// It holds one token when the slot is free.
	lock   chan struct{}
	cur    forward // guarded by lock; nil only after Close
	closed bool    // guarded by lock
}

// forward is a live port-forward as the dialer needs it. *kube.Forwarding
// satisfies it in production. Unit tests use a net.Listener-backed fake.
type forward interface {
	Address() string // 127.0.0.1:port
	Stopped() bool
	Close() error
}

// opener starts a forward to the Target's Service on one port, against a
// ready Pod picked at call time. In production it is kube.StartForward bound
// to the Cluster and the Target's Service. It must return when ctx ends.
type opener func(ctx context.Context, port int32) (forward, error)

// openDialer starts a forward for every Target port. Only Attach calls it.
// A Service with no ready Pod yet is retried every statusInterval until ctx
// ends. Pending on kube's "no ready pod" error, Stop on anything else. If
// any port fails, the ports already forwarded are closed before it returns,
// so a failed Attach leaves nothing running and a nil dialer. The error
// carries Evidence for t. budget is the forwardTimeout Attach received.
func openDialer(ctx, evidenceCtx context.Context, cl Cluster, t Target, budget time.Duration) (*Dialer, error) {
	d, err := startForwards(ctx, t, budget, serviceOpener(cl, t.Service))
	if err != nil {
		return nil, withEvidence(err, evidenceCtx, func(ctx context.Context) string {
			return Evidence(ctx, cl, t.Gateway, t.Service)
		})
	}
	return d, nil
}

func serviceOpener(cl Cluster, svc kube.Ref) opener {
	return func(ctx context.Context, port int32) (forward, error) {
		f, err := kube.StartForward(ctx, cl.Config, cl.Kube, svc.Namespace, svc.Name, port)
		if err != nil {
			return nil, err
		}
		return forwardingAdapter{f}, nil
	}
}

// startForwards opens one forward per Target port. Tests pass a fake opener.
// openDialer is the only production caller.
func startForwards(ctx context.Context, t Target, budget time.Duration, open opener) (*Dialer, error) {
	stop, cancel := context.WithCancel(context.Background())
	d := &Dialer{
		gw:     t.Gateway,
		slots:  make(map[int32]*slot, len(t.Ports)),
		ports:  append([]int32(nil), t.Ports...),
		budget: budget,
		stop:   stop,
		cancel: cancel,
	}
	for _, port := range t.Ports {
		f, err := openPort(ctx, open, port)
		if err != nil {
			d.abort()
			return nil, err
		}
		d.slots[port] = newSlot(port, open, f)
	}
	return d, nil
}

func newSlot(port int32, open opener, cur forward) *slot {
	s := &slot{port: port, open: open, lock: make(chan struct{}, 1), cur: cur}
	s.lock <- struct{}{}
	return s
}

// openPort tries the opener until it returns a forward, the error is not a
// missing ready Pod, or ctx ends. One attempt per statusInterval.
func openPort(ctx context.Context, open opener, port int32) (forward, error) {
	return Await(ctx, context.Background(), statusInterval, func(ctx context.Context) (forward, Verdict) {
		f, err := open(ctx, port)
		if err != nil {
			if f != nil {
				_ = f.Close()
			}
			if noReadyPod(err) {
				return nil, Pending("%s", err.Error())
			}
			return nil, Stop(err)
		}
		return f, Done()
	}, nil)
}

// noReadyPod reports kube.StartForward's missing-Pod error. That error is not
// a sentinel. The phrase is the whole signal, and it is retryable.
func noReadyPod(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no ready pod")
}

// DialContext connects to the forward for addr's port. The host is ignored.
// A port outside the Target's listeners fails. A forward that has already
// stopped is replaced before dialing. A refused dial re-forwards once, inside
// budget, and dials the replacement once. Safe for concurrent use.
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	port, err := dialPort(addr)
	if err != nil {
		return nil, err
	}
	s := d.slots[port]
	if s == nil {
		return nil, fmt.Errorf("gateway %s has no listener on port %d; listeners: %v", d.gw, port, d.ports)
	}
	cur, err := s.current(ctx)
	if err != nil {
		return nil, err
	}
	if cur.Stopped() {
		cur, err = s.replace(ctx, d, cur)
		if err != nil {
			return nil, err
		}
	}
	conn, err := dialForward(ctx, cur)
	if err == nil || !connectionRefused(err) {
		return conn, err
	}
	cur, err = s.replace(ctx, d, cur)
	if err != nil {
		return nil, err
	}
	return dialForward(ctx, cur)
}

func dialPort(addr string) (int32, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, fmt.Errorf("gateway dial %q: %w", addr, err)
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("gateway dial %q: invalid port %q", addr, port)
	}
	return int32(n), nil
}

func dialForward(ctx context.Context, f forward) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "tcp", f.Address())
}

func connectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}

func (s *slot) current(ctx context.Context) (forward, error) {
	if err := acquire(ctx, s.lock); err != nil {
		return nil, err
	}
	defer release(s.lock)
	if s.closed || s.cur == nil {
		return nil, ErrClosed
	}
	return s.cur, nil
}

// replace swaps failed for a new forward to the same Service and port, and
// returns the slot's current forward. It is idempotent per failure: if
// another dial already replaced failed, it returns that replacement without
// opening another. The re-forward runs under ctx merged with d.stop and
// capped at d.budget, retrying the opener while the Service has no ready Pod.
// If it gives up, the slot keeps the stopped forward, so the next dial sees
// Stopped and tries again.
func (s *slot) replace(ctx context.Context, d *Dialer, failed forward) (forward, error) {
	if err := acquire(ctx, s.lock); err != nil {
		return nil, err
	}
	defer release(s.lock)
	if s.closed || s.cur == nil {
		return nil, ErrClosed
	}
	if s.cur != failed {
		return s.cur, nil
	}
	_ = failed.Close()
	openCtx, cancel := bound(ctx, d.stop, d.budget)
	defer cancel()
	f, err := openPort(openCtx, s.open, s.port)
	if err != nil {
		return nil, err
	}
	s.cur = f
	return f, nil
}

// bound is ctx and stop, whichever ends first, capped at budget.
func bound(ctx, stop context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	merged, cancelMerge := mergeCancel(ctx, stop)
	limited, cancelLimit := context.WithTimeout(merged, budget)
	return limited, func() {
		cancelLimit()
		cancelMerge()
	}
}

func mergeCancel(parent, other context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(other, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func acquire(ctx context.Context, token chan struct{}) error {
	select {
	case <-token:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func release(token chan struct{}) { token <- struct{}{} }

// Close stops every forward and makes later dials fail with ErrClosed. It
// cancels re-forwards in flight first, so it waits only for the opener to
// notice, not for budget. It is safe to call more than once. Later calls
// return the first call's error.
func (d *Dialer) Close() error {
	d.closeOnce.Do(func() {
		d.cancel()
		var errs []error
		for _, port := range d.ports {
			s := d.slots[port]
			<-s.lock
			s.closed = true
			if s.cur != nil {
				if err := s.cur.Close(); err != nil {
					errs = append(errs, err)
				}
				s.cur = nil
			}
			s.lock <- struct{}{}
		}
		d.closeErr = errors.Join(errs...)
	})
	return d.closeErr
}

// abort closes forwards opened before startForwards failed. The dialer is
// not returned.
func (d *Dialer) abort() {
	d.cancel()
	for _, s := range d.slots {
		if s.cur != nil {
			_ = s.cur.Close()
		}
	}
}

// Transport is the http.Transport every Client of one Gateway shares. Proxy
// is nil, so HTTP(S)_PROXY in the environment never diverts Gateway traffic.
// ResponseHeaderTimeout bounds a request the proxy accepted but never
// answers, since Client sets no overall Timeout.
func Transport(d *Dialer) *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           d.DialContext,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConnsPerHost:   16,
	}
}

var _ forward = forwardingAdapter{}

// forwardingAdapter adapts *kube.Forwarding, whose address is a field.
type forwardingAdapter struct{ *kube.Forwarding }

func (f forwardingAdapter) Address() string { return f.Addr }
