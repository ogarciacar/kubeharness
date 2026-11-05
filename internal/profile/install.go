package profile

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/ogarciacar/kubeharness/internal/kube"
)

var gatewayClassResource = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gatewayclasses"}

// Install applies r's layers in dependency order and waits for each to take
// effect: Gateway API and controller CRDs until established, the controller
// until its Deployments are rolled out, and the class layer until GatewayClass
// kubeharness is Accepted for its current generation. It returns when the
// controller can serve Gateways. Every wait ends with ctx; the evidence in a
// failure is gathered within evidence.
func Install(ctx, evidence context.Context, cfg *rest.Config, client kubernetes.Interface, r *Recipe) error {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}
	crds, err := decodeLayers(r.Layers.GatewayAPICRDs, r.Layers.ControllerCRDs)
	if err != nil {
		return err
	}
	if _, err := kube.Apply(ctx, cfg, crds); err != nil {
		return fmt.Errorf("apply CRDs: %w", err)
	}
	var names []string
	for _, o := range crds {
		if o.GetKind() == "CustomResourceDefinition" {
			names = append(names, o.GetName())
		}
	}
	if err := kube.WaitEstablished(ctx, dyn, names); err != nil {
		return err
	}

	controller, err := decodeLayers(r.Layers.Controller)
	if err != nil {
		return err
	}
	if _, err := kube.Apply(ctx, cfg, controller); err != nil {
		return fmt.Errorf("apply controller: %w", err)
	}
	for _, d := range r.Controllers {
		if err := kube.WaitDeployment(ctx, evidence, client, d.Namespace, d.Name); err != nil {
			return fmt.Errorf("controller: %w", err)
		}
	}

	class, err := decodeLayers(r.Layers.Class)
	if err != nil {
		return err
	}
	if _, err := kube.Apply(ctx, cfg, class); err != nil {
		return fmt.Errorf("apply class: %w", err)
	}
	if err := waitAccepted(ctx, dyn); err != nil {
		var b strings.Builder
		for _, d := range r.Controllers {
			b.WriteString(kube.DeploymentEvidence(evidence, client, d.Namespace, d.Name))
		}
		return fmt.Errorf("%w%s", err, b.String())
	}
	return nil
}

func decodeLayers(layers ...Layer) ([]*unstructured.Unstructured, error) {
	var objs []*unstructured.Unstructured
	for _, l := range layers {
		o, err := kube.DecodeManifests(l.Path, l.YAML)
		if err != nil {
			return nil, err
		}
		objs = append(objs, o...)
	}
	return objs, nil
}

// waitAccepted polls until GatewayClass kubeharness has Accepted=True written
// for its current generation, so an earlier spec's status does not count.
func waitAccepted(ctx context.Context, client dynamic.Interface) error {
	last := "not observed yet"
	for {
		gc, err := client.Resource(gatewayClassResource).Get(ctx, GatewayClass, metav1.GetOptions{})
		if err == nil {
			var accepted bool
			accepted, last = acceptedState(gc)
			if accepted {
				return nil
			}
		} else if ctx.Err() == nil {
			last = err.Error()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("GatewayClass %s not accepted: %w; last observed: %s", GatewayClass, ctx.Err(), last)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func acceptedState(gc *unstructured.Unstructured) (bool, string) {
	conds, _, _ := unstructured.NestedSlice(gc.Object, "status", "conditions")
	gen := gc.GetGeneration()
	var b strings.Builder
	for _, c := range conds {
		m, _ := c.(map[string]any)
		observed, _, _ := unstructured.NestedInt64(m, "observedGeneration")
		if m["type"] == "Accepted" && m["status"] == "True" && observed == gen {
			return true, ""
		}
		fmt.Fprintf(&b, "\n  condition %v=%v reason=%v observedGeneration=%d (generation %d): %v", m["type"], m["status"], m["reason"], observed, gen, m["message"])
	}
	if b.Len() == 0 {
		return false, "no conditions yet"
	}
	return false, b.String()
}
