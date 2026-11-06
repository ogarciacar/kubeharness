# 31. Reach a Gateway through its status address and a port-forward

Date: 2026-11-06

## Status

Accepted. Supersedes [25. Resolve the gateway address from Gateway status](0025-resolve-the-gateway-address-from-gateway-status.md). Applies [20. Server-side apply and loopback port-forward](0020-server-side-apply-and-loopback-port-forward.md) and [28. Gateway profiles are typed values installed after New](0028-gateway-profiles-are-typed-values-installed-after-new.md).

## Context

D25 set a direction before any code existed. Gateway traffic finds its address in the `Gateway` resource's status, because each controller names and places its proxy Service differently. It did not say how a test process reaches that address.

A Gateway's status address is not reachable from the host. Kind has no LoadBalancer implementation, and on Docker Desktop only loopback reaches the cluster. D20 already makes host access a client-go port-forward on loopback, and `kube.StartForward` forwards a loopback port to a ready Pod behind a Service.

D28 settled the GatewayClass. Every profile creates GatewayClass `kubeharness`, so D25's "take the `GatewayClass` from the profile" became one fixed name. D29's recipe tests require each class layer to give the proxy a ClusterIP Service.

Two design candidates for S3 slice 2 were compared. Both resolved the address to one Service and forwarded each listener port. They differed on failure handling, which the chosen design takes from both.

## Decision

`GatewayController.Gateway(ref)` calls `gateway.Attach`, which runs one sequence under a 3-minute limit:

1. Wait until the Gateway is Accepted and Programmed for its current generation. A Gateway that does not exist, or whose `gatewayClassName` is not `kubeharness.GatewayClass`, fails at once.
2. Take the first status address of type `IPAddress`. A Gateway with only `Hostname` addresses fails at once.
3. `kube.ServiceForAddress` lists Services in all namespaces and returns the one whose cluster IP is that address. Headless and ExternalName Services never match. Zero matches keep waiting. Two or more fail at once.
4. Check that every listener port is a port of that Service. Envoy Gateway's Service port equals the listener port. `portPlan` is the one place that assumption lives.
5. Start one `kube.StartForward` per listener port, each to a ready Pod behind the Service, on `127.0.0.1`.

If any step fails, `Attach` closes the forwards it started and returns no dialer, so `Gateway(ref)` never returns a handle without its forwards. Each call builds new forwards, and `t.Cleanup` closes them. Handles are not cached.

Every connection from `Gateway.Client()` goes through the handle's dialer. The dialer ignores the URL host and picks the forward by the URL port. The URL host becomes the `Host` header and, for https, the SNI name, as `curl --connect-to` does. A port with no listener fails in `Do`. If a forward has stopped, or a dial to it is refused, the dialer re-forwards once to the same Service and port. The harness `forwardTimeout`, 30 seconds, caps that re-forward. The dialer does not resolve the address again. The shared transport ignores `HTTP_PROXY` and `HTTPS_PROXY` and has a 10-second response header timeout.

The public API for this slice is `GatewayController.Gateway`, the `Gateway` type with `Client`, `AwaitRoute`, and `AwaitResponse`, and the route types `RouteStatus`, `Condition`, and `RouteReady`.

- `AwaitRoute(ref, check)` passes `check` only the HTTPRoute status that this Gateway's controller wrote for this Gateway at the route's current generation. A missing route, a route with no parentRef to this Gateway, or two status parents for this Gateway and controller fail at once. Otherwise it fails after 1 minute.
- `AwaitResponse(req, check)` sends a clone of `req` every 250ms, each with a 5-second limit, until `check` accepts a response. It fails after 1 minute with the last status line and body head.
- `RouteReady` accepts only a `RouteStatus` that `AwaitRoute` built, with Accepted and ResolvedRefs both True.

Gateway manifests use `kubeharness.GatewayClass`, as D28 decided. This record does not change that.

The module does not depend on `sigs.k8s.io/gateway-api`. `internal/gateway` reads Gateways and HTTPRoutes through the dynamic client and parses the fields it needs from unstructured objects into its own types in `state.go`.

## Consequences

A test sees no Service name, proxy Pod, or forward. It applies a Gateway and routes, calls `Gateway(ref)`, and sends requests with standard `net/http`. Failures carry the Gateway's evidence: its conditions, listeners, attached routes, proxy Pods, and controller Pods.

Each profile must fill a Gateway status address of type `IPAddress` that is the cluster IP of exactly one Service, and that Service must expose each listener port as the same port number. Envoy Gateway does both. agentgateway (S4) is not yet checked. If its Service maps ports differently, `portPlan` changes and nothing else. Hostname addresses would need their own change.

Traffic goes from the host through the API server to one proxy Pod. It skips kube-proxy, so a test does not see load spread across proxy replicas. The path still covers the Gateway, the route, the proxy, and the backend, which D11's data-plane half needs.

The resolved target is fixed when `Gateway(ref)` returns. If a Gateway's address moves to another Service during a test, the handle keeps forwarding to the old one. G03 may need that, and following the address would need its own record.

Without generated Gateway API types, reading a field that `state.go` does not parse means writing parser code. Parser tests run against captured Gateway and HTTPRoute YAML in `internal/gateway/testdata`.
