package gateway

import (
	"context"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ogarciacar/kubeharness/internal/kube"
)

// Evidence describes a Gateway for a failure message, within ctx:
//
//	gateway default/edge generation 1 class kubeharness: Accepted=True Programmed=True; addresses 10.96.12.7
//	  listener http port 80 HTTP: Accepted=True Programmed=True ResolvedRefs=True attachedRoutes=2
//	route default/shop generation 2:
//	  parent default/edge by gateway.envoyproxy.io/gatewayclass-controller observed 2: Accepted=True ResolvedRefs=False reason=BackendNotFound: ...
//	proxy envoy-gateway-system/envoy-default-edge-1a2b3c (service):
//	  <kube.ServiceEvidence>
//	controller envoy-gateway-system/envoy-gateway:
//	  <kube.DeploymentEvidence>
//
// Routes are every HTTPRoute in the cluster attached to gw, one List, each
// with every status.parents entry. A stale entry, a foreign controller, or
// the two parents that made StatusFor stop are often the explanation. svc is
// the zero Ref before Attach resolved it. The proxy section then says the
// Service is not resolved and names the address that failed to resolve, if
// the Gateway has one.
//
// It never fails. What it cannot read, it names.
func Evidence(ctx context.Context, cl Cluster, gw, svc kube.Ref) string {
	var b strings.Builder
	state, haveState := writeGateway(&b, ctx, cl, gw)
	writeRoutes(&b, ctx, cl, gw)
	writeProxy(&b, ctx, cl, svc, state, haveState)
	writeControllers(&b, ctx, cl)
	return strings.TrimRight(b.String(), "\n")
}

func writeGateway(b *strings.Builder, ctx context.Context, cl Cluster, gw kube.Ref) (GatewayState, bool) {
	if cl.Dynamic == nil {
		fmt.Fprintf(b, "gateway %s: no dynamic client\n", gw)
		return GatewayState{}, false
	}
	u, err := cl.Dynamic.Resource(gatewayGVR).Namespace(gw.Namespace).Get(ctx, gw.Name, metav1.GetOptions{})
	if err != nil {
		fmt.Fprintf(b, "gateway %s: %s\n", gw, err.Error())
		return GatewayState{}, false
	}
	s, err := ParseGateway(u)
	if err != nil {
		fmt.Fprintf(b, "gateway %s: %s\n", gw, err.Error())
		return GatewayState{}, false
	}
	b.WriteString(describeGateway(s))
	b.WriteByte('\n')
	return s, true
}

func writeRoutes(b *strings.Builder, ctx context.Context, cl Cluster, gw kube.Ref) {
	if cl.Dynamic == nil {
		b.WriteString("routes: no dynamic client\n")
		return
	}
	var lines []string
	var cont string
	for {
		list, err := cl.Dynamic.Resource(routeGVR).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{Continue: cont})
		if err != nil {
			fmt.Fprintf(b, "routes: %s\n", err.Error())
			return
		}
		for i := range list.Items {
			item := &list.Items[i]
			r, err := ParseRoute(item)
			if err != nil {
				lines = append(lines, fmt.Sprintf("route %s/%s: %s", item.GetNamespace(), item.GetName(), err.Error()))
				continue
			}
			if !r.AttachedTo(gw) {
				continue
			}
			var block strings.Builder
			block.WriteString(describeRoute(r))
			writeBackends(&block, ctx, cl, r.Backends)
			lines = append(lines, block.String())
		}
		cont = list.GetContinue()
		if cont == "" {
			break
		}
	}
	slices.Sort(lines)
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

func writeProxy(b *strings.Builder, ctx context.Context, cl Cluster, svc kube.Ref, state GatewayState, haveState bool) {
	if svc == (kube.Ref{}) {
		if haveState {
			if addr := addressList(state); addr != "" {
				fmt.Fprintf(b, "proxy Service not resolved: address %s\n", addr)
				return
			}
		}
		b.WriteString("proxy Service not resolved\n")
		return
	}
	if cl.Kube == nil {
		fmt.Fprintf(b, "proxy %s (service): no client\n", svc)
		return
	}
	fmt.Fprintf(b, "proxy %s (service):%s\n", svc, kube.ServiceEvidence(ctx, cl.Kube, svc))
}

func writeControllers(b *strings.Builder, ctx context.Context, cl Cluster) {
	for _, ref := range cl.ControllerPods {
		if cl.Kube == nil {
			fmt.Fprintf(b, "controller %s: no client\n", ref)
			continue
		}
		fmt.Fprintf(b, "controller %s:%s\n", ref, kube.DeploymentEvidence(ctx, cl.Kube, ref.Namespace, ref.Name))
	}
}

func addressList(s GatewayState) string {
	vals := make([]string, 0, len(s.Addresses))
	for _, a := range s.Addresses {
		if a.Value != "" {
			vals = append(vals, a.Value)
		}
	}
	return strings.Join(vals, ", ")
}

func describeGateway(s GatewayState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gateway %s generation %d class %s: %s", s.Ref, s.Generation, s.Class, describeConditions(s.Conditions))
	if addr := addressList(s); addr != "" {
		fmt.Fprintf(&b, "; addresses %s", addr)
	}
	for _, l := range s.Listeners {
		cond := describeConditions(l.Conditions)
		if cond != "" {
			cond += " "
		}
		fmt.Fprintf(&b, "\n  listener %s port %d %s: %sattachedRoutes=%d", l.Name, l.Port, l.Protocol, cond, l.AttachedRoutes)
	}
	return b.String()
}

func describeRoute(r Route) string {
	var b strings.Builder
	fmt.Fprintf(&b, "route %s generation %d:", r.Ref, r.Generation)
	if len(r.Parents) == 0 {
		b.WriteString("\n  no status parents")
		return b.String()
	}
	for _, p := range r.Parents {
		fmt.Fprintf(&b, "\n  parent %s by %s observed %d: %s", p.Gateway, p.Controller, observedGeneration(p.Conditions), describeConditions(p.Conditions))
	}
	return b.String()
}

func describeConditions(cs Conditions) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		if c.Status == "True" {
			parts = append(parts, c.Type+"="+c.Status)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s reason=%s: %s", c.Type, c.Status, c.Reason, c.Message))
	}
	return strings.Join(parts, " ")
}

func observedGeneration(cs Conditions) int64 {
	if len(cs) == 0 {
		return 0
	}
	return cs[0].ObservedGeneration
}
