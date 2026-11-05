package profile

const (
	envoyGatewayVersion   = "v1.9.3"
	envoyGatewayNamespace = "envoy-gateway-system"
	envoyGatewayImage     = "docker.io/envoyproxy/gateway:v1.9.3@sha256:192fc9bdadb6d2462e8cf7bd702ce3dd9648f3310c6038787448a1f509b7cd9f"
	// envoyProxyImage and rateLimitImage are Envoy Gateway v1.9.3's built-in
	// defaults, pinned through chart values so they show in the manifests.
	envoyProxyImage = "docker.io/envoyproxy/envoy:distroless-v1.39.5@sha256:9d84a586119a27b50437dbb01eccd4722832741539b1ed53c8e9d6da6067a6e0"
	rateLimitImage  = "docker.io/envoyproxy/ratelimit:5ae0682e@sha256:b95206e32b1a5f824fb06062fbb85bbdfb7f5de54135e92afe8462f22c979681"
)

var envoyGatewayPins = Pins{
	GatewayAPI: GatewayAPI{
		Version: "v1.6.1",
		Channel: "standard",
		SHA256:  "24d931f22abd8e40c973264319ead7cfa09d0fb7716b7ab1ee2ff174cb063a73",
	},
	ControllerCRDs: Chart{
		Ref:     "oci://docker.io/envoyproxy/gateway-crds-helm",
		Version: envoyGatewayVersion,
		Digest:  "sha256:a7e5267358e3d46e90b9e1a626f75a2ea206662c520de440cab2a7704e8cd6fd",
		Release: "eg-crds",
		Set:     []string{"crds.gatewayAPI.enabled=false", "crds.envoyGateway.enabled=true"},
		Path:    "envoy-gateway/crds.yaml",
	},
	Controller: Chart{
		Ref:       "oci://docker.io/envoyproxy/gateway-helm",
		Version:   envoyGatewayVersion,
		Digest:    "sha256:be1b8f163a338f94e7d6532336c08d6856fc70a0b790ac14a2826a54d23e0dd3",
		Release:   "eg",
		Namespace: envoyGatewayNamespace,
		Set: []string{
			"crds.enabled=false",
			"global.images.envoyGateway.image=" + envoyGatewayImage,
			"global.images.envoyProxy.image=" + envoyProxyImage,
			"global.images.ratelimit.image=" + rateLimitImage,
		},
		Path: "envoy-gateway/controller.yaml",
	},
	Class:  "envoy-gateway/class.yaml",
	Images: []string{envoyGatewayImage, envoyProxyImage, rateLimitImage},
}

// EnvoyGateway is the gateway-api profile: Envoy Gateway (D06) on the Gateway
// API standard channel. It is a variable so tests in this module can install
// a broken variant through the public API.
var EnvoyGateway = recipe("gateway-api", "gateway.envoyproxy.io/gatewayclass-controller",
	[]Deployment{{Namespace: envoyGatewayNamespace, Name: "envoy-gateway"}}, envoyGatewayPins)
