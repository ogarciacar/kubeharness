package gateway

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/ogarciacar/kubeharness/internal/kube"
)

func TestParseRouteKeepsNonCoreBackends(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata":   map[string]any{"name": "mcp", "namespace": "default", "generation": int64(1)},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"name": "edge"}},
			"rules": []any{
				map[string]any{"backendRefs": []any{
					map[string]any{"name": "calculator", "port": int64(80)},
					map[string]any{"group": "agentgateway.dev", "kind": "AgentgatewayBackend", "name": "calculator"},
					map[string]any{"group": "agentgateway.dev", "kind": "AgentgatewayBackend", "name": "calculator"},
				}},
				map[string]any{"backendRefs": []any{
					map[string]any{"group": "agentgateway.dev", "kind": "AgentgatewayBackend", "name": "other", "namespace": "shop"},
				}},
			},
		},
	}}
	got, err := ParseRoute(u)
	if err != nil {
		t.Fatal(err)
	}
	want := []BackendRef{
		{GroupKind: schema.GroupKind{Group: "agentgateway.dev", Kind: "AgentgatewayBackend"}, Ref: kube.Ref{Namespace: "default", Name: "calculator"}},
		{GroupKind: schema.GroupKind{Group: "agentgateway.dev", Kind: "AgentgatewayBackend"}, Ref: kube.Ref{Namespace: "shop", Name: "other"}},
	}
	if len(got.Backends) != len(want) {
		t.Fatalf("backends = %+v, want %+v", got.Backends, want)
	}
	for i := range want {
		if got.Backends[i] != want[i] {
			t.Fatalf("backends = %+v, want %+v", got.Backends, want)
		}
	}
}

func TestParseRouteRejectsAMalformedBackendRef(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata":   map[string]any{"name": "mcp", "namespace": "default"},
		"spec": map[string]any{
			"rules": []any{
				map[string]any{"backendRefs": []any{"calculator"}},
			},
		},
	}}
	_, err := ParseRoute(u)
	if err == nil || !strings.Contains(err.Error(), "spec.rules[0].backendRefs[0]") {
		t.Fatal(err)
	}
}

func TestDescribeBackend(t *testing.T) {
	br := BackendRef{
		GroupKind: schema.GroupKind{Group: "agentgateway.dev", Kind: "AgentgatewayBackend"},
		Ref:       kube.Ref{Namespace: "default", Name: "calculator"},
	}
	withStatus := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "calculator", "namespace": "default", "generation": int64(2)},
		"status": map[string]any{"conditions": []any{
			map[string]any{"type": "Accepted", "status": "True", "reason": "Accepted", "message": ""},
		}},
	}}
	if got, want := describeBackend(br, withStatus), "  backend agentgateway.dev/AgentgatewayBackend default/calculator generation 2: Accepted=True"; got != want {
		t.Fatalf("describeBackend =\n%s\nwant\n%s", got, want)
	}
	bare := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "calculator", "namespace": "default", "generation": int64(1)},
	}}
	if got, want := describeBackend(br, bare), "  backend agentgateway.dev/AgentgatewayBackend default/calculator generation 1: no status conditions"; got != want {
		t.Fatalf("describeBackend =\n%s\nwant\n%s", got, want)
	}
}

func TestEvidencePrintsBackendConditions(t *testing.T) {
	route := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata":   map[string]any{"name": "mcp", "namespace": "default", "generation": int64(1)},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"name": "edge"}},
			"rules": []any{map[string]any{"backendRefs": []any{
				map[string]any{"group": "agentgateway.dev", "kind": "AgentgatewayBackend", "name": "calculator"},
				map[string]any{"group": "example.dev", "kind": "Widget", "name": "w"},
			}}},
		},
		"status": map[string]any{"parents": []any{map[string]any{
			"controllerName": envoyController,
			"parentRef":      map[string]any{"name": "edge"},
			"conditions": []any{map[string]any{
				"type": "Accepted", "status": "True", "reason": "Accepted", "message": "", "observedGeneration": int64(1),
			}},
		}}},
	}}
	backend := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "agentgateway.dev/v1alpha1",
		"kind":       "AgentgatewayBackend",
		"metadata":   map[string]any{"name": "calculator", "namespace": "default", "generation": int64(2)},
		"status": map[string]any{"conditions": []any{
			map[string]any{"type": "Accepted", "status": "True", "reason": "Accepted", "message": ""},
		}},
	}}
	gvr := schema.GroupVersionResource{Group: "agentgateway.dev", Version: "v1alpha1", Resource: "agentgatewaybackends"}
	cl := clusterWith(route)
	cl.Kinds = map[schema.GroupKind]schema.GroupVersionResource{
		{Group: "agentgateway.dev", Kind: "AgentgatewayBackend"}: gvr,
	}
	dyn := cl.Dynamic.(*dynamicfake.FakeDynamicClient)
	if err := dyn.Tracker().Create(gvr, backend, backend.GetNamespace()); err != nil {
		t.Fatal(err)
	}
	got := Evidence(context.Background(), cl, edge, kube.Ref{})
	for _, want := range []string{
		"route default/mcp generation 1:",
		"  backend agentgateway.dev/AgentgatewayBackend default/calculator generation 2: Accepted=True",
		"  backend example.dev/Widget default/w: kind not installed by the profile",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("evidence lacks %q:\n%s", want, got)
		}
	}
}

func TestEvidenceNamesAMissingBackend(t *testing.T) {
	route := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata":   map[string]any{"name": "mcp", "namespace": "default", "generation": int64(1)},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"name": "edge"}},
			"rules": []any{map[string]any{"backendRefs": []any{
				map[string]any{"group": "agentgateway.dev", "kind": "AgentgatewayBackend", "name": "calculator"},
			}}},
		},
	}}
	gvr := schema.GroupVersionResource{Group: "agentgateway.dev", Version: "v1alpha1", Resource: "agentgatewaybackends"}
	cl := clusterWith(route)
	cl.Kinds = map[schema.GroupKind]schema.GroupVersionResource{
		{Group: "agentgateway.dev", Kind: "AgentgatewayBackend"}: gvr,
	}
	got := Evidence(context.Background(), cl, edge, kube.Ref{})
	want := "  backend agentgateway.dev/AgentgatewayBackend default/calculator: not found"
	if !strings.Contains(got, want) {
		t.Fatalf("evidence lacks %q:\n%s", want, got)
	}
}
