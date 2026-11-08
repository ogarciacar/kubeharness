// Package profile installs gateway profiles. A profile is a Recipe: plain
// data naming its pinned versions, its embedded manifest layers, and what to
// wait on. One Install runs every recipe, so a new profile is a new Recipe
// value and a re-render, not new install code.
package profile

import (
	"embed"
	"fmt"
	"sync"
)

// GatewayClass is the GatewayClass every recipe's class layer creates, so
// test manifests name the same class under every profile.
const GatewayClass = "kubeharness"

// Recipe is one gateway profile.
type Recipe struct {
	// Name is the profile name shown in the report line, such as "gateway-api".
	Name string
	Pins Pins
	// ControllerName is the controllerName of the class layer's GatewayClass.
	ControllerName string
	// Controllers are the Deployments that must be rolled out before the
	// GatewayClass can be accepted, and whose logs explain a failed install.
	Controllers []Deployment
	Layers      Layers
}

// Pins name every external input of a recipe, each at one exact version, and
// where its layers live under manifests/. hack/render reads them to produce
// the rendered layers; nothing else states a version.
type Pins struct {
	GatewayAPI     GatewayAPI
	ControllerCRDs Chart
	Controller     Chart
	// Class is the hand-written class layer: the GatewayClass and whatever
	// its parametersRef points at.
	Class string
	// Images is every image the layers reference, each by digest.
	Images []string
	// LoadImages asks InstallGateway to docker-pull and Kind-load Images
	// before applying layers. Set when Kind's containerd cannot resolve the
	// registry's multi-arch index digests (agentgateway's registry).
	LoadImages bool
}

// GatewayAPI pins a Gateway API release's install manifest.
type GatewayAPI struct {
	Version string
	Channel string
	// SHA256 is the hex digest of the release's <channel>-install.yaml.
	SHA256 string
}

// URL is where the release's install manifest is downloaded from.
func (g GatewayAPI) URL() string {
	return "https://github.com/kubernetes-sigs/gateway-api/releases/download/" + g.Version + "/" + g.Channel + "-install.yaml"
}

// Path is where the install manifest is stored under manifests/.
func (g GatewayAPI) Path() string { return "gateway-api/" + g.Version + "-" + g.Channel + ".yaml" }

// Chart pins one OCI Helm chart and the values it is rendered with.
type Chart struct {
	Ref     string
	Version string
	// Digest is the digest of the chart's OCI manifest, as helm pull prints
	// it. The manifest names the chart archive by digest, so this pins the
	// chart's content.
	Digest  string
	Release string
	// Namespace is the release namespace. When set, the rendered layer starts
	// with a Namespace object for it, as helm install --create-namespace does.
	Namespace string
	// Set holds values in helm --set syntax.
	Set []string
	// Path is where the rendered layer is stored under manifests/.
	Path string
	// StripCELValidations drops every x-kubernetes-validations rule from the
	// rendered objects. agentgateway's CRDs exceed the API server's CEL cost
	// budget on Kind (Kubernetes v1.37), so Install cannot apply them as
	// shipped. OpenAPI type checks remain.
	StripCELValidations bool
}

// Deployment names a controller Deployment.
type Deployment struct{ Namespace, Name string }

// Layers are the manifests Install applies, in this order. Gateway API CRDs
// are their own layer, never part of a controller chart (D24).
type Layers struct {
	GatewayAPICRDs Layer
	ControllerCRDs Layer
	Controller     Layer
	Class          Layer
}

// Layer is one embedded manifest file. Path is relative to manifests/.
type Layer struct {
	Path string
	YAML []byte
}

//go:embed manifests
var manifests embed.FS

// catalog is the single list of recipes. Pinned and Recipes both range it, so
// a new profile is one row here plus its pins file and class layer.
type catalogEntry struct {
	pins Pins
	new  func() *Recipe
}

var catalog = []catalogEntry{
	{pins: envoyGatewayPins, new: func() *Recipe { return EnvoyGateway() }},
	{pins: agentgatewayPins, new: func() *Recipe { return AgentGateway() }},
}

// Pinned lists the pins of every recipe in Recipes, in the same order, for
// hack/render. It reads no manifests, so render still runs when a pin change
// moves a layer to a path that does not exist yet.
func Pinned() []Pins {
	out := make([]Pins, len(catalog))
	for i, e := range catalog {
		out[i] = e.pins
	}
	return out
}

// Recipes lists every recipe.
func Recipes() []*Recipe {
	out := make([]*Recipe, len(catalog))
	for i, e := range catalog {
		out[i] = e.new()
	}
	return out
}

func recipe(name, controllerName string, controllers []Deployment, p Pins) func() *Recipe {
	return sync.OnceValue(func() *Recipe {
		return &Recipe{
			Name:           name,
			Pins:           p,
			ControllerName: controllerName,
			Controllers:    controllers,
			Layers: Layers{
				GatewayAPICRDs: embedded(p.GatewayAPI.Path()),
				ControllerCRDs: embedded(p.ControllerCRDs.Path),
				Controller:     embedded(p.Controller.Path),
				Class:          embedded(p.Class),
			},
		}
	})
}

func embedded(path string) Layer {
	b, err := manifests.ReadFile("manifests/" + path)
	if err != nil {
		panic(fmt.Sprintf("profile: embedded layer %s: %v; render it with: go -C hack/render run .", path, err))
	}
	return Layer{Path: path, YAML: b}
}
