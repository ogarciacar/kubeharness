# 28. Gateway profiles are typed values installed after New

Date: 2026-11-05

## Status

Accepted. Refines [20. Server-side apply and loopback port-forward](0020-server-side-apply-and-loopback-port-forward.md), which kept `New` free of options.

## Context

The README's early sketch selected a profile with `New(t, WithProfile("gateway-api"))` and reached traffic through `h.Gateway()`. That shape has three problems. A string name fails only at run time. Gateway methods would exist on every `Harness`, so calling one on a cluster without a controller compiles and fails later. Installing during `New` would also move controller installation into the S1 lifecycle state machine, which [16](0016-one-owned-cluster-per-test.md), [18](0018-deadlines-end-before-the-go-test-timeout.md), and [19](0019-truthful-cleanup-reports.md) were proven against.

Two independent design candidates for S3 were compared. Both modeled a profile as data with one install algorithm. The chosen candidate made install a method that returns a typed controller. The other passed the profile to `New`, mainly to pick a node image per profile; the Kubernetes version question was settled separately ([30](0030-gateway-api-tested-combination.md)).

## Decision

A gateway profile is an opaque `GatewayProfile` value from a constructor such as `EnvoyGateway()`. The zero value is rejected. A test installs it on an unchanged `New(t)` with `h.InstallGateway(p)`, which returns a `*GatewayController`. Gateway calls live on that controller, so a gateway call on a cluster without a controller does not compile.

`InstallGateway` runs under its own operation deadline of 4 minutes, bounded by [18](0018-deadlines-end-before-the-go-test-timeout.md). It returns when the controller can serve Gateways: CRDs established, controller rolled out, and GatewayClass `kubeharness` Accepted for its current generation. On failure it stops the test with that evidence. Calling it again with the same profile installs nothing. A different profile fails the test, because a cluster holds one gateway profile ([7](0007-agentgateway-gets-its-own-profile.md)).

Every profile creates the same GatewayClass, exported as `kubeharness.GatewayClass`, so test manifests are the same under every profile.

Inside the library a profile is a `profile.Recipe`: plain data with pins, embedded manifest layers, the GatewayClass `controllerName`, and the controller Deployments. It has no interface and no function fields. One `profile.Install` runs every recipe.

## Consequences

`New` and the lifecycle state machine are unchanged. The report line gains `profile=` and `install=` only when a profile was installed, through `lifecycle.Env`, which stays the report's one writer.

A new profile is a new recipe value, a re-render, and a hand-written class layer. `gateway-agentgateway` (S4) adds a constructor and a recipe, not install code.

The public surface grows by four identifiers in this slice. Slice 2 adds `GatewayController.Gateway` and the traffic types.
