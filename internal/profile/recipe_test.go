package profile

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/ogarciacar/kubeharness/internal/kube"
)

const gatewayAPIGroup = "gateway.networking.k8s.io"

func layersOf(r *Recipe) []Layer {
	l := r.Layers
	return []Layer{l.GatewayAPICRDs, l.ControllerCRDs, l.Controller, l.Class}
}

func decode(t *testing.T, l Layer) []*unstructured.Unstructured {
	t.Helper()
	objs, err := kube.DecodeManifests(l.Path, l.YAML)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) == 0 {
		t.Fatalf("layer %s is empty", l.Path)
	}
	return objs
}

func isCRD(o *unstructured.Unstructured) bool {
	return o.GetKind() == "CustomResourceDefinition" && o.GroupVersionKind().Group == "apiextensions.k8s.io"
}

func forEachRecipe(t *testing.T, check func(t *testing.T, r *Recipe)) {
	for _, r := range Recipes() {
		t.Run(r.Name, func(t *testing.T) { check(t, r) })
	}
}

func TestEveryRecipeIsRenderedFromItsPins(t *testing.T) {
	recipes := Recipes()
	pinned := Pinned()
	if len(recipes) != len(pinned) {
		t.Fatalf("%d recipes but %d pinned for hack/render", len(recipes), len(pinned))
	}
	for i, r := range recipes {
		if !reflect.DeepEqual(r.Pins, pinned[i]) {
			t.Errorf("recipe %s: pins differ from Pinned()[%d], so hack/render would not render it", r.Name, i)
		}
	}
}

// D24: a controller layer that ships Gateway API CRDs would make the
// cluster's Gateway API version depend on the controller chart, not the pin.
func TestGatewayAPICRDsComeOnlyFromTheirOwnLayer(t *testing.T) {
	forEachRecipe(t, func(t *testing.T, r *Recipe) {
		for _, l := range layersOf(r)[1:] {
			for _, o := range decode(t, l) {
				group, _, _ := unstructured.NestedString(o.Object, "spec", "group")
				if isCRD(o) && group == gatewayAPIGroup {
					t.Errorf("layer %s ships Gateway API CRD %s", l.Path, o.GetName())
				}
			}
		}
	})
}

func TestOnlyCRDLayersHoldCRDs(t *testing.T) {
	forEachRecipe(t, func(t *testing.T, r *Recipe) {
		for _, l := range layersOf(r)[2:] {
			for _, o := range decode(t, l) {
				if isCRD(o) {
					t.Errorf("layer %s holds CRD %s, which Install would not wait to be established", l.Path, o.GetName())
				}
			}
		}
	})
}

func TestGatewayAPICRDsMatchThePinnedBundle(t *testing.T) {
	forEachRecipe(t, func(t *testing.T, r *Recipe) {
		crds := 0
		for _, o := range decode(t, layersOf(r)[0]) {
			if !isCRD(o) {
				continue
			}
			crds++
			a := o.GetAnnotations()
			if got := a[gatewayAPIGroup+"/bundle-version"]; got != r.Pins.GatewayAPI.Version {
				t.Errorf("CRD %s bundle-version %q, pinned %q", o.GetName(), got, r.Pins.GatewayAPI.Version)
			}
			if got := a[gatewayAPIGroup+"/channel"]; got != r.Pins.GatewayAPI.Channel {
				t.Errorf("CRD %s channel %q, pinned %q", o.GetName(), got, r.Pins.GatewayAPI.Channel)
			}
		}
		if crds == 0 {
			t.Error("gateway-api layer holds no CRDs")
		}
	})
}

// imageLine matches image fields anywhere in a layer, including inside the
// YAML a ConfigMap carries as a string, such as Envoy Gateway's config.
var imageLine = regexp.MustCompile(`(?m)^[ \t]*(?:-[ \t]+)?image:[ \t]*["']?([^\s"'#]+)`)

