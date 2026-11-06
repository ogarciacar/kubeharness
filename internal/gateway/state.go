// Package gateway reads Gateway API status and reaches a Gateway's data
// plane, for any controller. It parses the fields it needs from unstructured
// objects into the types in this file, so the module takes no
// sigs.k8s.io/gateway-api dependency and no wire type leaves the package.
// The types in this file are pure.
package gateway

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/ogarciacar/kubeharness/internal/kube"
)

const gatewayGroup = "gateway.networking.k8s.io"

// Condition is one status condition.
type Condition struct {
	Type, Status, Reason, Message string
	ObservedGeneration            int64
}

// Conditions is a status.conditions list.
type Conditions []Condition

// Get returns the condition of type t, and whether it is present.
func (cs Conditions) Get(t string) (Condition, bool) {
	for _, c := range cs {
		if c.Type == t {
			return c, true
		}
	}
	return Condition{}, false
}

// ObservedAt reports whether every condition was written for generation g.
// An empty list was observed at no generation.
func (cs Conditions) ObservedAt(g int64) bool {
	if len(cs) == 0 {
		return false
	}
	for _, c := range cs {
		if c.ObservedGeneration != g {
			return false
		}
	}
	return true
}

// Address is one status.addresses entry. An empty type is IPAddress, the
// Gateway API default.
type Address struct{ Type, Value string }

// Listener joins a spec listener with its status entry by name.
type Listener struct {
	Name           string
	Port           int32
	Protocol       string
	Conditions     Conditions
	AttachedRoutes int32
}

// GatewayState is a Gateway as this package needs it.
type GatewayState struct {
	Ref        kube.Ref
	Generation int64
	Class      string // spec.gatewayClassName
	Conditions Conditions
	Addresses  []Address
	Listeners  []Listener
}

// ParseGateway reads a Gateway object. It fails only on a structurally
// impossible object, such as a listener without a port, never on missing status.
func ParseGateway(u *unstructured.Unstructured) (GatewayState, error) {
	if u == nil || u.Object == nil {
		return GatewayState{}, fmt.Errorf("gateway object is nil")
	}
	s := GatewayState{Ref: kube.Ref{Namespace: u.GetNamespace(), Name: u.GetName()}}
	where := "gateway " + s.Ref.String()
	gen, err := objectGeneration(u, where)
	if err != nil {
		return GatewayState{}, err
	}
	s.Generation = gen
	class, _, err := unstructured.NestedString(u.Object, "spec", "gatewayClassName")
	if err != nil {
		return GatewayState{}, fmt.Errorf("%s: %w", where, err)
	}
	s.Class = class
	s.Conditions, err = parseConditions(u.Object, where, "status", "conditions")
	if err != nil {
		return GatewayState{}, err
	}
	s.Addresses, err = parseAddresses(u.Object, where)
	if err != nil {
		return GatewayState{}, err
	}
	s.Listeners, err = parseListeners(u.Object, s.Ref)
	if err != nil {
		return GatewayState{}, err
	}
	return s, nil
}

// Readiness returns Done when the Gateway is Accepted=True and
// Programmed=True, both observed at Generation, with at least one address.
// Otherwise it returns Pending naming what is missing, such as
// "Programmed=False reason=AddressNotAssigned: ..." or "Programmed observed
// generation 1, generation 2". A class other than wantClass is Stop. No
// installed controller will ever program it.
func (s GatewayState) Readiness(wantClass string) Verdict {
	if s.Class != wantClass {
		return Stop(fmt.Errorf("gateway %s class %q, want %q", s.Ref, s.Class, wantClass))
	}
	for _, typ := range []string{"Accepted", "Programmed"} {
		c, ok := s.Conditions.Get(typ)
		if !ok {
			return Pending("%s absent", typ)
		}
		if c.ObservedGeneration != s.Generation {
			return Pending("%s observed generation %d, generation %d", typ, c.ObservedGeneration, s.Generation)
		}
		if c.Status != "True" {
			return Pending("%s=%s reason=%s: %s", typ, c.Status, c.Reason, c.Message)
		}
	}
	if len(s.Addresses) == 0 {
		return Pending("no address")
	}
	return Done()
}

