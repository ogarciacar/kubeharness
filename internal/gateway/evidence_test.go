package gateway

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/ogarciacar/kubeharness/internal/kube"
)

func TestDescribeGateway(t *testing.T) {
	got := describeGateway(mustGateway(t, load(t, "gateway-programmed.yaml")))
	want := strings.Join([]string{
		"gateway default/edge generation 1 class kubeharness: Accepted=True Programmed=True; addresses 10.96.12.7",
		"  listener http port 80 HTTP: Accepted=True Programmed=True ResolvedRefs=True attachedRoutes=2",
	}, "\n")
	if got != want {
		t.Fatalf("describeGateway =\n%s\nwant\n%s", got, want)
	}
}

func TestDescribeRouteShowsEveryParent(t *testing.T) {
	one := describeRoute(mustRoute(t, load(t, "route-one-parent.yaml")))
	if one != "route default/shop generation 1:\n  parent default/edge by "+envoyController+" observed 1: Accepted=True ResolvedRefs=True" {
		t.Fatalf("one parent =\n%s", one)
	}
	two := describeRoute(mustRoute(t, load(t, "route-two-parents.yaml")))
	if strings.Count(two, "\n  parent default/edge by "+envoyController) != 2 {
		t.Fatalf("two parents =\n%s", two)
	}
	failed := describeRoute(Route{
		Ref:        shop,
		Generation: 2,
		Parents: []RouteParent{{
			Gateway:    edge,
			Controller: envoyController,
			Conditions: Conditions{
				{Type: "Accepted", Status: "True", ObservedGeneration: 2},
				{Type: "ResolvedRefs", Status: "False", Reason: "BackendNotFound", Message: "backend not found", ObservedGeneration: 2},
			},
		}},
	})
	want := "route default/shop generation 2:\n  parent default/edge by " + envoyController + " observed 2: Accepted=True ResolvedRefs=False reason=BackendNotFound: backend not found"
	if failed != want {
		t.Fatalf("failed route =\n%s\nwant\n%s", failed, want)
	}
	if got := describeRoute(Route{Ref: shop, Generation: 1}); got != "route default/shop generation 1:\n  no status parents" {
		t.Fatalf("no parents =\n%s", got)
	}
}

func TestDescribeConditions(t *testing.T) {
	if got := describeConditions(nil); got != "" {
		t.Fatalf("empty = %q", got)
	}
	got := describeConditions(Conditions{
		{Type: "Accepted", Status: "True"},
		{Type: "Programmed", Status: "False", Reason: "AddressNotAssigned", Message: "no address"},
	})
	if got != "Accepted=True Programmed=False reason=AddressNotAssigned: no address" {
		t.Fatalf("conditions = %q", got)
	}
}

func TestEvidenceNamesTheUnresolvedAddressAndSkipsOtherRoutes(t *testing.T) {
	gw := load(t, "gateway-programmed.yaml")
	shopRoute := load(t, "route-one-parent.yaml")
	other := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata":   map[string]any{"name": "payments", "namespace": "default", "generation": int64(1)},
		"spec":       map[string]any{"parentRefs": []any{map[string]any{"name": "other"}}},
	}}
	cl := clusterWith(gw, shopRoute, other)
	got := Evidence(context.Background(), cl, edge, kube.Ref{})
	for _, want := range []string{
		"gateway default/edge generation 1 class kubeharness: Accepted=True Programmed=True; addresses 10.96.12.7",
		"listener http port 80 HTTP: Accepted=True Programmed=True ResolvedRefs=True attachedRoutes=2",
		"route default/shop generation 1:",
		"parent default/edge by " + envoyController,
		"proxy Service not resolved: address 10.96.12.7",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("evidence lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "route default/payments") {
		t.Fatalf("evidence includes a route that is not attached:\n%s", got)
	}
	if strings.Contains(got, "\n  backend ") {
		t.Fatalf("evidence lists a core backendRef:\n%s", got)
	}
}

func TestEvidenceIncludesProxyPodsAndControllerPods(t *testing.T) {
	gw := load(t, "gateway-programmed.yaml")
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "envoy-gateway-system", Name: "envoy-edge"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "envoy"}},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "envoy-gateway-system", Name: "envoy-edge-1", Labels: map[string]string{"app": "envoy"}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "envoy-gateway-system", Name: "envoy-gateway", Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "eg"}},
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1},
	}
	cl := clusterWith(gw)
	cl.Kube = fake.NewClientset(svc, pod, dep)
	cl.ControllerPods = []kube.Ref{{Namespace: "envoy-gateway-system", Name: "envoy-gateway"}}
	ref := kube.Ref{Namespace: "envoy-gateway-system", Name: "envoy-edge"}
	got := Evidence(context.Background(), cl, edge, ref)
	for _, want := range []string{
		"proxy envoy-gateway-system/envoy-edge (service):",
		"pod envoy-edge-1 phase=Running",
		"controller envoy-gateway-system/envoy-gateway:",
		"deployment envoy-gateway-system/envoy-gateway:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("evidence lacks %q:\n%s", want, got)
		}
	}
}

func TestEvidenceNamesWhatItCannotRead(t *testing.T) {
	got := Evidence(context.Background(), Cluster{}, edge, kube.Ref{})
	for _, want := range []string{
		"gateway default/edge: no dynamic client",
		"routes: no dynamic client",
		"proxy Service not resolved",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("evidence lacks %q:\n%s", want, got)
		}
	}
}