// D14: every image a layer can make kubelet pull is pinned by digest, and the
// pins list nothing the layers do not use. An image shows up in a layer as an
// image: line, or as an image object {registry, repository, tag, digest} on a
// non-CRD (AgentgatewayParameters pins the proxy that way).
func TestImagesInTheLayersAreExactlyThePinnedImages(t *testing.T) {
	forEachRecipe(t, func(t *testing.T, r *Recipe) {
		var used []string
		for _, l := range layersOf(r) {
			for _, m := range imageLine.FindAllSubmatch(l.YAML, -1) {
				used = append(used, string(m[1]))
			}
			for _, o := range decode(t, l) {
				if isCRD(o) {
					continue
				}
				used = append(used, imageObjects(o.Object)...)
			}
		}
		used = slices.Compact(slices.Sorted(slices.Values(used)))
		pinned := slices.Sorted(slices.Values(r.Pins.Images))
		if !slices.Equal(used, pinned) {
			t.Errorf("images in the layers:\n  %s\npinned:\n  %s", strings.Join(used, "\n  "), strings.Join(pinned, "\n  "))
		}
		for _, img := range pinned {
			if !strings.Contains(img, "@sha256:") {
				t.Errorf("image %s is not pinned by digest", img)
			}
		}
	})
}

// imageObjects returns every image named in v by a map under a key "image"
// that has string fields repository and digest, composed as
// registry/repository:tag@digest. registry and tag may be absent.
func imageObjects(v any) []string {
	var out []string
	switch v := v.(type) {
	case map[string]any:
		if img, ok := v["image"].(map[string]any); ok {
			repo, _ := img["repository"].(string)
			digest, _ := img["digest"].(string)
			if repo != "" && digest != "" {
				ref := repo
				if reg, _ := img["registry"].(string); reg != "" {
					ref = reg + "/" + repo
				}
				if tag, _ := img["tag"].(string); tag != "" {
					ref += ":" + tag
				}
				out = append(out, ref+"@"+digest)
			}
		}
		for k, child := range v {
			if k == "image" {
				continue
			}
			out = append(out, imageObjects(child)...)
		}
	case []any:
		for _, child := range v {
			out = append(out, imageObjects(child)...)
		}
	}
	return out
}

func TestControllerDeploymentsAreInTheControllerLayer(t *testing.T) {
	forEachRecipe(t, func(t *testing.T, r *Recipe) {
		if len(r.Controllers) == 0 {
			t.Fatal("recipe names no controller Deployment to wait on")
		}
		objs := decode(t, layersOf(r)[2])
		for _, d := range r.Controllers {
			if !slices.ContainsFunc(objs, func(o *unstructured.Unstructured) bool {
				return o.GetKind() == "Deployment" && o.GetNamespace() == d.Namespace && o.GetName() == d.Name
			}) {
				t.Errorf("controller Deployment %s/%s is not in the controller layer", d.Namespace, d.Name)
			}
		}
	})
}

// D25: test manifests name GatewayClass "kubeharness" under every profile, and
// Kind has no LoadBalancer, so the proxy Service must be ClusterIP for the
// controller to publish a Gateway address.
func TestClassLayerHasTheKubeharnessGatewayClassWithAClusterIPProxy(t *testing.T) {
	forEachRecipe(t, func(t *testing.T, r *Recipe) {
		objs := decode(t, layersOf(r)[3])
		var classes []*unstructured.Unstructured
		for _, o := range objs {
			if o.GetKind() == "GatewayClass" && o.GroupVersionKind().Group == gatewayAPIGroup {
				classes = append(classes, o)
			}
		}
		if len(classes) != 1 || classes[0].GetName() != GatewayClass {
			t.Fatalf("class layer has %d GatewayClasses, want exactly one named %q", len(classes), GatewayClass)
		}
		class := classes[0]
		if got, _, _ := unstructured.NestedString(class.Object, "spec", "controllerName"); got != r.ControllerName {
			t.Errorf("GatewayClass controllerName %q, recipe says %q", got, r.ControllerName)
		}
		ref, _, _ := unstructured.NestedStringMap(class.Object, "spec", "parametersRef")
		i := slices.IndexFunc(objs, func(o *unstructured.Unstructured) bool {
			return o.GroupVersionKind().Group == ref["group"] && o.GetKind() == ref["kind"] && o.GetName() == ref["name"] && o.GetNamespace() == ref["namespace"]
		})
		if i < 0 {
			t.Fatalf("GatewayClass parametersRef %v is not in the class layer", ref)
		}
		if !setsClusterIPService(objs[i].Object, false) {
			t.Errorf("%s %s does not make the proxy Service ClusterIP", objs[i].GetKind(), objs[i].GetName())
		}
	})
}

