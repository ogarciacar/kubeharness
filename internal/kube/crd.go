package kube

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var crdResource = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

// WaitEstablished polls until every named CustomResourceDefinition reports
// Established=True, so objects of its kinds can be created. When ctx ends, the
// error names each CRD that is not established with its last conditions.
func WaitEstablished(ctx context.Context, client dynamic.Interface, names []string) error {
	last := map[string]string{}
	for {
		for _, name := range names {
			if state, seen := last[name]; seen && state == "" {
				continue
			}
			crd, err := client.Resource(crdResource).Get(ctx, name, metav1.GetOptions{})
			switch {
			case err == nil:
				last[name] = notEstablished(crd)
			case ctx.Err() == nil:
				last[name] = err.Error()
			default:
				if _, seen := last[name]; !seen {
					last[name] = "not observed yet"
				}
			}
		}
		var pending []string
		for _, name := range names {
			if last[name] != "" {
				pending = append(pending, fmt.Sprintf("\n  crd %s: %s", name, last[name]))
			}
		}
		if len(pending) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%d of %d CRDs not established: %w%s", len(pending), len(names), ctx.Err(), strings.Join(pending, ""))
		case <-time.After(pollInterval):
		}
	}
}

// notEstablished returns "" for an established CRD, and otherwise its
// conditions, such as NamesAccepted=False when a kind name conflicts.
func notEstablished(crd *unstructured.Unstructured) string {
	conds, _, _ := unstructured.NestedSlice(crd.Object, "status", "conditions")
	var b strings.Builder
	for _, c := range conds {
		m, _ := c.(map[string]any)
		if m["type"] == "Established" && m["status"] == "True" {
			return ""
		}
		fmt.Fprintf(&b, " %v=%v reason=%v: %v;", m["type"], m["status"], m["reason"], m["message"])
	}
	if b.Len() == 0 {
		return "no conditions yet"
	}
	return strings.TrimSuffix(strings.TrimPrefix(b.String(), " "), ";")
}
