# 29. Render profile manifests offline and embed them

Date: 2026-11-05

## Status

Accepted. Applies [14. Pin profile versions](0014-pin-profile-versions.md) and [24. Install Gateway API CRDs separately](0024-install-gateway-api-crds-separately.md).

## Context

Envoy Gateway and agentgateway publish their controllers and CRDs as OCI Helm charts. Gateway API publishes a release manifest per channel. CONCEPT.md question 4 asked whether installation should use client-go, the Helm SDK, external commands, or a hybrid.

Installing from charts at test time would put Helm, a registry, and the network in every test. Envoy Gateway's `install.yaml` bundles the Gateway API CRDs, which D24 forbids. S3 slice 0 also found that Helm v4 prints `Pulled:` on stdout when it templates an OCI chart, so piping `helm template` into an apply breaks.

## Decision

Each recipe's pins name every external input at one exact version: the Gateway API version, channel, and sha256; each chart's OCI reference, version, manifest digest, and values; and every image by digest. `hack/render`, a separate Go module, reads the pins. It downloads the Gateway API manifest and checks its sha256. It pulls each chart into memory with the Helm v4 Go SDK, checks the manifest digest, and templates it as `helm template` does, hooks included. It writes each layer with a header naming its source, version, and sha256.

The library embeds the layers with `go:embed` and applies them with the existing client-go server-side apply ([20](0020-server-side-apply-and-loopback-port-forward.md)). The library module does not depend on Helm, and tests reach no network for manifests.

CI runs `go -C hack/render run . -check`, which fails when the committed layers differ from a fresh render. Unit tests hold every recipe to its invariants: Gateway API CRDs only in their own layer, no CRDs in the controller or class layers, the Gateway API bundle version and channel equal to the pins, the images in the layers exactly the pinned images, and exactly one GatewayClass `kubeharness` with the recipe's `controllerName` and a ClusterIP proxy Service.

## Consequences

Installs are offline and deterministic, and a version bump is a reviewable diff of pins and rendered YAML. The Gateway API and Envoy Gateway layers add about 3.7 MB to test binaries that import the library.

Helm hooks become plain manifests. Envoy Gateway's certgen Job is applied with the controller, and the controller Pod waits for the certificate Secret it creates, so the rollout wait covers it. A chart that depends on hook ordering beyond that would need its layer split.

The controller namespace is a Namespace object that render prepends, as `helm install --create-namespace` does.

Kubelet still pulls the pinned controller and proxy images from their registries. Preloading them is S3 slice 5, gated on measurements.
