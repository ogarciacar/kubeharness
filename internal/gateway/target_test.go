package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/ogarciacar/kubeharness/internal/kube"
)

func TestPortPlan(t *testing.T) {
	cases := []struct {
		name      string
		listeners []int32
		service   []int32
		want      []int32
		err       string
	}{
		{name: "listener ports in ascending order", listeners: []int32{443, 80}, service: []int32{80, 443, 8080}, want: []int32{80, 443}},
		{name: "duplicate listeners collapse", listeners: []int32{80, 80}, service: []int32{80}, want: []int32{80}},
		{name: "no listeners", err: "gateway has no listener ports"},
		{name: "service lacks a listener", listeners: []int32{80, 443}, service: []int32{8080, 80}, err: "proxy Service has ports [80 8080], lacks listener ports [443]"},
		{name: "service has no ports", listeners: []int32{80}, err: "proxy Service has ports [], lacks listener ports [80]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := portPlan(c.listeners, c.service)
			if c.err != "" {
				if err == nil || err.Error() != c.err || got != nil {
					t.Fatalf("portPlan = %v %v, want error %q", got, err, c.err)
				}
				return
			}
			if err != nil || !samePorts(got, c.want) {
				t.Fatalf("portPlan = %v %v, want %v", got, err, c.want)
			}
		})
	}
}

func TestObserveTarget(t *testing.T) {
	programmed := load(t, "gateway-programmed.yaml")
	proxy := proxyService("envoy-gateway-system", "envoy-edge", "10.96.12.7", 80)

	t.Run("not found", func(t *testing.T) {
		_, v := observeTarget(context.Background(), clusterWith(nil), edge)
		requireStop(t, v, "gateway default/edge not found; apply it before Gateway(ref)")
	})
	t.Run("wrong class", func(t *testing.T) {
		gw := programmed.DeepCopy()
		if err := unstructured.SetNestedField(gw.Object, "other", "spec", "gatewayClassName"); err != nil {
			t.Fatal(err)
		}
		_, v := observeTarget(context.Background(), clusterWith(gw, proxy), edge)
		requireStop(t, v, `gateway default/edge class "other", want "kubeharness"`)
	})
	t.Run("no address", func(t *testing.T) {
		gw := programmed.DeepCopy()
		unstructured.RemoveNestedField(gw.Object, "status", "addresses")
		_, v := observeTarget(context.Background(), clusterWith(gw, proxy), edge)
		requirePending(t, v, "no address")
	})
	t.Run("load balancer ip has no service", func(t *testing.T) {
		_, v := observeTarget(context.Background(), clusterWith(programmed), edge)
		requirePending(t, v, "10.96.12.7: no Service has this cluster IP")
	})
	t.Run("hostname only", func(t *testing.T) {
		gw := programmed.DeepCopy()
		if err := unstructured.SetNestedSlice(gw.Object, []any{
			map[string]any{"type": "Hostname", "value": "edge.example"},
		}, "status", "addresses"); err != nil {
			t.Fatal(err)
		}
		_, v := observeTarget(context.Background(), clusterWith(gw, proxy), edge)
		requireStop(t, v, "hostname addresses are not supported")
	})
	t.Run("first ipaddress wins over an earlier hostname", func(t *testing.T) {
		gw := programmed.DeepCopy()
		if err := unstructured.SetNestedSlice(gw.Object, []any{
			map[string]any{"type": "Hostname", "value": "edge.example"},
			map[string]any{"type": "IPAddress", "value": "10.96.12.7"},
		}, "status", "addresses"); err != nil {
			t.Fatal(err)
		}
		got, v := observeTarget(context.Background(), clusterWith(gw, proxy), edge)
		requireDone(t, v)
		want := Target{Gateway: edge, Service: kube.Ref{Namespace: "envoy-gateway-system", Name: "envoy-edge"}, Ports: []int32{80}}
		if !sameTarget(got, want) {
			t.Fatalf("target = %+v", got)
		}
	})
	t.Run("two services for one address", func(t *testing.T) {
		other := proxyService("ops", "also", "10.96.12.7", 80)
		_, v := observeTarget(context.Background(), clusterWith(programmed, proxy, other), edge)
		if v.done || v.why != "" || v.stop == nil || errors.Is(v.stop, kube.ErrNoService) {
			t.Fatalf("verdict = done %v why %q stop %v", v.done, v.why, v.stop)
		}
		for _, want := range []string{"10.96.12.7", "envoy-gateway-system/envoy-edge", "ops/also"} {
			if !strings.Contains(v.stop.Error(), want) {
				t.Errorf("stop error lacks %q: %v", want, v.stop)
			}
		}
	})
	t.Run("service lacks the listener port", func(t *testing.T) {
		_, v := observeTarget(context.Background(), clusterWith(programmed, proxyService("envoy-gateway-system", "envoy-edge", "10.96.12.7", 8080)), edge)
		requirePending(t, v, "proxy Service has ports [8080], lacks listener ports [80]")
	})
	t.Run("ready", func(t *testing.T) {
		got, v := observeTarget(context.Background(), clusterWith(programmed, proxy), edge)
		requireDone(t, v)
		want := Target{Gateway: edge, Service: kube.Ref{Namespace: "envoy-gateway-system", Name: "envoy-edge"}, Ports: []int32{80}}
		if !sameTarget(got, want) {
			t.Fatalf("target = %+v, want %+v", got, want)
		}
	})
}

