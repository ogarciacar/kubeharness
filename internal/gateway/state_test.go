package gateway

import (
	"embed"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/ogarciacar/kubeharness/internal/kube"
)

// Status fixtures in testdata are synthetic. They follow the Gateway API
// status schema and the fields Envoy Gateway v1.9.3 writes. That writer
// creates one RouteParentStatus per spec parentRef, and its Accepted,
// Programmed, and route messages are copied here. No Kind dump of G01 was
// read. route-two-parents.yaml covers StatusFor's duplicate-parent Stop.
// A live Kind dump of G01's HTTPRoute has to show one matching parent for
// edge before e2e waits on StatusFor.

//go:embed testdata/*.yaml
var testdata embed.FS

const envoyController = "gateway.envoyproxy.io/gatewayclass-controller"

var (
	edge = kube.Ref{Namespace: "default", Name: "edge"}
	shop = kube.Ref{Namespace: "default", Name: "shop"}
)

func TestParseGatewayProgrammed(t *testing.T) {
	got := mustGateway(t, load(t, "gateway-programmed.yaml"))
	if got.Ref != edge || got.Generation != 1 || got.Class != "kubeharness" {
		t.Fatalf("identity = ref %s generation %d class %q", got.Ref, got.Generation, got.Class)
	}
	if len(got.Addresses) != 1 || got.Addresses[0] != (Address{Type: "IPAddress", Value: "10.96.12.7"}) {
		t.Fatalf("addresses = %+v", got.Addresses)
	}
	accepted, ok := got.Conditions.Get("Accepted")
	if !ok || accepted.Status != "True" || accepted.ObservedGeneration != 1 || accepted.Message != "The Gateway has been scheduled by Envoy Gateway" {
		t.Fatalf("Accepted = %+v present %v", accepted, ok)
	}
	programmed, ok := got.Conditions.Get("Programmed")
	if !ok || programmed.Status != "True" || programmed.Reason != "Programmed" || programmed.Message != "Address assigned to the Gateway, 1/1 envoy replicas available" {
		t.Fatalf("Programmed = %+v present %v", programmed, ok)
	}
	if !got.Conditions.ObservedAt(1) || got.Conditions.ObservedAt(2) {
		t.Fatal("conditions were not observed at generation 1")
	}
	if len(got.Listeners) != 1 {
		t.Fatalf("listeners = %+v", got.Listeners)
	}
	l := got.Listeners[0]
	if l.Name != "http" || l.Port != 80 || l.Protocol != "HTTP" || l.AttachedRoutes != 2 {
		t.Fatalf("listener = %+v", l)
	}
	if resolved, ok := l.Conditions.Get("ResolvedRefs"); !ok || resolved.Status != "True" {
		t.Fatalf("listener ResolvedRefs = %+v present %v", resolved, ok)
	}
	if ports := got.ListenerPorts(); !slices.Equal(ports, []int32{80}) {
		t.Fatalf("ports = %v", ports)
	}
	requireDone(t, got.Readiness("kubeharness"))
	requireStop(t, got.Readiness("other"), `gateway default/edge class "kubeharness", want "other"`)
}

func TestParseGatewayReadsEachNumericWireForm(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "edge", "namespace": "default", "generation": int64(2)},
		"spec": map[string]any{
			"gatewayClassName": "kubeharness",
			"listeners":        []any{map[string]any{"name": "http", "protocol": "HTTP", "port": float64(80)}},
		},
		"status": map[string]any{
			"addresses": []any{map[string]any{"type": "IPAddress", "value": "10.96.12.7"}},
			"conditions": []any{
				map[string]any{"type": "Accepted", "status": "True", "reason": "Accepted", "observedGeneration": json.Number("2")},
				map[string]any{"type": "Programmed", "status": "True", "reason": "Programmed", "observedGeneration": int64(2)},
			},
		},
	}}
	got := mustGateway(t, u)
	if got.Generation != 2 || !slices.Equal(got.ListenerPorts(), []int32{80}) {
		t.Fatalf("generation %d ports %v", got.Generation, got.ListenerPorts())
	}
	requireDone(t, got.Readiness("kubeharness"))
}

