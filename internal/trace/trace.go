// Package trace records what one Harness did to its cluster as JSON Lines, so
// a run can be replayed (docs/figure) or read after a failure.
//
// Every Harness writes one trace into t.ArtifactDir(). go test keeps that
// directory with -artifacts and deletes it otherwise, so kubeharness has no
// switch of its own. kubeharness never reads a trace back.
//
// A trace holds what a failure message could already print: refs, object
// names, condition and rollout text, status codes, and the head of a
// response body the caller read. It never holds kubeconfig contents, request
// headers, query strings, userinfo, request bodies, or Pod logs. This package
// is the one place that applies that rule: every free-text field goes through
// clean, and every URL through URL.
package trace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Version is the schema version in every trace's first line. Bump it when an
// event's field changes meaning.
const Version = 1

// FileName is the trace's name inside t.ArtifactDir(). The cluster name keeps
// two Harnesses in one test apart.
func FileName(cluster string) string { return "kubeharness-trace-" + cluster + ".jsonl" }

// Fn is the Harness call an event belongs to: the exported method the test
// called, or Cleanup for the disposal New registered.
type Fn string

const (
	FnNew            Fn = "New"
	FnInstallGateway Fn = "InstallGateway"
	FnLoadImage      Fn = "LoadImage"
	FnApply          Fn = "Apply"
	FnDelete         Fn = "Delete"
	FnWaitReady      Fn = "WaitReady"
	FnRunJob         Fn = "RunJob"
	FnForward        Fn = "Forward"
	FnGateway        Fn = "Gateway"
	FnAwaitRoute     Fn = "AwaitRoute"
	FnAwaitResponse  Fn = "AwaitResponse"
	FnAwaitProbe     Fn = "AwaitProbe"
	FnCleanup        Fn = "Cleanup"
)

// On is what a wait loop polls.
type On string

const (
	OnUsable   On = "usable"
	OnCRDs     On = "crds"
	OnRollout  On = "rollout"
	OnClass    On = "class"
	OnJob      On = "job"
	OnGateway  On = "gateway"
	OnRoute    On = "route"
	OnResponse On = "response"
	OnProbe    On = "probe"
)

// ObjState is what happened to one manifest object.
type ObjState string

const (
	Applied  ObjState = "applied"  // the server-side apply patch returned
	Deleting ObjState = "deleting" // the delete request was accepted
	Gone     ObjState = "gone"     // the server answered NotFound
)

// Via is how a request reached the Gateway transport.
type Via string

const (
	ViaClient Via = "client" // Gateway.Client(), from any goroutine
	ViaAwait  Via = "await"  // one AwaitResponse attempt
	ViaProbe  Via = "probe"  // a request the probe sent in one AwaitProbe attempt
)

// Recorder owns one trace. A nil *Recorder records nothing, and so does every
// *Scope it hands out.
//
// The test goroutine, the deadline watchdog, and every goroutine using a
// Gateway.Client write to it. mu serializes their lines and stamps "at" under
// the lock, so the file is one timeline in non-decreasing time order.
type Recorder struct {
	ids    atomic.Int64
	redact []string

	mu     sync.Mutex
	w      io.Writer
	now    func() time.Time
	start  time.Time
	err    error
	closed bool
	waits  map[waitKey]waitState
}

type waitKey struct {
	call    int64
	on      On
	subject string
}

type waitState struct {
	state string
	done  bool
}

// Start writes the header line and returns the recorder. A nil w gives a nil
// recorder. Every string in redact, such as the kubeconfig path, is replaced
// wherever it appears in free text. A failed write disables the trace and
// never fails the test; Close reports it.
func Start(w io.Writer, test, cluster string, redact ...string) *Recorder {
	return start(w, time.Now, test, cluster, redact...)
}

func start(w io.Writer, now func() time.Time, test, cluster string, redact ...string) *Recorder {
	if w == nil {
		return nil
	}
	r := &Recorder{w: w, now: now, waits: map[waitKey]waitState{}}
	for _, s := range redact {
		if s != "" {
			r.redact = append(r.redact, s)
		}
	}
	r.start = now()
	r.write(evTrace, header{V: Version, Test: test, Cluster: cluster, Start: r.start.UTC().Format("2006-01-02T15:04:05.000Z")})
	return r
}

