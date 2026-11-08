# 34. Gateways expose one protocol-neutral probe wait

Date: 2026-11-08

## Status

Accepted. Applies [31](0031-reach-a-gateway-through-its-status-address-and-a-port-forward.md) and [33](0033-agentgateway-recipe-shares-the-gateway-api-pin.md).

## Context

A01 needs several HTTP requests in one MCP session through the Gateway handle. `AwaitResponse` waits on a single request. Putting MCP types on the public API would couple the library to JSON-RPC and an SDK.

## Decision

`Gateway.AwaitProbe` retries a caller-supplied `func(ctx context.Context, c *http.Client) error` on the existing Gateway transport until it succeeds or the deadline ends. Each attempt gets a fresh client and a short context. On timeout the failure text includes the probe's last error and the Gateway evidence.

`AwaitResponse` keeps its public contract and shares the same attempt loop.

Evidence prints status for non-Service `backendRefs` on attached HTTPRoutes. The recipe derives `GroupKind` to resource mappings from the embedded CRD layers, so an `AgentgatewayBackend` appears without a hand-maintained kind list.

MCP clients and protocol oracles live in example modules such as `examples/mcp`.

## Consequences

Callers can wait for multi-request protocols without new public protocol types. A02 can wrap the probe client with its own headers. Failure text for MCP and other probes depends on the quality of the probe's error.