func TestGatewayReadinessNamesWhatIsMissing(t *testing.T) {
	notProgrammed := mustGateway(t, load(t, "gateway-programmed-false.yaml"))
	requirePending(t, notProgrammed.Readiness("kubeharness"), "Programmed=False reason=AddressNotAssigned: No addresses have been assigned to the Gateway")

	stale := mustGateway(t, load(t, "gateway-stale.yaml"))
	requirePending(t, stale.Readiness("kubeharness"), "Programmed observed generation 1, generation 2")

	noStatus := mustGateway(t, load(t, "gateway-no-status.yaml"))
	requirePending(t, noStatus.Readiness("kubeharness"), "Accepted absent")
	requireStop(t, noStatus.Readiness("other"), `gateway default/edge class "kubeharness", want "other"`)

	defaulted := mustGateway(t, load(t, "gateway-address-default.yaml"))
	if len(defaulted.Addresses) != 1 || defaulted.Addresses[0] != (Address{Type: "IPAddress", Value: "10.96.12.7"}) {
		t.Fatalf("defaulted address = %+v", defaulted.Addresses)
	}

	ports := mustGateway(t, load(t, "gateway-ports.yaml"))
	if got := ports.ListenerPorts(); !slices.Equal(got, []int32{80, 443}) {
		t.Fatalf("ports = %v", got)
	}
}

func TestParseGatewayRejectsAListenerWithoutAPort(t *testing.T) {
	_, err := ParseGateway(load(t, "gateway-no-port.yaml"))
	if err == nil || !strings.Contains(err.Error(), `listener "http"`) || !strings.Contains(err.Error(), "port is required") {
		t.Fatalf("ParseGateway = %v", err)
	}
	if _, err := ParseGateway(nil); err == nil {
		t.Fatal("nil gateway parsed")
	}
}

func TestStatusForOneParent(t *testing.T) {
	got := mustRoute(t, load(t, "route-one-parent.yaml"))
	if got.Ref != shop || got.Generation != 1 || !slices.Equal(got.ParentRefs, []kube.Ref{edge}) {
		t.Fatalf("route = %+v", got)
	}
	if !got.AttachedTo(edge) || got.AttachedTo(kube.Ref{Namespace: "default", Name: "other"}) {
		t.Fatalf("attached = %v", got.ParentRefs)
	}
	conds, v := got.StatusFor(edge, envoyController)
	requireDone(t, v)
	want := Conditions{
		{Type: "Accepted", Status: "True", Reason: "Accepted", Message: "Route is accepted", ObservedGeneration: 1},
		{Type: "ResolvedRefs", Status: "True", Reason: "ResolvedRefs", Message: "Resolved all the Object references for the Route", ObservedGeneration: 1},
	}
	if !slices.Equal(conds, want) {
		t.Fatalf("conditions = %+v", conds)
	}
}

func TestStatusForStaleGeneration(t *testing.T) {
	got := mustRoute(t, load(t, "route-stale.yaml"))
	conds, v := got.StatusFor(edge, envoyController)
	requirePending(t, v, "status observed generation 1, route generation 2")
	if conds != nil {
		t.Fatalf("stale conditions returned: %+v", conds)
	}
}

func TestStatusForMissingParent(t *testing.T) {
	got := mustRoute(t, load(t, "route-missing-parent.yaml"))
	if len(got.Parents) != 1 || got.Parents[0].Gateway != (kube.Ref{Namespace: "default", Name: "payments"}) {
		t.Fatalf("parents = %+v", got.Parents)
	}
	conds, v := got.StatusFor(edge, envoyController)
	requirePending(t, v, "no status from "+envoyController+" for gateway default/edge yet")
	if conds != nil {
		t.Fatalf("conditions = %+v", conds)
	}
}

func TestStatusForIgnoresAForeignController(t *testing.T) {
	got := mustRoute(t, load(t, "route-foreign-controller.yaml"))
	if len(got.Parents) != 2 || got.Parents[0].Controller != "other.example/controller" {
		t.Fatalf("parents = %+v", got.Parents)
	}
	conds, v := got.StatusFor(edge, envoyController)
	requireDone(t, v)
	if len(conds) != 2 || conds[0].Message != "Route is accepted" || conds[1].Message != "Resolved all the Object references for the Route" {
		t.Fatalf("conditions = %+v", conds)
	}
}