// Close closes w if it is an io.Closer. Later events are dropped. It returns
// the first failed write or the close error. Safe on nil and twice.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	var closeErr error
	if c, ok := r.w.(io.Closer); ok {
		closeErr = c.Close()
	}
	return errors.Join(r.err, closeErr)
}

// Call records the start of one Harness call and returns its scope. Events
// written through the scope carry the call's id as "in".
func (r *Recorder) Call(fn Fn, arg string) *Scope {
	if r == nil {
		return nil
	}
	s := &Scope{r: r, call: r.ids.Add(1)}
	r.write(evCall, call{ID: s.call, Fn: fn, Arg: r.clean(arg)})
	return s
}

// Root is the scope for events outside any call, such as Gateway.Client
// requests. Their "in" is omitted.
func (r *Recorder) Root() *Scope {
	if r == nil {
		return nil
	}
	return &Scope{r: r}
}

// Scope is one call's handle on the trace. Every method is a no-op on nil.
type Scope struct {
	r    *Recorder
	call int64
}

// Root is the recorder's root scope.
func (s *Scope) Root() *Scope {
	if s == nil {
		return nil
	}
	return s.r.Root()
}

// Return records the end of the call. failed says the test became failed
// during the call, which is what a t.Fatalf inside it does.
func (s *Scope) Return(failed bool) {
	if s == nil {
		return
	}
	s.r.write(evReturn, ret{ID: s.call, Failed: failed})
}

// Phase records a lifecycle phase.
func (s *Scope) Phase(phase string) {
	if s == nil {
		return
	}
	s.r.write(evPhase, phaseEvent{Phase: phase})
}

// Wait records one observation of a wait loop. state is the loop's own "last
// observed" text, the one its timeout error quotes, or what it saw when done.
// An observation equal to the previous one for the same call, loop, and
// subject is dropped, so a 500ms poll writes a line only when what it sees
// changes.
func (s *Scope) Wait(on On, subject, state string, done bool) {
	if s == nil {
		return
	}
	subject, state = s.r.clean(subject), s.r.clean(state)
	if !s.r.changed(waitKey{s.call, on, subject}, waitState{state, done}) {
		return
	}
	s.r.write(evWait, wait{In: s.call, On: on, Subject: subject, State: state, Done: done})
}

// Object records what happened to one manifest object: its apiVersion, such
// as "gateway.networking.k8s.io/v1" or "v1", and its kind-qualified ref, such
// as "HTTPRoute/shop/pay".
func (s *Scope) Object(api, ref string, st ObjState) {
	if s == nil {
		return
	}
	s.r.write(evObject, object{In: s.call, API: api, Obj: ref, State: st})
}

// Pod records one Pod behind owner, a kind-qualified ref such as
// "Deployment/shop/orders" or "Service/shop/orders". A Pod is always core
// "v1", so the line's api is fixed.
func (s *Scope) Pod(owner, pod string, ready bool) {
	if s == nil {
		return
	}
	s.r.write(evPod, podEvent{In: s.call, API: "v1", Owner: owner, Pod: pod, Ready: ready})
}

// Forward records a port-forward that is up: the Service as "namespace/name",
// its port, the Pod the forward picked, and the loopback address.
func (s *Scope) Forward(svc string, port int32, pod, local string) {
	if s == nil {
		return
	}
	s.r.write(evForward, forward{In: s.call, Svc: svc, Port: port, Pod: pod, Local: local})
}

// Report records the report line, the same string t.Log prints.
func (s *Scope) Report(line string) {
	if s == nil {
		return
	}
	s.r.write(evReport, report{In: s.call, Line: s.r.clean(line)})
}

type ctxKey struct{}

// With returns ctx carrying s.
func With(ctx context.Context, s *Scope) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// From returns the scope ctx carries, or nil.
func From(ctx context.Context) *Scope {
	s, _ := ctx.Value(ctxKey{}).(*Scope)
	return s
}

