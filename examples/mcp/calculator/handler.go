// Package calculator is A01's MCP server: one tool, sum, over Streamable HTTP.
package calculator

import (
	"context"
	"net/http"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SumIn is sum's input. AddTool infers the schema from the struct.
type SumIn struct {
	A int `json:"a" jsonschema:"first addend"`
	B int `json:"b" jsonschema:"second addend"`
}

// SumOut is sum's structured output. Callers that read only text still see
// the decimal sum, because the tool result sets that text itself.
type SumOut struct {
	Sum int `json:"sum"`
}

// Handler serves GET /healthz with 200 and the MCP Streamable HTTP endpoint
// at /mcp. The session is stateful, which is the SDK default. There is no
// SSE transport, so an SSE target fails against this process.
func Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "calculator", Version: "v0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "sum", Description: "add two integers"}, sum)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)
	mux.Handle("/mcp", mcpHandler)
	return mux
}

func healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func sum(_ context.Context, _ *mcp.CallToolRequest, in SumIn) (*mcp.CallToolResult, SumOut, error) {
	n := in.A + in.B
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: strconv.Itoa(n)}},
	}, SumOut{Sum: n}, nil
}