func TestStatusForDefaultsParentNamespace(t *testing.T) {
	got := mustRoute(t, load(t, "route-cross-namespace.yaml"))
	appsEdge := kube.Ref{Namespace: "apps", Name: "edge"}
	external := kube.Ref{Namespace: "edge-ns", Name: "external"}
	if got.Ref != (kube.Ref{Namespace: "apps", Name: "shop"}) || got.Generation != 3 {
		t.Fatalf("identity = %+v generation %d", got.Ref, got.Generation)
	}
	if !slices.Equal(got.ParentRefs, []kube.Ref{appsEdge, external}) {
		t.Fatalf("parentRefs = %+v", got.ParentRefs)
	}
	if len(got.Parents) != 1 || got.Parents[0].Gateway != appsEdge {
		t.Fatalf("parents = %+v", got.Parents)
	}
	if !got.AttachedTo(appsEdge) || !got.AttachedTo(external) || got.AttachedTo(edge) {
		t.Fatalf("attachment = %v", got.ParentRefs)
	}
	conds, v := got.StatusFor(appsEdge, envoyController)
	requireDone(t, v)
	if len(conds) != 2 || conds[0].ObservedGeneration != 3 {
		t.Fatalf("conditions = %+v", conds)
	}
	_, pending := got.StatusFor(external, envoyController)
	requirePending(t, pending, "no status from "+envoyController+" for gateway edge-ns/external yet")
}

func TestStatusForNoParentRefStops(t *testing.T) {
	got := mustRoute(t, load(t, "route-no-parentref.yaml"))
	if got.AttachedTo(edge) || !got.AttachedTo(kube.Ref{Namespace: "default", Name: "other"}) {
		t.Fatalf("parentRefs = %+v", got.ParentRefs)
	}
	conds, v := got.StatusFor(edge, envoyController)
	requireStop(t, v, "route default/shop has no parentRef to gateway default/edge")
	if conds != nil {
		t.Fatalf("conditions = %+v", conds)
	}
}

func TestStatusForTwoParentsStops(t *testing.T) {
	// Synthetic. Envoy Gateway v1.9.3 writes one status parent per spec
	// parentRef, and G01 has one parentRef with no sectionName. This fixture
	// adds a second status parent for the same Gateway and controller, with
	// sectionName set, to lock the Stop. A live Kind dump still has to
	// confirm a healthy route has one matching parent before e2e.
	got := mustRoute(t, load(t, "route-two-parents.yaml"))
	if !got.AttachedTo(edge) || len(got.Parents) != 2 {
		t.Fatalf("route parents = spec %v status %+v", got.ParentRefs, got.Parents)
	}
	conds, v := got.StatusFor(edge, envoyController)
	requireStop(t, v, "route default/shop has 2 status parents for gateway default/edge from "+envoyController)
	if conds != nil {
		t.Fatalf("merged conditions = %+v", conds)
	}
}

func TestStatusForEmptyConditionsArePending(t *testing.T) {
	got := mustRoute(t, load(t, "route-empty-conditions.yaml"))
	if got.Parents[0].Conditions.ObservedAt(4) {
		t.Fatal("empty conditions counted as observed")
	}
	_, v := got.StatusFor(edge, envoyController)
	requirePending(t, v, "status observed generation 0, route generation 4")
}

func load(t *testing.T, name string) *unstructured.Unstructured {
	t.Helper()
	b, err := testdata.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	objs, err := kube.DecodeManifests(name, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 {
		t.Fatalf("%s: %d documents", name, len(objs))
	}
	return objs[0]
}

func mustGateway(t *testing.T, u *unstructured.Unstructured) GatewayState {
	t.Helper()
	got, err := ParseGateway(u)
	if err != nil {
		t.Fatalf("ParseGateway: %v", err)
	}
	return got
}

func mustRoute(t *testing.T, u *unstructured.Unstructured) Route {
	t.Helper()
	got, err := ParseRoute(u)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	return got
}

func requireDone(t *testing.T, v Verdict) {
	t.Helper()
	if !v.done || v.stop != nil || v.why != "" {
		t.Fatalf("verdict done=%v stop=%v why=%q, want done", v.done, v.stop, v.why)
	}
}

func requirePending(t *testing.T, v Verdict, why string) {
	t.Helper()
	if v.done || v.stop != nil || v.why != why {
		t.Fatalf("verdict done=%v stop=%v why=%q, want pending %q", v.done, v.stop, v.why, why)
	}
}

func requireStop(t *testing.T, v Verdict, text string) {
	t.Helper()
	if v.done || v.why != "" || v.stop == nil || v.stop.Error() != text {
		got := "<nil>"
		if v.stop != nil {
			got = v.stop.Error()
		}
		t.Fatalf("verdict done=%v why=%q stop=%s, want stop %q", v.done, v.why, got, text)
	}
}