func TestAttachStopsWhenTheGatewayIsMissing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	target, d, err := Attach(ctx, context.Background(), clusterWith(nil), edge, time.Second)
	if d != nil || !sameTarget(target, Target{}) || err == nil {
		t.Fatalf("Attach = %+v %v %v", target, d, err)
	}
	for _, want := range []string{"gateway default/edge not found", "proxy Service not resolved"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestAttachReturnsNilDialerWhenTheForwardCannotStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// No selector: StartForward stops at once. A missing ready Pod would poll.
	svc := proxyService("envoy-gateway-system", "envoy-edge", "10.96.12.7", 80)
	cl := clusterWith(load(t, "gateway-programmed.yaml"), svc)
	target, d, err := Attach(ctx, context.Background(), cl, edge, time.Second)
	if d != nil || !sameTarget(target, Target{}) || err == nil {
		t.Fatalf("Attach = %+v %v %v", target, d, err)
	}
	for _, want := range []string{"has no selector", "gateway default/edge", "proxy envoy-gateway-system/envoy-edge"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("Attach waited instead of stopping")
	}
}

func clusterWith(objects ...runtime.Object) Cluster {
	// The fake dynamic client stores unstructured objects under a guessed
	// plural. "Gateway" becomes "gatewaies", while the API resource is
	// "gateways". Create with the real GVR so Get and List match production.
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		gatewayGVR: "GatewayList",
		routeGVR:   "HTTPRouteList",
	})
	var kubeObjs []runtime.Object
	for _, obj := range objects {
		if obj == nil {
			continue
		}
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			kubeObjs = append(kubeObjs, obj)
			continue
		}
		gvr := gatewayGVR
		if u.GetKind() == "HTTPRoute" {
			gvr = routeGVR
		}
		if err := dyn.Tracker().Create(gvr, u.DeepCopy(), u.GetNamespace()); err != nil {
			panic(err)
		}
	}
	return Cluster{
		Dynamic: dyn,
		Kube:    fake.NewClientset(kubeObjs...),
		Class:   "kubeharness",
	}
}

func proxyService(ns, name, ip string, ports ...int32) *corev1.Service {
	specPorts := make([]corev1.ServicePort, len(ports))
	for i, port := range ports {
		specPorts[i] = corev1.ServicePort{Name: "p", Port: port}
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       corev1.ServiceSpec{ClusterIP: ip, Ports: specPorts},
	}
}

func samePorts(got, want []int32) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func sameTarget(got, want Target) bool {
	return got.Gateway == want.Gateway && got.Service == want.Service && samePorts(got.Ports, want.Ports)
}
