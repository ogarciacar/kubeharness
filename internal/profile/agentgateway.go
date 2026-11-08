package profile

// Digests are those of each image's multi-arch index, so kubelet resolves the
// node's platform. They were read with docker buildx imagetools inspect.
//
// v1.4.1 is the first release that watches TCPRoute as gateway.networking.k8s.io/v1,
// which the Gateway API v1.6.1 standard channel serves. v1.0.x still watched
// v1alpha2 and hung forever against standard-channel CRDs.
const (
	agentgatewayVersion   = "v1.4.1"
	agentgatewayNamespace = "agentgateway-system"
	agentgatewayRegistry  = "cr.agentgateway.dev"

	agentgatewayControllerDigest = "sha256:8b9e2bbe65b6a4b2ed93acd4fd0ede11f1eb2cfa8495e2b67c2d890e7fce8cec"
	agentgatewayProxyDigest      = "sha256:efd79355b89094a8225a9db465d9a01dc656b377f0bab458761b935a13231d29"

	// agentgatewayControllerImage is the image line the chart renders when
	// controller.image.tag carries the digest. The chart's imageTag helper
	// passes a "v"-prefixed tag through unchanged.
	agentgatewayControllerImage = agentgatewayRegistry + "/controller:" + agentgatewayVersion + "@" + agentgatewayControllerDigest

	// agentgatewayProxyImage is the data-plane image. The chart passes it to
	// the controller only as AGW_PROXY_IMAGE_* environment variables, so no
	// rendered image line names it. Instead the class layer sets it on the
	// kubeharness GatewayClass's AgentgatewayParameters as spec.image
	// {registry, repository, tag, digest}. The recipe tests compose that
	// object back into this string, so the class layer and this constant
	// cannot drift apart silently.
	agentgatewayProxyImage = agentgatewayRegistry + "/agentgateway:" + agentgatewayVersion + "@" + agentgatewayProxyDigest
)

var agentgatewayPins = Pins{
	GatewayAPI: gatewayAPI,
	// The CRD chart ships only agentgateway.dev CRDs (AgentgatewayBackend,
	// AgentgatewayParameters, AgentgatewayPolicy) and has no values, so D24
	// needs no switch here.
	ControllerCRDs: Chart{
		Ref:                 "oci://cr.agentgateway.dev/charts/agentgateway-crds",
		Version:             agentgatewayVersion,
		Digest:              "sha256:8fbdf62d1c2e095da0c5b197770cd711a8338354e9439f73aee32bb7a90eb980",
		Release:             "agentgateway-crds",
		Path:                "agentgateway/crds.yaml",
		StripCELValidations: true,
	},
	Controller: Chart{
		Ref:       "oci://cr.agentgateway.dev/charts/agentgateway",
		Version:   agentgatewayVersion,
		Digest:    "sha256:0dfc88a2a3d6ae970a2ed987ceef4fbe51483af134e0eaa41bea0b223328c9d4",
		Release:   "agentgateway",
		Namespace: agentgatewayNamespace,
		Set: []string{
			"controller.image.registry=" + agentgatewayRegistry,
			"controller.image.repository=controller",
			"controller.image.tag=" + agentgatewayVersion + "@" + agentgatewayControllerDigest,
		},
		Path: "agentgateway/controller.yaml",
	},
	Class:      "agentgateway/class.yaml",
	Images:     []string{agentgatewayControllerImage, agentgatewayProxyImage},
	LoadImages: true,
}

// AgentGateway is the gateway-agentgateway profile: agentgateway's own
// controller (D07, D26) on the shared Gateway API CRDs. It is a variable so
// tests in this module can install a broken variant through the public API.
var AgentGateway = recipe("gateway-agentgateway", "agentgateway.dev/agentgateway",
	[]Deployment{{Namespace: agentgatewayNamespace, Name: "agentgateway"}}, agentgatewayPins)
