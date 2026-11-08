package mcpprobe

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ogarciacar/kubeharness/examples/mcp/calculator"
)

func TestOpenListCall(t *testing.T) {
	srv := httptest.NewServer(calculator.Handler())
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	s, err := Open(ctx, srv.Client(), srv.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	names, err := s.ToolNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"sum"}) {
		t.Fatalf("tools/list = %q, want [sum]", names)
	}
	got, err := s.CallText(ctx, "sum", map[string]int{"a": 40, "b": 2})
	if err != nil {
		t.Fatal(err)
	}
	if got != "42" {
		t.Fatalf("tools/call sum(40,2) = %q, want 42", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestWrongPathNamesExchange(t *testing.T) {
	srv := httptest.NewServer(calculator.Handler())
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Open(ctx, srv.Client(), srv.URL+"/nope")
	if err == nil {
		t.Fatal("Open /nope succeeded")
	}
	if !strings.Contains(err.Error(), "mcp initialize") || !strings.Contains(err.Error(), "last exchange: POST /nope 404") {
		t.Fatalf("error = %q, want mcp initialize and last exchange: POST /nope 404", err)
	}
	if !GatewayAnswered(err) {
		t.Fatalf("GatewayAnswered(%v) = false, want true", err)
	}
}

func TestDialErrorIsNotAnswered(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err = Open(ctx, &http.Client{}, "http://"+addr+"/mcp")
	if err == nil {
		t.Fatal("Open on a closed port succeeded")
	}
	if GatewayAnswered(err) {
		t.Fatalf("GatewayAnswered(%v) = true, want false", err)
	}
	if GatewayAnswered(nil) {
		t.Fatal("GatewayAnswered(nil) = true, want false")
	}
}

func TestToolErrorQuotesText(t *testing.T) {
	const text = "a and b are required"
	server := mcp.NewServer(&mcp.Implementation{Name: "boom", Version: "v0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "sum"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	})
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	s, err := Open(ctx, srv.Client(), srv.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.CallText(ctx, "sum", map[string]int{})
	if err == nil {
		t.Fatal("CallText succeeded, want a tool error")
	}
	if !strings.Contains(err.Error(), "tool error: \""+text+"\"") {
		t.Fatalf("error = %q, want it to quote %q", err, text)
	}
}

func TestHeadersNeverRecorded(t *testing.T) {
	const secret = "Bearer secret-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Leak", secret)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	base := srv.Client().Transport
	c := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.Header.Set("Authorization", secret)
		return base.RoundTrip(r)
	})}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err := Open(ctx, c, srv.URL+"/mcp")
	if err == nil {
		t.Fatal("Open succeeded, want an HTTP error")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "Authorization") {
		t.Fatalf("error recorded a header: %s", err)
	}
}

func TestBodyReplayed(t *testing.T) {
	body := strings.Repeat("a", bodyHeadLimit) + "TAIL"
	next := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Status:     "502 Bad Gateway",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})
	rec := &recorder{next: next}
	resp, err := rec.RoundTrip(httptest.NewRequest(http.MethodPost, "http://mcp.example/mcp", nil))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("body after recorder = %q, want the full %d-byte body", got, len(body))
	}
	head := rec.snapshot().bodyHead
	if head != body[:bodyHeadLimit] {
		t.Fatalf("body head = %q, want the first %d bytes", head, bodyHeadLimit)
	}
}

func TestToolNamesFollowsCursor(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "paged", Version: "v0.0.1"}, &mcp.ServerOptions{PageSize: 1})
	nop := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	}
	mcp.AddTool(server, &mcp.Tool{Name: "a"}, nop)
	mcp.AddTool(server, &mcp.Tool{Name: "b"}, nop)
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	s, err := Open(ctx, srv.Client(), srv.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	names, err := s.ToolNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"a", "b"}) {
		t.Fatalf("tools/list = %q, want [a b]", names)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