// ListenerPorts is the set of distinct listener ports, ascending.
func (s GatewayState) ListenerPorts() []int32 {
	seen := make(map[int32]struct{}, len(s.Listeners))
	ports := make([]int32, 0, len(s.Listeners))
	for _, l := range s.Listeners {
		if _, ok := seen[l.Port]; ok {
			continue
		}
		seen[l.Port] = struct{}{}
		ports = append(ports, l.Port)
	}
	slices.Sort(ports)
	if len(ports) == 0 {
		return nil
	}
	return ports
}

// RouteParent is the status one controller wrote for one route under one
// parent Gateway. It is one status.parents entry.
type RouteParent struct {
	Gateway    kube.Ref // parentRef with namespace defaulted to the route's
	Controller string   // controllerName
	Conditions Conditions
}

// Route is an HTTPRoute as this package needs it.
type Route struct {
	Ref        kube.Ref
	Generation int64
	// ParentRefs are spec.parentRefs that name a Gateway. The group is
	// gateway.networking.k8s.io or empty, and the kind is Gateway or empty.
	// The namespace defaults to the route's namespace. sectionName and port
	// are ignored. Any parentRef to the Gateway attaches the route to it
	// for this package.
	ParentRefs []kube.Ref
	Parents    []RouteParent
}

// ParseRoute reads an HTTPRoute object.
func ParseRoute(u *unstructured.Unstructured) (Route, error) {
	if u == nil || u.Object == nil {
		return Route{}, fmt.Errorf("route object is nil")
	}
	r := Route{Ref: kube.Ref{Namespace: u.GetNamespace(), Name: u.GetName()}}
	where := "route " + r.Ref.String()
	gen, err := objectGeneration(u, where)
	if err != nil {
		return Route{}, err
	}
	r.Generation = gen
	r.ParentRefs, err = parseParentRefs(u.Object, r.Ref.Namespace, where)
	if err != nil {
		return Route{}, err
	}
	r.Parents, err = parseParents(u.Object, r.Ref.Namespace, where)
	if err != nil {
		return Route{}, err
	}
	return r, nil
}

// StatusFor picks what controller wrote for this route under gw, and says
// whether it may reach a caller's check. It is the only settled-status rule.
//
//   - Stop when the route has no parentRef to gw. Waiting cannot fix that.
//   - Stop when two status.parents entries match gw and controller. Merging
//     them would invent a status the controller did not write as one parent.
//   - Pending "no status from <controller> for gateway <gw> yet" when no
//     entry matches.
//   - Pending "status observed generation 1, route generation 2" when the
//     one entry's conditions are stale or empty.
//   - Done with that entry's conditions otherwise.
//
// Evidence prints every parent from the same parse. It does not call
// StatusFor to decide which parents to show.
func (r Route) StatusFor(gw kube.Ref, controller string) (Conditions, Verdict) {
	if !r.AttachedTo(gw) {
		return nil, Stop(fmt.Errorf("route %s has no parentRef to gateway %s", r.Ref, gw))
	}
	n := 0
	var match RouteParent
	for _, p := range r.Parents {
		if p.Gateway != gw || p.Controller != controller {
			continue
		}
		n++
		match = p
	}
	switch n {
	case 0:
		return nil, Pending("no status from %s for gateway %s yet", controller, gw)
	case 1:
		if !match.Conditions.ObservedAt(r.Generation) {
			observed := staleGeneration(match.Conditions, r.Generation)
			return nil, Pending("status observed generation %d, route generation %d", observed, r.Generation)
		}
		return match.Conditions, Done()
	default:
		return nil, Stop(fmt.Errorf("route %s has %d status parents for gateway %s from %s", r.Ref, n, gw, controller))
	}
}

// AttachedTo reports whether the route has a parentRef to gw.
func (r Route) AttachedTo(gw kube.Ref) bool {
	return slices.Contains(r.ParentRefs, gw)
}

func staleGeneration(cs Conditions, want int64) int64 {
	for _, c := range cs {
		if c.ObservedGeneration != want {
			return c.ObservedGeneration
		}
	}
	return 0
}

func objectGeneration(u *unstructured.Unstructured, where string) (int64, error) {
	n, found, err := fieldInt64(u.Object, "metadata", "generation")
	if err != nil {
		return 0, fmt.Errorf("%s: %w", where, err)
	}
	if !found {
		return 0, nil
	}
	return n, nil
}

