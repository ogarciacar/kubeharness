package kubeharness

import (
	"net/http"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/ogarciacar/kubeharness/internal/gateway"
	"github.com/ogarciacar/kubeharness/internal/kube"
	"github.com/ogarciacar/kubeharness/internal/profile"
)

// gatewayTimeout covers the first proxy Pod's image pull, which happens on
// the first Gateway, so it matches readyTimeout.
const gatewayTimeout = readyTimeout

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
	h *Harness
	r *profile.Recipe
	// cl is built once by InstallGateway from the Harness's clients and the
	// recipe's controller facts, so every Gateway shares the same view of the
	// cluster.
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
	c      *GatewayController
	target gateway.Target
	// transport is shared by every Client from this handle, so they share
	// connections and the dialer's forwards. Timeout set on a client applies
	// only to that client.
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

// ref parses "[namespace/]name" for a method's argument and fails the test
// on a malformed one, naming kind ("gateway", "route") in the message.
func (h *Harness) ref(kind, s string) kube.Ref {
	h.t.Helper()
	r, err := kube.ParseRef(s, kube.DefaultNamespace)
	if err != nil {
		h.t.Fatalf("kubeharness: %s %q: %v", kind, s, err)
	}
	return r
}

// controllerRefs converts the recipe's controller Deployments to refs, once,
// in InstallGateway.
func controllerRefs(ds []profile.Deployment) []kube.Ref {
	out := make([]kube.Ref, len(ds))
	for i, d := range ds {
		out[i] = kube.Ref{Namespace: d.Namespace, Name: d.Name}
	}
	return out
}
