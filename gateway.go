package kubeharness

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/ogarciacar/kubeharness/internal/gateway"
	"github.com/ogarciacar/kubeharness/internal/kube"
	"github.com/ogarciacar/kubeharness/internal/profile"
)

const gatewayTimeout = readyTimeout

const (
	routeTimeout    = time.Minute
	responseTimeout = time.Minute
)

// GatewayClass is the GatewayClass that every gateway profile creates. Use it
// as gatewayClassName in Gateway manifests, and the same manifests work under
// every profile.
const GatewayClass = profile.GatewayClass

const installTimeout = 4 * time.Minute

// GatewayProfile is a Gateway API controller with every version pinned: the
// Gateway API CRDs, the controller, and its images. Get one from a
// constructor such as EnvoyGateway. The zero GatewayProfile is not a profile,
// and InstallGateway rejects it.
type GatewayProfile struct{ r *profile.Recipe }

// EnvoyGateway is the gateway-api profile: Envoy Gateway on the Gateway API
// standard channel, configured so Gateways get an address on Kind.
func EnvoyGateway() GatewayProfile { return GatewayProfile{profile.EnvoyGateway()} }

// Name is the profile's name, such as "gateway-api", as the report line shows
// it. It is empty for the zero GatewayProfile.
func (p GatewayProfile) Name() string {
	if p.r == nil {
		return ""
	}
	return p.r.Name
}

// GatewayController is the gateway controller that InstallGateway put in a
// Harness's cluster. It is ready to serve Gateways of class GatewayClass.
type GatewayController struct {
	h  *Harness
	r  *profile.Recipe
	cl gateway.Cluster
}

// InstallGateway installs p into the cluster and returns when the controller
// can serve Gateways: the Gateway API CRDs and the controller's CRDs are
// established, the controller is rolled out, and GatewayClass kubeharness is
// Accepted. The manifests are embedded in this module, so the install needs no
// Helm and no network beyond the controller images kubelet pulls.
//
// The install has 4 minutes, less if go test -timeout ends sooner. On failure
// the test stops with what blocked it: the CRDs that are not established, the
// controller's rollout state and Pod logs, or the GatewayClass conditions with
// the controller's logs. On success the report line gains the profile name
// and the install time.
//
// A cluster holds one gateway profile. Calling InstallGateway again with the
// same profile returns the same controller and installs nothing; calling it
// with a different profile fails the test.
func (h *Harness) InstallGateway(p GatewayProfile) *GatewayController {
	h.t.Helper()
	if p.r == nil {
		h.t.Fatalf("kubeharness: install gateway: zero GatewayProfile; use a constructor such as kubeharness.EnvoyGateway()")
	}
	if h.gateway != nil {
		if h.gateway.r == p.r {
			return h.gateway
		}
		h.t.Fatalf("kubeharness: install gateway %s: cluster %s already has gateway profile %s, and a cluster holds one", p.r.Name, h.name, h.gateway.r.Name)
	}
	ctx, evidence, cancel := h.op(installTimeout)
	defer cancel()
	began := time.Now()
	if err := h.install(ctx, evidence, h.rest, h.client, p.r); err != nil {
		h.fatalf(ctx, "kubeharness: install gateway %s failed after %s: %v", p.r.Name, time.Since(began).Round(time.Second), err)
	}
	h.env.RecordProfile(p.r.Name, time.Since(began))
	dyn, err := dynamic.NewForConfig(h.rest)
	if err != nil {
		h.t.Fatalf("kubeharness: install gateway %s: client: %v", p.r.Name, err)
	}
	h.gateway = &GatewayController{h: h, r: p.r, cl: gateway.Cluster{
		Config:         h.rest,
		Kube:           h.client,
		Dynamic:        dyn,
		Class:          GatewayClass,
		Controller:     p.r.ControllerName,
		ControllerPods: controllerRefs(p.r.Controllers),
	}}
	return h.gateway
}

// Gateway waits until the Gateway named ref ("[namespace/]name", default
// "default") can take traffic from this test, and returns a handle to it.
//
// It returns when the Gateway is Accepted and Programmed for its current
// generation, its status address is the ClusterIP of exactly one Service,
// that Service exposes every listener port, and a loopback port-forward to a
// ready Pod behind it is up for each listener port. A Gateway that does not
// exist, or whose gatewayClassName is not GatewayClass, fails at once. Any
// other unmet condition fails after 3 minutes with the last observation and
// the Gateway's evidence.
//
// Every call returns a new handle with its own forwards, which stop when the
// test ends.
func (c *GatewayController) Gateway(ref string) *Gateway {
	h := c.h
	h.t.Helper()
	gw := h.ref("gateway", ref)
	ctx, evidence, cancel := h.op(gatewayTimeout)
	defer cancel()
	target, d, err := gateway.Attach(ctx, evidence, c.cl, gw, forwardTimeout)
	if err != nil {
		h.fatalf(ctx, "kubeharness: gateway %s: %v", gw, err)
	}
	h.t.Cleanup(func() {
		if err := d.Close(); err != nil {
			h.t.Logf("kubeharness: gateway %s forwards ended with: %v", gw, err)
		}
	})
	return &Gateway{c: c, target: target, transport: gateway.Transport(d)}
}

