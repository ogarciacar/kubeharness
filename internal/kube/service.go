package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Service is a Service as a port-forward needs it: where it is and which
// ports it exposes.
type Service struct {
	Ref   Ref
	Ports []int32 // spec.ports[].port, in spec order
}

// ErrNoService means no Service has the address as a cluster IP yet, or ever
// (for example a LoadBalancer IP from a class misconfigured for Kind). It is
// the only error ServiceForAddress returns that a caller may retry on.
var ErrNoService = errors.New("no Service has this cluster IP")

// ServiceForAddress finds the one Service in the cluster whose cluster IPs
// include ip. Headless and ExternalName Services never match. One List call,
// all namespaces.
func ServiceForAddress(ctx context.Context, client kubernetes.Interface, ip string) (Service, error) {
	list, err := client.CoreV1().Services(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return Service{}, fmt.Errorf("list Services: %w", err)
	}
	return matchClusterIP(ip, list.Items)
}

// matchClusterIP is ServiceForAddress without the List. Zero matches wrap
// ErrNoService and name the address. Two or more matches list them. A caller
// must not retry that error: a healthy API server never assigns one cluster
// IP twice.
func matchClusterIP(ip string, svcs []corev1.Service) (Service, error) {
	var found []Service
	for i := range svcs {
		if !hasClusterIP(&svcs[i], ip) {
			continue
		}
		found = append(found, serviceFrom(&svcs[i]))
	}
	switch len(found) {
	case 0:
		return Service{}, fmt.Errorf("%s: %w", ip, ErrNoService)
	case 1:
		return found[0], nil
	default:
		names := make([]string, len(found))
		for i, svc := range found {
			names[i] = svc.Ref.String()
		}
		return Service{}, fmt.Errorf("%s matches %s", ip, strings.Join(names, ", "))
	}
}

// hasClusterIP reports whether ip is one of the Service's cluster IPs.
// Headless Services set ClusterIP or an entry of ClusterIPs to None, and
// ExternalName Services have no cluster IP. Neither matches. When ClusterIPs
// is empty, ClusterIP is the list. Older objects and fakes still look like that.
func hasClusterIP(svc *corev1.Service, ip string) bool {
	if ip == "" || svc.Spec.Type == corev1.ServiceTypeExternalName || svc.Spec.ClusterIP == corev1.ClusterIPNone {
		return false
	}
	ips := svc.Spec.ClusterIPs
	if len(ips) == 0 {
		ips = []string{svc.Spec.ClusterIP}
	}
	for _, candidate := range ips {
		if candidate == corev1.ClusterIPNone {
			return false
		}
	}
	for _, candidate := range ips {
		if candidate != "" && candidate == ip {
			return true
		}
	}
	return false
}

func serviceFrom(svc *corev1.Service) Service {
	ports := make([]int32, len(svc.Spec.Ports))
	for i, port := range svc.Spec.Ports {
		ports[i] = port.Port
	}
	return Service{Ref: Ref{Namespace: svc.Namespace, Name: svc.Name}, Ports: ports}
}

// ServiceEvidence describes the Pods behind a Service for a failure message,
// in the same form as pod evidence: phase, container states, log tails. It
// never fails. What it cannot read, it names.
func ServiceEvidence(ctx context.Context, client kubernetes.Interface, svc Ref) string {
	s, err := client.CoreV1().Services(svc.Namespace).Get(ctx, svc.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Sprintf("\n  service %s: %v", svc, err)
	}
	if len(s.Spec.Selector) == 0 {
		return fmt.Sprintf("\n  service %s: no selector", svc)
	}
	return podEvidence(ctx, client, svc.Namespace, &metav1.LabelSelector{MatchLabels: s.Spec.Selector})
}
