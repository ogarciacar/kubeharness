# 33. The agentgateway recipe shares the Gateway API pin

Date: 2026-11-07

## Status

Accepted. Applies [7](0007-agentgateway-gets-its-own-profile.md), [24](0024-install-gateway-api-crds-separately.md), [26](0026-agentgateway-uses-its-own-controller.md), [28](0028-gateway-profiles-are-typed-values-installed-after-new.md), [29](0029-render-profile-manifests-offline-and-embed-them.md), and [30](0030-gateway-api-tested-combination.md).

## Context

S3 slice 6 adds a second gateway profile so G01 to G03 run under Envoy Gateway and agentgateway with the same fixtures. agentgateway v1.0 declares Gateway API 1.5.0. The harness pins 1.6.1 standard for Envoy Gateway. The agentgateway chart passes the proxy image to the controller only as environment variables, and the proxy Service defaults to LoadBalancer.

## Decision

`kubeharness.AgentGateway()` returns the `gateway-agentgateway` profile: agentgateway v1.4.1 charts and images pinned by digest, controller name `agentgateway.dev/agentgateway`, and a hand-written class layer with an `AgentgatewayParameters` named `kubeharness` and GatewayClass `kubeharness`.

Every recipe installs the same Gateway API release, held in one value in `internal/profile` and checked by a recipe test. The `AgentgatewayParameters` makes the proxy Service ClusterIP through its `service.spec` overlay and pins the proxy image through its typed `image` field. The recipe tests recognize both forms.

v1.4.1 is required (not v1.0.x): it watches `TCPRoute` as `gateway.networking.k8s.io/v1`, which the v1.6.1 standard channel serves. Earlier releases watched `v1alpha2` and never became ready against standard-channel CRDs.

## Consequences

The tested combination for `gateway-agentgateway` is kind v0.33.0 (Kubernetes v1.37.0), Gateway API v1.6.1 standard, and agentgateway v1.4.1.

The controller also creates its default GatewayClass `agentgateway`. Fixtures ignore it.

The examples' gateway CI job runs each scenario twice.

agentgateway's CRDs ship `x-kubernetes-validations` rules whose estimated CEL cost exceeds the API server budget on the tested Kind combination, so `hack/render` strips those rules from the embedded CRD layer (`Chart.StripCELValidations`). OpenAPI types remain; Kind still rejects invalid field types.

`Pins.LoadImages` is set for this recipe so InstallGateway docker-pulls and Kind-loads the pinned images before apply. Kind's containerd cannot always resolve this registry's multi-arch index digests.
