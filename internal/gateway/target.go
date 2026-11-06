package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/ogarciacar/kubeharness/internal/kube"
)

var (
	gatewayGVR = schema.GroupVersionResource{Group: gatewayGroup, Version: "v1", Resource: "gateways"}
	routeGVR   = schema.GroupVersionResource{Group: gatewayGroup, Version: "v1", Resource: "httproutes"}
)

// Cluster is the cluster and the installed controller as this package sees
// them. The root package builds one per GatewayController from the Harness's
// clients and the recipe. Nothing here writes it.
type Cluster struct {
	Config  *rest.Config
	Kube    kubernetes.Interface
	Dynamic dynamic.Interface
	// Class is the GatewayClass every profile creates.
	Class string
	// Controller is the class's controllerName. Route status from any other
	// controller is ignored.
	Controller string
	// ControllerPods are the controller Deployments, for evidence only.
	ControllerPods []kube.Ref
}

// Target is everything later operations need to know about one Gateway,
// resolved once inside Attach and immutable afterwards. Forwards, route
// waits, and evidence all read it. None re-derive it.
//
// Invariant: every port in Ports is a listener port of Gateway and a port of
// Service, and Service's cluster IP was Gateway's status address when Attach
// returned.
type Target struct {
	Gateway kube.Ref
	Service kube.Ref
	Ports   []int32 // ascending, distinct
}

// Attach is the only production sequence from a Gateway ref to live forwards.
// It resolves an immutable Target, then opens the dialer for that Target.
// budget caps one re-forward. The caller passes the harness forwardTimeout.
// This package does not declare a second limit.
//
// A non-nil error means the dialer is nil. awaitTarget's error already
// carries Evidence. openDialer closes any forward it started before it
// returns.
func Attach(ctx, evidenceCtx context.Context, cl Cluster, gw kube.Ref, budget time.Duration) (Target, *Dialer, error) {
	t, err := awaitTarget(ctx, evidenceCtx, cl, gw)
	if err != nil {
		return Target{}, nil, err
	}
	d, err := openDialer(ctx, evidenceCtx, cl, t, budget)
	if err != nil {
		return Target{}, nil, err
	}
	return t, d, nil
}

// awaitTarget polls until observeTarget is Done. Only Attach calls it.
// On failure the error carries the last reason and Evidence for gw.
func awaitTarget(ctx, evidenceCtx context.Context, cl Cluster, gw kube.Ref) (Target, error) {
	return Await(ctx, evidenceCtx, kube.PollInterval, func(ctx context.Context) (Target, Verdict) {
		return observeTarget(ctx, cl, gw)
	}, func(ctx context.Context) string {
		return Evidence(ctx, cl, gw, kube.Ref{})
	})
}

func observeTarget(ctx context.Context, cl Cluster, gw kube.Ref) (Target, Verdict) {
	if cl.Dynamic == nil {
		return Target{}, Stop(fmt.Errorf("gateway %s: no dynamic client", gw))
	}
	u, err := cl.Dynamic.Resource(gatewayGVR).Namespace(gw.Namespace).Get(ctx, gw.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return Target{}, Stop(fmt.Errorf("gateway %s not found; apply it before Gateway(ref)", gw))
		}
		return Target{}, Pending("%s", err.Error())
	}
	s, err := ParseGateway(u)
	if err != nil {
		return Target{}, Stop(err)
	}
	if v := s.Readiness(cl.Class); !v.done {
		return Target{}, v
	}
	ip, err := firstIPAddress(s)
	if err != nil {
		return Target{}, Stop(err)
	}
	if cl.Kube == nil {
		return Target{}, Stop(fmt.Errorf("gateway %s: no kubernetes client", gw))
	}
	svc, err := kube.ServiceForAddress(ctx, cl.Kube, ip)
	if err != nil {
		if errors.Is(err, kube.ErrNoService) {
			return Target{}, Pending("%s", err.Error())
		}
		return Target{}, Stop(err)
	}
	ports, err := portPlan(s.ListenerPorts(), svc.Ports)
	if err != nil {
		return Target{}, Pending("%s", err.Error())
	}
	return Target{Gateway: gw, Service: svc.Ref, Ports: ports}, Done()
}

// firstIPAddress returns the first status address of type IPAddress.
// A Gateway whose addresses are only hostnames has no cluster IP to forward
// to, and waiting will not create one.
func firstIPAddress(s GatewayState) (string, error) {
	hostnames := 0
	for _, a := range s.Addresses {
		switch a.Type {
		case "IPAddress":
			return a.Value, nil
		case "Hostname":
			hostnames++
		}
	}
	if len(s.Addresses) > 0 && hostnames == len(s.Addresses) {
		return "", errors.New("hostname addresses are not supported")
	}
	return "", fmt.Errorf("gateway %s has no IPAddress", s.Ref)
}

// portPlan returns the ports to forward: the listener ports, each of which
// must also be a port of the proxy Service. The Gateway API leaves the
// Service's shape to the controller. Envoy Gateway's Service port equals the
// listener port.
func portPlan(listeners, servicePorts []int32) ([]int32, error) {
	if len(listeners) == 0 {
		return nil, errors.New("gateway has no listener ports")
	}
	have := make(map[int32]struct{}, len(servicePorts))
	for _, p := range servicePorts {
		have[p] = struct{}{}
	}
	seen := make(map[int32]struct{}, len(listeners))
	var ports, missing []int32
	for _, p := range listeners {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		if _, ok := have[p]; ok {
			ports = append(ports, p)
			continue
		}
		missing = append(missing, p)
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return nil, fmt.Errorf("proxy Service has ports %v, lacks listener ports %v", sortedUnique(servicePorts), missing)
	}
	slices.Sort(ports)
	return ports, nil
}

func sortedUnique(ports []int32) []int32 {
	if len(ports) == 0 {
		return []int32{}
	}
	out := append([]int32(nil), ports...)
	slices.Sort(out)
	return slices.Compact(out)
}
