package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	attemptTimeout = 5 * time.Second
	bodyLimit      = 1 << 20 // bytes buffered for check
	bodyHead       = 200     // bytes of the last body quoted in a failure
)

// AwaitResponse sends a fresh clone of req through transport every
// responseInterval until check accepts the response. Each attempt's context
// ends after attemptTimeout, so one hung dial, including a re-forward, cannot
// use the whole wait.
//
// A req with a body and no GetBody is Stop. It cannot be sent again. A
// transport error or a check error is Pending. summarize describes that
// attempt in one line. The status in that line is whatever the proxy sent.
//
// On failure the error carries that line and Evidence for t. The Service in
// that evidence is t.Service. AwaitResponse does not look it up again.
func AwaitResponse(ctx, evidenceCtx context.Context, cl Cluster, t Target, transport *http.Transport, req *http.Request, check func(*http.Response) error) error {
	evidence := func(ctx context.Context) string {
		return Evidence(ctx, cl, t.Gateway, t.Service)
	}
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		return withEvidence(errors.New("request body cannot be replayed"), evidenceCtx, evidence)
	}
	_, err := Await(ctx, evidenceCtx, responseInterval, func(ctx context.Context) (struct{}, Verdict) {
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		defer cancel()
		// net/http dials with a context that ignores request cancellation, so a
		// re-forward would otherwise run until the dialer's own budget. This
		// attempt uses a clone so that dial ends with the attempt. Client keeps
		// the shared transport.
		tr := transportForAttempt(transport, attemptCtx)
		defer tr.CloseIdleConnections()
		resp, body, err := attempt(attemptCtx, &http.Client{Transport: tr}, req)
		if err != nil {
			return struct{}{}, Pending("%s", summarize(nil, nil, err, nil))
		}
		if checkErr := check(resp); checkErr != nil {
			return struct{}{}, Pending("%s", summarize(resp, body, nil, checkErr))
		}
		return struct{}{}, Done()
	}, evidence)
	return err
}

// attempt sends one clone of req. The response body is read into memory, up
// to bodyLimit, and replaced with a reader over those bytes. check may read
// the body. Nothing has to close it.
func attempt(ctx context.Context, c *http.Client, req *http.Request) (*http.Response, []byte, error) {
	r := req.Clone(ctx)
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, nil, err
		}
		r.Body = body
	}
	resp, err := c.Do(r)
	if err != nil {
		return nil, nil, err
	}
	if resp.Body == nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return resp, nil, nil
	}
	src := resp.Body
	buf, err := io.ReadAll(io.LimitReader(src, bodyLimit))
	closeErr := src.Close()
	if err != nil {
		return nil, nil, err
	}
	if closeErr != nil {
		return nil, nil, closeErr
	}
	resp.Body = io.NopCloser(bytes.NewReader(buf))
	return resp, buf, nil
}

// transportForAttempt dials like base, and the dial ends when ctx ends.
// The clone does not share base's idle connections. base itself is unchanged.
func transportForAttempt(base *http.Transport, ctx context.Context) *http.Transport {
	if base == nil {
		base = &http.Transport{}
	}
	tr := base.Clone()
	dial := tr.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	tr.DialContext = func(dialCtx context.Context, network, addr string) (net.Conn, error) {
		merged, cancel := mergeCancel(dialCtx, ctx)
		defer cancel()
		return dial(merged, network, addr)
	}
	return tr
}

// summarize is the one-line description of an attempt. A transport error and a
// response are different lines. The response line quotes at most bodyHead
// bytes and includes checkErr when check rejected the response.
func summarize(resp *http.Response, body []byte, err, checkErr error) string {
	if err != nil {
		return "last attempt: " + oneLine(err.Error())
	}
	if resp == nil {
		return "last attempt: no response"
	}
	head := body
	if len(head) > bodyHead {
		head = head[:bodyHead]
	}
	status := resp.Status
	if status == "" {
		status = strconv.Itoa(resp.StatusCode)
	}
	line := "last response: " + status + "; body " + strconv.Quote(string(head))
	if checkErr != nil {
		line += "; check: " + oneLine(checkErr.Error())
	}
	return line
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
