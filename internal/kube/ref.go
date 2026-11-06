package kube

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// Ref names a namespaced object. The zero value names nothing, and ParseRef
// never returns it with a nil error.
type Ref struct {
	Namespace string
	Name      string
}

// String is "namespace/name", the form every message uses.
func (r Ref) String() string { return r.Namespace + "/" + r.Name }

// ParseRef reads "[namespace/]name". A bare name is in defaultNamespace.
// The namespace must be a DNS-1123 label. The name must be a DNS-1123
// subdomain, because Gateway and HTTPRoute names may contain dots. There is
// at most one "/". It is the only parser of user-supplied refs.
func ParseRef(s, defaultNamespace string) (Ref, error) {
	ns, name, found := strings.Cut(s, "/")
	if !found {
		ns, name = defaultNamespace, s
	}
	if ns == "" || name == "" || strings.Contains(name, "/") {
		return Ref{}, fmt.Errorf("ref %q: want [namespace/]name", s)
	}
	if msgs := validation.IsDNS1123Label(ns); len(msgs) > 0 {
		return Ref{}, fmt.Errorf("ref %q: namespace: %s", s, strings.Join(msgs, "; "))
	}
	if msgs := validation.IsDNS1123Subdomain(name); len(msgs) > 0 {
		return Ref{}, fmt.Errorf("ref %q: name: %s", s, strings.Join(msgs, "; "))
	}
	return Ref{Namespace: ns, Name: name}, nil
}
