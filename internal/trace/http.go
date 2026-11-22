package trace

import (
	"io"
	"net/http"
	"sync"
)

// HeadLimit is how many bytes of a body a trace or a failure message quotes.
const HeadLimit = 200

// RoundTripper wraps rt so every request it sends records a request line
// (method and URL) before sending, a response line (status or error, and
// elapsed ms) when the headers arrive, and a body line with the first
// HeadLimit bytes once the caller has read any. A nil scope returns rt itself.
//
// rt is used, never changed, so a *http.Transport passed here can still be
// cloned for each await attempt.
func (s *Scope) RoundTripper(rt http.RoundTripper, via Via) http.RoundTripper {
	if s == nil {
		return rt
	}
	return tripper{base: rt, s: s, via: via}
}

type tripper struct {
	base http.RoundTripper
	s    *Scope
	via  Via
}

func (t tripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r := t.s.r
	id := r.ids.Add(1)
	r.write(evRequest, request{ID: id, In: t.s.call, Via: t.via, Method: req.Method, URL: URL(req.URL)})
	began := r.now()
	resp, err := t.base.RoundTrip(req)
	ms := r.now().Sub(began).Milliseconds()
	if err != nil {
		r.write(evResponse, response{ID: id, Err: r.clean(err.Error()), MS: ms})
		return resp, err
	}
	r.write(evResponse, response{ID: id, Status: resp.StatusCode, MS: ms})
	// A 101 Switching Protocols body is also an io.Writer, and hiding that
	// would break the upgrade.
	if _, upgraded := resp.Body.(io.Writer); resp.Body != nil && resp.Body != http.NoBody && !upgraded {
		resp.Body = &headBody{ReadCloser: resp.Body, r: r, id: id}
	}
	return resp, nil
}

type headBody struct {
	io.ReadCloser
	r  *Recorder
	id int64

	mu   sync.Mutex
	head []byte
	done bool
}

func (b *headBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := HeadLimit - len(b.head); room > 0 && n > 0 {
		b.head = append(b.head, p[:min(n, room)]...)
	}
	if err != nil || len(b.head) == HeadLimit {
		b.flush()
	}
	return n, err
}

func (b *headBody) Close() error {
	err := b.ReadCloser.Close()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.flush()
	return err
}

func (b *headBody) flush() {
	if b.done || len(b.head) == 0 {
		return
	}
	b.done = true
	b.r.write(evBody, bodyEvent{ID: b.id, Head: string(b.head)})
}
