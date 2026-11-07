# 32. Namespaced refs and Delete for ReferenceGrant proof

Date: 2026-11-07

## Status

Accepted. Extends [20. Server-side apply and loopback port-forward](0020-server-side-apply-and-loopback-port-forward.md). Uses [31. Reach a Gateway through its status address and a port-forward](0031-reach-a-gateway-through-its-status-address-and-a-port-forward.md).

## Context

G02 must show that Envoy Gateway withholds a cross-namespace backend until a ReferenceGrant allows it, and withholds it again after that grant is removed. The protected Deployment and Service live in namespace `payments`. The Gateway and HTTPRoute stay in `shop`, so a missing grant is `RefNotPermitted` rather than `NotAllowedByListeners`.

`WaitReady` and `Forward` only addressed objects in `default`. There was no public way to remove a small set of applied objects and wait until they were gone. Revoke needs that wait. A grant does not bump the HTTPRoute generation, so `AwaitRoute` must keep polling while its check rejects the old reason.

Two design candidates were compared under `.arena/g02/`. Both kept the public surface small. The chosen design leaves `WaitDeployment` and `StartForward` on a namespace string and a name string, puts the only ref parse on the harness, and owns delete identity in one `bind` helper that `Apply` and `Delete` share.

## Decision

`WaitReady` and `Forward` take the same ref string the gateway methods already take. `"name"` means namespace `default`. `"namespace/name"` means that namespace and name. A bad string fails the test before the operation starts. `Harness.ref` is the only parse. Internals still take a namespace string and a name string.

`Harness.Delete(dir)` removes every manifest in `dir` in the reverse of Apply order and waits until each GET returns `NotFound`. `NotFound` on delete is success, so a second Delete of the same directory is done. A kind the API server does not serve is skipped. Propagation is background. The timeout is one minute and names remaining objects and their finalizers. Callers pass a directory that holds only what they mean to remove.

`Apply` and `Delete` share `bind`. Apply waits for an unknown kind so a CRD earlier in the same directory can make the next kind mappable. Delete does not wait. The first `NoMatch` means the kind is already gone.

G02 lives in `examples/gateway`. Denial and revoke require `ResolvedRefs=False` reason `RefNotPermitted` with `Accepted=True`, a same-namespace `/orders` answer, and a flat payments request counter. Allow requires `RouteReady`, a counted `/payments` response from that backend, and a counter increase. The counter is read through `Forward("payments/payments", 80)` and `GET /requests`. Only requests that carry `X-Kubeharness-Count: 1` increase the count. The grant object is `gateway.networking.k8s.io/v1beta1` ReferenceGrant in its own directory. The module still does not depend on `sigs.k8s.io/gateway-api`.

## Consequences

Cross-namespace workloads and grant revoke are ordinary harness calls. Bare names keep working for existing tests. Callers who put a Namespace in a Delete directory wait on Namespace finalizers. A controller that recreates a deleted name keeps the object present, so Delete runs out the deadline and lists finalizers. Evidence still does not dump ReferenceGrants. The G02 counter path is a side channel. Routed traffic still goes through `Gateway.Client`.
