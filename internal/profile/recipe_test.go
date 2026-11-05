package profile

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

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
	if len(recipes) != len(Pinned) {
		t.Fatalf("%d recipes but %d pinned for hack/render", len(recipes), len(Pinned))
	}
	for i, r := range recipes {
		if !reflect.DeepEqual(r.Pins, Pinned[i]) {
			t.Errorf("recipe %s: pins differ from Pinned[%d], so hack/render would not render it", r.Name, i)
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
// pins list nothing the layers do not use.
func TestImagesInTheLayersAreExactlyThePinnedImages(t *testing.T) {
	forEachRecipe(t, func(t *testing.T, r *Recipe) {
		var used []string
		for _, l := range layersOf(r) {
			for _, m := range imageLine.FindAllSubmatch(l.YAML, -1) {
				used = append(used, string(m[1]))
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
		if !setsClusterIPService(objs[i].Object, "") {
			t.Errorf("%s %s does not make the proxy Service ClusterIP", objs[i].GetKind(), objs[i].GetName())
		}
	})
}

// setsClusterIPService reports whether v holds a field named like "service"
// whose type is ClusterIP, which is how controllers' parameter objects shape
// the proxy Service (Envoy Gateway's envoyService, for example).
func setsClusterIPService(v any, key string) bool {
	switch v := v.(type) {
	case map[string]any:
		if strings.HasSuffix(strings.ToLower(key), "service") && v["type"] == "ClusterIP" {
			return true
		}
		for k, child := range v {
			if setsClusterIPService(child, k) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if setsClusterIPService(child, key) {
				return true
			}
		}
	}
	return false
}