// URL is the one redaction of a URL: scheme, host, and path. Userinfo, query,
// and fragment never reach a trace.
func URL(u *url.URL) string {
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

const textLimit = 200

func (r *Recorder) clean(s string) string {
	for _, secret := range r.redact {
		s = strings.ReplaceAll(s, secret, "<redacted>")
	}
	s = strings.Join(strings.Fields(cutURLs(s)), " ")
	if utf8.RuneCountInString(s) <= textLimit {
		return s
	}
	n := 0
	for i := range s {
		if n == textLimit-1 {
			return s[:i] + "…"
		}
		n++
	}
	return s
}

// cutURLs rewrites every scheme://... token in s with URL, such as the quoted
// URL inside a client-go or net/http error.
func cutURLs(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "://")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		begin := i
		for begin > 0 && isSchemeByte(s[begin-1]) {
			begin--
		}
		end := i + len("://")
		for end < len(s) && !strings.ContainsRune(" \t\r\n\"'`<>", rune(s[end])) {
			end++
		}
		b.WriteString(s[:begin])
		if u, err := url.Parse(s[begin:end]); err == nil && begin < i {
			b.WriteString(URL(u))
		} else {
			b.WriteString("<url>")
		}
		s = s[end:]
	}
}

func isSchemeByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '+' || c == '-' || c == '.'
}

func (r *Recorder) changed(k waitKey, v waitState) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.waits[k]; ok && old == v {
		return false
	}
	r.waits[k] = v
	return true
}

// The line is one Write so a killed run leaves whole lines.
func (r *Recorder) write(kind ev, fields any) {
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(fields); err != nil {
		return
	}
	rest := bytes.TrimPrefix(body.Bytes(), []byte("{"))
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.err != nil {
		return
	}
	line := make([]byte, 0, len(rest)+32)
	line = append(line, `{"at":`...)
	line = strconv.AppendInt(line, r.now().Sub(r.start).Milliseconds(), 10)
	line = append(line, `,"ev":"`...)
	line = append(line, kind...)
	line = append(line, '"')
	if rest[0] != '}' {
		line = append(line, ',')
	}
	line = append(line, rest...)
	if _, err := r.w.Write(line); err != nil {
		r.err = err
	}
}

// ev is an event kind, the "ev" value of a line. testdata/golden.jsonl holds
// one line per kind, and docs/figure parses it, so a kind added here fails
// the figure check until the figure handles it.
type ev string

const (
	evTrace    ev = "trace"
	evCall     ev = "call"
	evReturn   ev = "return"
	evPhase    ev = "phase"
	evWait     ev = "wait"
	evObject   ev = "object"
	evPod      ev = "pod"
	evForward  ev = "forward"
	evRequest  ev = "request"
	evResponse ev = "response"
	evBody     ev = "body"
	evReport   ev = "report"
)

// The fields of each kind, in line order after "at" and "ev". "in" is the
// enclosing call's id and is omitted outside any call.

type header struct {
	V       int    `json:"v"`
	Test    string `json:"test"`
	Cluster string `json:"cluster"`
	Start   string `json:"start"`
}

type call struct {
	ID  int64  `json:"id"`
	Fn  Fn     `json:"fn"`
	Arg string `json:"arg,omitempty"`
}

type ret struct {
	ID     int64 `json:"id"`
	Failed bool  `json:"failed,omitempty"`
}

type phaseEvent struct {
	Phase string `json:"phase"`
}

type wait struct {
	In      int64  `json:"in,omitempty"`
	On      On     `json:"on"`
	Subject string `json:"subject"`
	State   string `json:"state,omitempty"`
	Done    bool   `json:"done,omitempty"`
}

type object struct {
	In    int64    `json:"in,omitempty"`
	API   string   `json:"api,omitempty"`
	Obj   string   `json:"obj"`
	State ObjState `json:"state"`
}

type podEvent struct {
	In    int64  `json:"in,omitempty"`
	API   string `json:"api,omitempty"`
	Owner string `json:"owner"`
	Pod   string `json:"pod"`
	Ready bool   `json:"ready"`
}

type forward struct {
	In    int64  `json:"in,omitempty"`
	Svc   string `json:"svc"`
	Port  int32  `json:"port"`
	Pod   string `json:"pod"`
	Local string `json:"local"`
}

type request struct {
	ID     int64  `json:"id"`
	In     int64  `json:"in,omitempty"`
	Via    Via    `json:"via"`
	Method string `json:"method"`
	URL    string `json:"url"`
}

type response struct {
	ID     int64  `json:"id"`
	Status int    `json:"status,omitempty"`
	Err    string `json:"err,omitempty"`
	MS     int64  `json:"ms"`
}

type bodyEvent struct {
	ID   int64  `json:"id"`
	Head string `json:"head"`
}

type report struct {
	In   int64  `json:"in,omitempty"`
	Line string `json:"line"`
}