// Gateway is one Gateway resource as a test reaches it: through its status
// address, the Service behind it, and loopback port-forwards to that
// Service's Pods. Get one from GatewayController.Gateway.
type Gateway struct {
	c         *GatewayController
	target    gateway.Target
	transport *http.Transport
}

// Client returns an HTTP client whose every connection goes to the Gateway,
// whatever the URL host. The URL's port picks the listener. The URL host
// becomes the Host header and, for https, the SNI name, as curl --connect-to
// does. A URL port the Gateway has no listener for fails in Do. The client
// sets no Timeout. Set one on the returned value if a test needs it.
//
// Client is safe to call and use from any goroutine. Its connections fail
// once the test ends.
func (g *Gateway) Client() *http.Client {
	return &http.Client{Transport: g.transport}
}

// AwaitRoute waits until the HTTPRoute named ref ("[namespace/]name", default
// "default") has status from this Gateway's controller, for this Gateway, for
// the route's current generation, and check accepts that status. Status
// written for an earlier generation never reaches check, so AwaitRoute after
// an Apply that changed the route waits for the change.
//
// A route that does not exist, has no parentRef to this Gateway, or has two
// status parents for this Gateway and this controller, fails at once.
// Otherwise AwaitRoute fails after 1 minute with check's last error, the
// route's conditions, and the Gateway's evidence.
func (g *Gateway) AwaitRoute(ref string, check func(RouteStatus) error) {
	h := g.c.h
	h.t.Helper()
	route := h.ref("route", ref)
	ctx, evidence, cancel := h.op(routeTimeout)
	defer cancel()
	err := gateway.AwaitRoute(ctx, evidence, g.c.cl, g.target, route, func(cs gateway.Conditions) error {
		return check(routeStatus(cs))
	})
	if err != nil {
		h.fatalf(ctx, "kubeharness: await route %s on gateway %s: %v", route, g.target.Gateway, err)
	}
}

// AwaitResponse sends req through the Gateway every 250ms until check accepts
// a response, and fails the test after 1 minute with the last attempt's status
// line or transport error, the head of its body, check's last error, and the
// Gateway's evidence. Each attempt sends a fresh clone of req with a 5s limit,
// so req may be reused afterwards. A req whose body cannot be replayed (GetBody
// is nil) fails at once. check sees the response with its body buffered, up to
// 1 MiB. It need not close the body. The error includes the status line the
// proxy sent.
func (g *Gateway) AwaitResponse(req *http.Request, check func(*http.Response) error) {
	h := g.c.h
	h.t.Helper()
	ctx, evidence, cancel := h.op(responseTimeout)
	defer cancel()
	if err := gateway.AwaitResponse(ctx, evidence, g.c.cl, g.target, g.transport, req, check); err != nil {
		h.fatalf(ctx, "kubeharness: await response %s %s on gateway %s: %v", req.Method, req.URL, g.target.Gateway, err)
	}
}

// RouteStatus is what one Gateway's controller says about one HTTPRoute
// attached to that Gateway, for the route's current generation.
// routeStatus is the only constructor. RouteReady rejects any other value.
type RouteStatus struct {
	// Accepted says whether the Gateway accepted the route, for example
	// False with reason NotAllowedByListeners.
	Accepted Condition
	// ResolvedRefs says whether every backendRef resolved, for example False
	// with reason RefNotPermitted or BackendNotFound.
	ResolvedRefs Condition
	fromWait     bool
}

// Condition is one status condition. Status is "True", "False", "Unknown",
// or "" when the controller did not report the condition at all.
type Condition struct{ Status, Reason, Message string }

// RouteReady accepts a route the Gateway accepted and whose backendRefs all
// resolved. Its error names each condition that is not True, with its reason
// and message.
func RouteReady(s RouteStatus) error {
	if !s.fromWait {
		return errors.New("RouteStatus was not observed by AwaitRoute")
	}
	var bad []string
	for _, c := range []struct {
		name string
		cond Condition
	}{
		{name: "Accepted", cond: s.Accepted},
		{name: "ResolvedRefs", cond: s.ResolvedRefs},
	} {
		if c.cond.Status == "True" {
			continue
		}
		if c.cond.Status == "" {
			bad = append(bad, c.name+" not reported")
			continue
		}
		bad = append(bad, fmt.Sprintf("%s=%s reason=%s: %s", c.name, c.cond.Status, c.cond.Reason, c.cond.Message))
	}
	if len(bad) == 0 {
		return nil
	}
	return errors.New(strings.Join(bad, "; "))
}

func routeStatus(cs gateway.Conditions) RouteStatus {
	pick := func(t string) Condition {
		c, _ := cs.Get(t)
		return Condition{Status: c.Status, Reason: c.Reason, Message: c.Message}
	}
	return RouteStatus{Accepted: pick("Accepted"), ResolvedRefs: pick("ResolvedRefs"), fromWait: true}
}

func (h *Harness) ref(kind, s string) kube.Ref {
	h.t.Helper()
	r, err := kube.ParseRef(s, kube.DefaultNamespace)
	if err != nil {
		h.t.Fatalf("kubeharness: %s %q: %v", kind, s, err)
	}
	return r
}

func controllerRefs(ds []profile.Deployment) []kube.Ref {
	out := make([]kube.Ref, len(ds))
	for i, d := range ds {
		out[i] = kube.Ref{Namespace: d.Namespace, Name: d.Name}
	}
	return out
}
