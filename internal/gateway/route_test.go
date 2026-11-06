package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/ogarciacar/kubeharness/internal/kube"
)

// Fixtures are the synthetic testdata StatusFor already parses. A live Kind
// dump of G01 still has to show one matching parent before e2e waits on this.

var routeTarget = Target{
	Gateway: edge,
	Service: kube.Ref{Namespace: "envoy-gateway-system", Name: "envoy-edge"},
	Ports:   []int32{80},
}

func TestAwaitRouteStopsWhenTheRouteIsMissing(t *testing.T) {
	err := callAwaitRoute(t, routeCluster(t), time.Second, func(Conditions) error {
		t.Error("check ran")
		return nil
	})
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("missing route = %v", err)
	}
	for _, want := range []string{
		"route default/shop not found; apply it first",
		"proxy envoy-gateway-system/envoy-edge",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestAwaitRouteGivesCheckTheSettledConditions(t *testing.T) {
	var got Conditions
	err := callAwaitRoute(t, routeCluster(t, "route-one-parent.yaml"), time.Second, func(cs Conditions) error {
		got = cs
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted, ok := got.Get("Accepted")
	resolved, rok := got.Get("ResolvedRefs")
	if !ok || !rok || accepted.Status != "True" || resolved.Status != "True" || accepted.Message != "Route is accepted" {
		t.Fatalf("conditions = %+v", got)
	}
}

func TestAwaitRouteKeepsACheckErrorPendingWithTheConditions(t *testing.T) {
	u := load(t, "route-one-parent.yaml").DeepCopy()
	markResolvedRefs(t, u, "False", "BackendNotFound", "backend not found")
	cl := clusterWith(u)
	cl.Controller = envoyController
	err := callAwaitRoute(t, cl, 300*time.Millisecond, func(Conditions) error {
		return errors.New("backend missing")
	})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("check error = %v", err)
	}
	const want = "check: backend missing; conditions Accepted=True ResolvedRefs=False reason=BackendNotFound: backend not found"
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "proxy envoy-gateway-system/envoy-edge") {
		t.Fatalf("error = %v", err)
	}
}

func TestAwaitRouteStopsWhenStatusForStops(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		want    string
	}{
		{name: "no parentRef", fixture: "route-no-parentref.yaml", want: "route default/shop has no parentRef to gateway default/edge"},
		{name: "two parents", fixture: "route-two-parents.yaml", want: "route default/shop has 2 status parents for gateway default/edge from " + envoyController},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := callAwaitRoute(t, routeCluster(t, c.fixture), time.Second, func(Conditions) error {
				t.Error("check ran")
				return nil
			})
			if err == nil || errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestAwaitRouteWaitsThroughStaleStatus(t *testing.T) {
	err := callAwaitRoute(t, routeCluster(t, "route-stale.yaml"), 300*time.Millisecond, func(Conditions) error {
		t.Error("check ran on a stale generation")
		return nil
	})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "status observed generation 1, route generation 2") {
		t.Fatalf("stale = %v", err)
	}
}

func TestAwaitRouteUsesTheClusterController(t *testing.T) {
	cl := routeCluster(t, "route-one-parent.yaml")
	cl.Controller = "other.example/controller"
	err := callAwaitRoute(t, cl, 300*time.Millisecond, func(Conditions) error {
		t.Error("check ran for a controller that did not write status")
		return nil
	})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "no status from other.example/controller for gateway default/edge yet") {
		t.Fatalf("controller = %v", err)
	}
}

func callAwaitRoute(t *testing.T, cl Cluster, limit time.Duration, check func(Conditions) error) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	return AwaitRoute(ctx, context.Background(), cl, routeTarget, shop, check)
}

func routeCluster(t *testing.T, fixtures ...string) Cluster {
	t.Helper()
	objs := make([]runtime.Object, 0, len(fixtures))
	for _, name := range fixtures {
		objs = append(objs, load(t, name))
	}
	cl := clusterWith(objs...)
	cl.Controller = envoyController
	return cl
}

func markResolvedRefs(t *testing.T, u *unstructured.Unstructured, status, reason, message string) {
	t.Helper()
	val, found, err := unstructured.NestedFieldNoCopy(u.Object, "status", "parents")
	if err != nil || !found {
		t.Fatalf("status.parents: found=%v err=%v", found, err)
	}
	parents, ok := val.([]any)
	if !ok || len(parents) == 0 {
		t.Fatalf("parents = %T len %d", val, len(parents))
	}
	parent, ok := parents[0].(map[string]any)
	if !ok {
		t.Fatalf("parent is %T", parents[0])
	}
	raw, ok := parent["conditions"].([]any)
	if !ok {
		t.Fatalf("conditions is %T", parent["conditions"])
	}
	for _, item := range raw {
		cond, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("condition is %T", item)
		}
		if cond["type"] != "ResolvedRefs" {
			continue
		}
		cond["status"] = status
		cond["reason"] = reason
		cond["message"] = message
		return
	}
	t.Fatal("no ResolvedRefs condition")
}
