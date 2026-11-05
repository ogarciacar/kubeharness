package kubeharness

import (
	"time"

	"github.com/ogarciacar/kubeharness/internal/profile"
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
type GatewayController struct{ r *profile.Recipe }

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
	h.gateway = &GatewayController{r: p.r}
	return h.gateway
}
