// Package mcpprobe is the example's MCP client for probes. It opens one
// session on a caller's HTTP client, lists tool names, and calls a tool for
// its text. The official Go SDK writes the JSON-RPC and SSE framing.
//
// Every error names the MCP step and the endpoint, and ends with the last
// HTTP exchange. Request and response headers are not part of that exchange,
// so a test JWT cannot land in a failure message. A 2xx body is left unread
// because it may be the SSE stream the SDK is still consuming.
package mcpprobe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const bodyHeadLimit = 200

// Session is one MCP session. It is not safe for concurrent use. A probe
// opens one, uses it, and closes it inside one attempt.
type Session struct {
	cs       *mcp.ClientSession
	endpoint string
	rec      *recorder
	closed   bool
}

// Open runs the MCP initialize handshake against endpoint over c.
// c is not modified. The session uses a copy whose transport records the
// last exchange. The SDK transport disables its own retries and the
// standalone SSE stream, so the caller's retry loop is the only one.
func Open(ctx context.Context, c *http.Client, endpoint string) (*Session, error) {
	if c == nil {
		return nil, &probeError{step: "initialize", endpoint: endpoint, err: fmt.Errorf("nil http client")}
	}
	rec := &recorder{next: c.Transport}
	wrapped := *c
	wrapped.Transport = rec
	client := mcp.NewClient(&mcp.Implementation{Name: "mcpprobe", Version: "v0.0.1"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           &wrapped,
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		return nil, &probeError{step: "initialize", endpoint: endpoint, err: err, exchange: rec.snapshot()}
	}
	return &Session{cs: cs, endpoint: endpoint, rec: rec}, nil
}

// ToolNames runs tools/list, follows pagination cursors, and returns the
// names in server order. A repeated cursor is an error, so a stuck server
// cannot spin the probe.
func (s *Session) ToolNames(ctx context.Context) ([]string, error) {
	var names []string
	seen := map[string]struct{}{}
	cursor := ""
	for {
		if _, ok := seen[cursor]; ok {
			return nil, s.fail("tools/list", fmt.Errorf("cursor %q repeated", cursor))
		}
		seen[cursor] = struct{}{}
		res, err := s.cs.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, s.fail("tools/list", err)
		}
		for _, tool := range res.Tools {
			names = append(names, tool.Name)
		}
		if res.NextCursor == "" {
			return names, nil
		}
		cursor = res.NextCursor
	}
}

// CallText runs tools/call name(args) and returns the concatenated text
// content. A result with IsError set fails the call and quotes that text.
func (s *Session) CallText(ctx context.Context, name string, args any) (string, error) {
	res, err := s.cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", s.fail("tools/call "+name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	got := b.String()
	if res.IsError {
		return "", s.fail("tools/call "+name, fmt.Errorf("tool error: %q", got))
	}
	return got, nil
}

// Close ends the session. A second Close is a no-op.
func (s *Session) Close() error {
	if s == nil || s.closed || s.cs == nil {
		return nil
	}
	s.closed = true
	return s.cs.Close()
}

// GatewayAnswered reports whether err came from a session whose client got
// at least one HTTP response. A nil error is false. A dial failure is false.
func GatewayAnswered(err error) bool {
	var pe *probeError
	if !errors.As(err, &pe) {
		return false
	}
	return pe.exchange.status != ""
}

func (s *Session) fail(step string, err error) error {
	return &probeError{step: step, endpoint: s.endpoint, err: err, exchange: s.rec.snapshot()}
}

// probeError is every error this package returns.
type probeError struct {
	step     string
	endpoint string
	err      error
	exchange exchange
}

func (e *probeError) Error() string {
	msg := fmt.Sprintf("mcp %s %s: %v", e.step, e.endpoint, e.err)
	if e.exchange.method != "" {
		msg += "; last exchange: " + e.exchange.String()
	}
	return msg
}

func (e *probeError) Unwrap() error { return e.err }

// exchange is the last HTTP request and response the session saw.
type exchange struct {
	method      string
	path        string
	status      string
	contentType string
	bodyHead    string
}

func (e exchange) String() string {
	var b strings.Builder
	b.WriteString(e.method)
	if e.path != "" {
		b.WriteByte(' ')
		b.WriteString(e.path)
	}
	if e.status != "" {
		b.WriteByte(' ')
		b.WriteString(e.status)
	}
	if e.success() && e.contentType != "" {
		b.WriteByte(' ')
		b.WriteString(e.contentType)
	}
	if !e.success() && e.bodyHead != "" {
		fmt.Fprintf(&b, " body %q", e.bodyHead)
	}
	return b.String()
}

func (e exchange) success() bool {
	return strings.HasPrefix(e.status, "2")
}

// recorder fills exchange. It reads a non-2xx body up to bodyHeadLimit and
// puts those bytes back, so the SDK still reads the original body.
type recorder struct {
	next http.RoundTripper
	mu   sync.Mutex
	last exchange
}

func (r *recorder) snapshot() exchange {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	next := r.next
	if next == nil {
		next = http.DefaultTransport
	}
	resp, err := next.RoundTrip(req)
	ex := exchange{method: req.Method}
	if req.URL != nil {
		ex.path = req.URL.Path
	}
	if resp != nil {
		ex.status = resp.Status
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			ex.bodyHead, resp.Body = peekBody(resp.Body, bodyHeadLimit)
		} else {
			ex.contentType = mediaType(resp.Header.Get("Content-Type"))
		}
	}
	r.mu.Lock()
	r.last = ex
	r.mu.Unlock()
	return resp, err
}

func mediaType(v string) string {
	if v == "" {
		return ""
	}
	mt, _, err := mime.ParseMediaType(v)
	if err != nil {
		return v
	}
	return mt
}

func peekBody(body io.ReadCloser, n int) (string, io.ReadCloser) {
	if body == nil {
		return "", http.NoBody
	}
	buf := make([]byte, n)
	k, _ := io.ReadFull(body, buf)
	head := append([]byte(nil), buf[:k]...)
	return string(head), &replayBody{head: head, rest: body}
}

type replayBody struct {
	head []byte
	off  int
	rest io.ReadCloser
}

func (b *replayBody) Read(p []byte) (int, error) {
	if b.off < len(b.head) {
		n := copy(p, b.head[b.off:])
		b.off += n
		return n, nil
	}
	return b.rest.Read(p)
}

func (b *replayBody) Close() error { return b.rest.Close() }