// setsClusterIPService reports whether v holds type ClusterIP under a path
// that passes through a key ending in "service" (Envoy's envoyService.type,
// agentgateway's service.spec.type overlay).
func setsClusterIPService(v any, underService bool) bool {
	switch v := v.(type) {
	case map[string]any:
		if underService && v["type"] == "ClusterIP" {
			return true
		}
		for k, child := range v {
			if setsClusterIPService(child, underService || strings.HasSuffix(strings.ToLower(k), "service")) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if setsClusterIPService(child, underService) {
				return true
			}
		}
	}
	return false
}

func TestEveryRecipeInstallsTheSameGatewayAPI(t *testing.T) {
	for _, r := range Recipes() {
		if r.Pins.GatewayAPI != gatewayAPI {
			t.Errorf("recipe %s pins Gateway API %+v, the harness pins %+v", r.Name, r.Pins.GatewayAPI, gatewayAPI)
		}
	}
}

func TestRecipeKindsComeFromTheEmbeddedCRDs(t *testing.T) {
	ag := AgentGateway()
	backend := schema.GroupKind{Group: "agentgateway.dev", Kind: "AgentgatewayBackend"}
	if got, want := ag.Kinds[backend], (schema.GroupVersionResource{Group: "agentgateway.dev", Version: "v1alpha1", Resource: "agentgatewaybackends"}); got != want {
		t.Fatalf("AgentgatewayBackend = %+v, want %+v", got, want)
	}
	route := schema.GroupKind{Group: gatewayAPIGroup, Kind: "HTTPRoute"}
	if got, want := ag.Kinds[route], (schema.GroupVersionResource{Group: gatewayAPIGroup, Version: "v1", Resource: "httproutes"}); got != want {
		t.Fatalf("HTTPRoute = %+v, want %+v", got, want)
	}
	for gk := range EnvoyGateway().Kinds {
		if gk.Group == "agentgateway.dev" {
			t.Fatalf("envoy recipe lists %s", gk)
		}
	}
	if _, ok := EnvoyGateway().Kinds[route]; !ok {
		t.Fatal("envoy recipe has no HTTPRoute")
	}
}

func TestCRDKindsRequiresAStorageVersion(t *testing.T) {
	_, err := crdKinds(Layer{Path: "bad.yaml", YAML: []byte(`
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.dev
spec:
  group: example.dev
  names:
    kind: Widget
    plural: widgets
  versions:
    - name: v1
      served: true
`)})
	if err == nil || !strings.Contains(err.Error(), "widgets.example.dev") || !strings.Contains(err.Error(), "no storage version") {
		t.Fatal(err)
	}
}

func TestRecipeWalkersRejectNearMisses(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
		v    any
	}{
		{"type without service ancestor", false, map[string]any{"spec": map[string]any{"type": "ClusterIP"}}},
		{"service LoadBalancer", false, map[string]any{"service": map[string]any{"spec": map[string]any{"type": "LoadBalancer"}}}},
		{"envoyService ClusterIP", true, map[string]any{"envoyService": map[string]any{"type": "ClusterIP"}}},
		{"service.spec ClusterIP", true, map[string]any{"service": map[string]any{"spec": map[string]any{"type": "ClusterIP"}}}},
	}
	for _, tc := range cases {
		if got := setsClusterIPService(tc.v, false); got != tc.ok {
			t.Errorf("setsClusterIPService(%s)=%v, want %v", tc.name, got, tc.ok)
		}
	}
	if got := imageObjects(map[string]any{"image": map[string]any{"repository": "agentgateway", "tag": "v1"}}); got != nil {
		t.Errorf("imageObjects without digest = %v, want nil", got)
	}
	if got := imageObjects(map[string]any{"image": map[string]any{"type": "object", "properties": map[string]any{}}}); got != nil {
		t.Errorf("imageObjects schema shape = %v, want nil", got)
	}
	want := []string{"r/x:v1@sha256:a"}
	if got := imageObjects(map[string]any{"image": map[string]any{"registry": "r", "repository": "x", "tag": "v1", "digest": "sha256:a"}}); !slices.Equal(got, want) {
		t.Errorf("imageObjects = %v, want %v", got, want)
	}
}
