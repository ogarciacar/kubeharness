package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/ogarciacar/kubeharness/internal/kube"
)

// BackendRef is one spec.rules[].backendRefs[] entry of an HTTPRoute whose
// group is not the core group. Service refs are not BackendRefs. The proxy
// section and the data plane already speak for them.
//
// Invariant: GroupKind.Group != "". Ref.Namespace defaults to the route's.
type BackendRef struct {
	schema.GroupKind
	Ref kube.Ref
}

// parseBackends reads spec.rules[].backendRefs[] and keeps non-core entries.
// A malformed entry is an error naming the route, the rule index, and the
// ref index, as parseParentRefs does.
func parseBackends(obj map[string]any, routeNS, where string) ([]BackendRef, error) {
	rules, found, err := unstructured.NestedSlice(obj, "spec", "rules")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	if !found {
		return nil, nil
	}
	var out []BackendRef
	seen := make(map[BackendRef]struct{})
	for i, rule := range rules {
		m, ok := objectAt(rule)
		if !ok {
			return nil, fmt.Errorf("%s spec.rules[%d]: not an object", where, i)
		}
		refs, found, err := unstructured.NestedSlice(m, "backendRefs")
		if err != nil {
			return nil, fmt.Errorf("%s spec.rules[%d]: %w", where, i, err)
		}
		if !found {
			continue
		}
		for j, item := range refs {
			ref, keep, err := oneBackendRef(item, routeNS)
			if err != nil {
				return nil, fmt.Errorf("%s spec.rules[%d].backendRefs[%d]: %w", where, i, j, err)
			}
			if !keep {
				continue
			}
			if _, ok := seen[ref]; ok {
				continue
			}
			seen[ref] = struct{}{}
			out = append(out, ref)
		}
	}
	return out, nil
}

func oneBackendRef(item any, routeNS string) (BackendRef, bool, error) {
	m, ok := objectAt(item)
	if !ok {
		return BackendRef{}, false, errors.New("not an object")
	}
	group, err := stringField(m, "group")
	if err != nil {
		return BackendRef{}, false, err
	}
	if group == "" {
		return BackendRef{}, false, nil
	}
	kind, err := stringField(m, "kind")
	if err != nil {
		return BackendRef{}, false, err
	}
	if kind == "" {
		return BackendRef{}, false, errors.New("kind is required")
	}
	name, err := stringField(m, "name")
	if err != nil {
		return BackendRef{}, false, err
	}
	if name == "" {
		return BackendRef{}, false, errors.New("name is required")
	}
	ns, err := stringField(m, "namespace")
	if err != nil {
		return BackendRef{}, false, err
	}
	if ns == "" {
		ns = routeNS
	}
	return BackendRef{
		GroupKind: schema.GroupKind{Group: group, Kind: kind},
		Ref:       kube.Ref{Namespace: ns, Name: name},
	}, true, nil
}

// writeBackends appends, under one route's lines, one line per backend.
// Conditions come from status.conditions and print with describeConditions,
// the same text the route and Gateway lines use. An object with no
// status.conditions prints "no status conditions". It never fails. What it
// cannot read, it names.
func writeBackends(b *strings.Builder, ctx context.Context, cl Cluster, backends []BackendRef) {
	for _, br := range backends {
		label := backendLabel(br)
		gvr, ok := cl.Kinds[br.GroupKind]
		if !ok {
			fmt.Fprintf(b, "\n  %s: kind not installed by the profile", label)
			continue
		}
		if cl.Dynamic == nil {
			fmt.Fprintf(b, "\n  %s: no dynamic client", label)
			continue
		}
		u, err := cl.Dynamic.Resource(gvr).Namespace(br.Ref.Namespace).Get(ctx, br.Ref.Name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				fmt.Fprintf(b, "\n  %s: not found", label)
				continue
			}
			fmt.Fprintf(b, "\n  %s: %s", label, err.Error())
			continue
		}
		fmt.Fprintf(b, "\n%s", describeBackend(br, u))
	}
}

func backendLabel(br BackendRef) string {
	return fmt.Sprintf("backend %s/%s %s", br.Group, br.Kind, br.Ref)
}

// describeBackend is the formatting half of writeBackends for an object
// that was read. The caller writes the line under the route.
func describeBackend(br BackendRef, u *unstructured.Unstructured) string {
	label := backendLabel(br)
	if u == nil || u.Object == nil {
		return "  " + label + ": not found"
	}
	conds, err := parseConditions(u.Object, label, "status", "conditions")
	if err != nil {
		return "  " + label + ": " + err.Error()
	}
	text := describeConditions(conds)
	if text == "" {
		text = "no status conditions"
	}
	return fmt.Sprintf("  %s generation %d: %s", label, u.GetGeneration(), text)
}