func parseAddresses(obj map[string]any, where string) ([]Address, error) {
	raw, found, err := unstructured.NestedSlice(obj, "status", "addresses")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	if !found {
		return nil, nil
	}
	out := make([]Address, 0, len(raw))
	for i, item := range raw {
		m, ok := objectAt(item)
		if !ok {
			return nil, fmt.Errorf("%s status.addresses[%d]: not an object", where, i)
		}
		typ, err := stringField(m, "type")
		if err != nil {
			return nil, fmt.Errorf("%s status.addresses[%d]: %w", where, i, err)
		}
		if typ == "" {
			typ = "IPAddress"
		}
		value, err := stringField(m, "value")
		if err != nil {
			return nil, fmt.Errorf("%s status.addresses[%d]: %w", where, i, err)
		}
		out = append(out, Address{Type: typ, Value: value})
	}
	return out, nil
}

func parseListeners(obj map[string]any, gw kube.Ref) ([]Listener, error) {
	where := "gateway " + gw.String()
	raw, found, err := unstructured.NestedSlice(obj, "spec", "listeners")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	if !found {
		return nil, nil
	}
	statusByName, err := listenerStatusByName(obj, where)
	if err != nil {
		return nil, err
	}
	out := make([]Listener, 0, len(raw))
	for i, item := range raw {
		m, ok := objectAt(item)
		if !ok {
			return nil, fmt.Errorf("%s spec.listeners[%d]: not an object", where, i)
		}
		name, err := stringField(m, "name")
		if err != nil {
			return nil, fmt.Errorf("%s spec.listeners[%d]: %w", where, i, err)
		}
		label := listenerWhere(gw, i, name)
		port, portFound, err := fieldInt64(m, "port")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		if !portFound {
			return nil, fmt.Errorf("%s: port is required", label)
		}
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("%s: port %d is not a listener port", label, port)
		}
		protocol, err := stringField(m, "protocol")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		l := Listener{Name: name, Port: int32(port), Protocol: protocol}
		st, hasStatus := statusByName[name]
		if hasStatus {
			l.Conditions, err = parseConditions(st, label, "conditions")
			if err != nil {
				return nil, err
			}
			attached, attachedFound, err := fieldInt64(st, "attachedRoutes")
			if err != nil {
				return nil, fmt.Errorf("%s: %w", label, err)
			}
			if attachedFound {
				if attached < 0 || attached > math.MaxInt32 {
					return nil, fmt.Errorf("%s: attachedRoutes %d out of range", label, attached)
				}
				l.AttachedRoutes = int32(attached)
			}
		}
		out = append(out, l)
	}
	return out, nil
}

func listenerWhere(gw kube.Ref, index int, name string) string {
	if name == "" {
		return fmt.Sprintf("gateway %s listener %d", gw, index)
	}
	return fmt.Sprintf("gateway %s listener %q", gw, name)
}

func listenerStatusByName(obj map[string]any, where string) (map[string]map[string]any, error) {
	raw, found, err := unstructured.NestedSlice(obj, "status", "listeners")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	if !found {
		return nil, nil
	}
	byName := make(map[string]map[string]any, len(raw))
	for i, item := range raw {
		m, ok := objectAt(item)
		if !ok {
			return nil, fmt.Errorf("%s status.listeners[%d]: not an object", where, i)
		}
		name, err := stringField(m, "name")
		if err != nil {
			return nil, fmt.Errorf("%s status.listeners[%d]: %w", where, i, err)
		}
		if _, exists := byName[name]; exists {
			continue
		}
		byName[name] = m
	}
	return byName, nil
}

func parseParentRefs(obj map[string]any, routeNS, where string) ([]kube.Ref, error) {
	raw, found, err := unstructured.NestedSlice(obj, "spec", "parentRefs")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	if !found {
		return nil, nil
	}
	out := make([]kube.Ref, 0, len(raw))
	for i, item := range raw {
		m, ok := objectAt(item)
		if !ok {
			return nil, fmt.Errorf("%s spec.parentRefs[%d]: not an object", where, i)
		}
		ref, keep, err := gatewayRef(m, routeNS)
		if err != nil {
			return nil, fmt.Errorf("%s spec.parentRefs[%d]: %w", where, i, err)
		}
		if keep {
			out = append(out, ref)
		}
	}
	return out, nil
}

