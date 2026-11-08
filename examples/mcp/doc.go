// Package mcp holds the A01 example. A calculator MCP server runs in the
// cluster. HTTPRoute mcp on Gateway edge sends mcp.example/mcp to an
// AgentgatewayBackend. The test lists tools and calls sum through the
// Gateway handle. This module is AgentGateway only.
//
// The library does not speak MCP. Run the Kind proof from this directory:
//
//	go test -tags e2e -count=1 -timeout 20m -v -run '^TestA01' .
package mcp