func parseParents(obj map[string]any, routeNS, where string) ([]RouteParent, error) {
	raw, found, err := unstructured.NestedSlice(obj, "status", "parents")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	if !found {
		return nil, nil
	}
	out := make([]RouteParent, 0, len(raw))
	for i, item := range raw {
		m, ok := objectAt(item)
		if !ok {
			return nil, fmt.Errorf("%s status.parents[%d]: not an object", where, i)
		}
		pref, found, err := unstructured.NestedFieldNoCopy(m, "parentRef")
		if err != nil {
			return nil, fmt.Errorf("%s status.parents[%d]: %w", where, i, err)
		}
		if !found {
			continue
		}
		pm, ok := objectAt(pref)
		if !ok {
			return nil, fmt.Errorf("%s status.parents[%d].parentRef: not an object", where, i)
		}
		ref, keep, err := gatewayRef(pm, routeNS)
		if err != nil {
			return nil, fmt.Errorf("%s status.parents[%d].parentRef: %w", where, i, err)
		}
		if !keep {
			continue
		}
		controller, err := stringField(m, "controllerName")
		if err != nil {
			return nil, fmt.Errorf("%s status.parents[%d]: %w", where, i, err)
		}
		conds, err := parseConditions(m, fmt.Sprintf("%s status.parents[%d]", where, i), "conditions")
		if err != nil {
			return nil, err
		}
		out = append(out, RouteParent{Gateway: ref, Controller: controller, Conditions: conds})
	}
	return out, nil
}

func gatewayRef(m map[string]any, routeNS string) (kube.Ref, bool, error) {
	group, err := stringField(m, "group")
	if err != nil {
		return kube.Ref{}, false, err
	}
	kind, err := stringField(m, "kind")
	if err != nil {
		return kube.Ref{}, false, err
	}
	if (group != "" && group != gatewayGroup) || (kind != "" && kind != "Gateway") {
		return kube.Ref{}, false, nil
	}
	name, err := stringField(m, "name")
	if err != nil {
		return kube.Ref{}, false, err
	}
	if name == "" {
		return kube.Ref{}, false, nil
	}
	ns, err := stringField(m, "namespace")
	if err != nil {
		return kube.Ref{}, false, err
	}
	if ns == "" {
		ns = routeNS
	}
	return kube.Ref{Namespace: ns, Name: name}, true, nil
}

func parseConditions(obj map[string]any, where string, fields ...string) (Conditions, error) {
	raw, found, err := unstructured.NestedSlice(obj, fields...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	if !found {
		return nil, nil
	}
	field := strings.Join(fields, ".")
	out := make(Conditions, 0, len(raw))
	for i, item := range raw {
		m, ok := objectAt(item)
		if !ok {
			return nil, fmt.Errorf("%s %s[%d]: not an object", where, field, i)
		}
		typ, err := stringField(m, "type")
		if err != nil {
			return nil, fmt.Errorf("%s %s[%d]: %w", where, field, i, err)
		}
		status, err := stringField(m, "status")
		if err != nil {
			return nil, fmt.Errorf("%s condition %s: %w", where, typ, err)
		}
		reason, err := stringField(m, "reason")
		if err != nil {
			return nil, fmt.Errorf("%s condition %s: %w", where, typ, err)
		}
		message, err := stringField(m, "message")
		if err != nil {
			return nil, fmt.Errorf("%s condition %s: %w", where, typ, err)
		}
		observed, _, err := fieldInt64(m, "observedGeneration")
		if err != nil {
			return nil, fmt.Errorf("%s condition %s: %w", where, typ, err)
		}
		out = append(out, Condition{
			Type:               typ,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: observed,
		})
	}
	return out, nil
}

func stringField(m map[string]any, key string) (string, error) {
	s, _, err := unstructured.NestedString(m, key)
	return s, err
}

func fieldInt64(obj map[string]any, fields ...string) (int64, bool, error) {
	val, found, err := unstructured.NestedFieldNoCopy(obj, fields...)
	if !found || err != nil {
		return 0, found, err
	}
	n, err := asInt64(val)
	if err != nil {
		return 0, true, fmt.Errorf("%s: %w", strings.Join(fields, "."), err)
	}
	return n, true, nil
}

func asInt64(v any) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case int32:
		return int64(n), nil
	case int:
		return int64(n), nil
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, fmt.Errorf("%v is not an integer", n)
		}
		return i, nil
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
			return 0, fmt.Errorf("%v is not an integer", n)
		}
		return int64(n), nil
	default:
		return 0, fmt.Errorf("%T is not an integer", v)
	}
}

func objectAt(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}
