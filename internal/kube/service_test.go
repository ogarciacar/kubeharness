package kube

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func clusterService(ns, name, clusterIP string, clusterIPs []string, typ corev1.ServiceType, ports ...int32) corev1.Service {
	if typ == "" {
		typ = corev1.ServiceTypeClusterIP
	}
	specPorts := make([]corev1.ServicePort, len(ports))
	for i, port := range ports {
		specPorts[i] = corev1.ServicePort{Port: port}
	}
	return corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: corev1.ServiceSpec{
			Type:       typ,
			ClusterIP:  clusterIP,
			ClusterIPs: clusterIPs,
			Ports:      specPorts,
		},
	}
}

func TestMatchClusterIP(t *testing.T) {
	store := clusterService("shop", "storefront", "10.0.0.8", []string{"10.0.0.8", "fd00::8"}, "", 80, 443)
	legacy := clusterService("shop", "legacy", "10.0.0.4", nil, "", 8080)
	other := clusterService("ops", "other", "10.0.0.9", []string{"10.0.0.9"}, "", 80)
	headless := clusterService("shop", "headless", corev1.ClusterIPNone, []string{"10.0.0.8"}, "", 80)
	noneList := clusterService("shop", "none", "", []string{corev1.ClusterIPNone}, "", 80)
	external := clusterService("shop", "ext", "10.0.0.8", []string{"10.0.0.8"}, corev1.ServiceTypeExternalName, 80)
	dup := clusterService("ops", "also", "10.0.0.8", nil, "", 80)

	cases := []struct {
		name  string
		ip    string
		svcs  []corev1.Service
		want  Service
		noSvc bool
		names []string
	}{
		{name: "dual-stack second address", ip: "fd00::8", svcs: []corev1.Service{store, other}, want: Service{Ref: Ref{Namespace: "shop", Name: "storefront"}, Ports: []int32{80, 443}}},
		{name: "clusterIP when clusterIPs is empty", ip: "10.0.0.4", svcs: []corev1.Service{legacy, store}, want: Service{Ref: Ref{Namespace: "shop", Name: "legacy"}, Ports: []int32{8080}}},
		{name: "clusterIPs wins over a different clusterIP", ip: "10.0.0.1", svcs: []corev1.Service{clusterService("shop", "split", "10.0.0.1", []string{"10.0.0.8"}, "", 80)}, noSvc: true},
		{name: "None anywhere in clusterIPs", ip: "10.0.0.8", svcs: []corev1.Service{clusterService("shop", "mixed", "10.0.0.8", []string{"10.0.0.8", corev1.ClusterIPNone}, "", 80)}, noSvc: true},
		{name: "headless", ip: "10.0.0.8", svcs: []corev1.Service{headless}, noSvc: true},
		{name: "clusterIPs None", ip: "None", svcs: []corev1.Service{noneList}, noSvc: true},
		{name: "external name", ip: "10.0.0.8", svcs: []corev1.Service{external}, noSvc: true},
		{name: "none", ip: "10.9.9.9", svcs: []corev1.Service{store, other}, noSvc: true},
		{name: "empty address", ip: "", svcs: []corev1.Service{clusterService("shop", "blank", "", nil, "", 80)}, noSvc: true},
		{name: "two", ip: "10.0.0.8", svcs: []corev1.Service{store, dup}, names: []string{"shop/storefront", "ops/also"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := matchClusterIP(c.ip, c.svcs)
			if c.noSvc {
				if !errors.Is(err, ErrNoService) || (c.ip != "" && !strings.Contains(err.Error(), c.ip)) || got.Ref != (Ref{}) || len(got.Ports) != 0 {
					t.Fatalf("matchClusterIP = %+v %v, want ErrNoService naming %q", got, err, c.ip)
				}
				return
			}
			if c.names != nil {
				if err == nil || errors.Is(err, ErrNoService) || got.Ref != (Ref{}) || len(got.Ports) != 0 {
					t.Fatalf("matchClusterIP = %+v %v, want a hard error", got, err)
				}
				for _, name := range c.names {
					if !strings.Contains(err.Error(), name) {
						t.Fatalf("error %v does not list %s", err, name)
					}
				}
				if !strings.Contains(err.Error(), c.ip) {
					t.Fatalf("error %v does not name %s", err, c.ip)
				}
				return
			}
			if err != nil || got.Ref != c.want.Ref || !slices.Equal(got.Ports, c.want.Ports) {
				t.Fatalf("matchClusterIP = %+v %v, want %+v", got, err, c.want)
			}
		})
	}
}

func TestServiceForAddressListsTheClusterOnce(t *testing.T) {
	store := clusterService("shop", "storefront", "10.0.0.8", []string{"10.0.0.8"}, "", 80, 443)
	other := clusterService("ops", "other", "10.0.0.9", nil, "", 80)
	headless := clusterService("shop", "headless", corev1.ClusterIPNone, nil, "", 80)
	client := fake.NewClientset(&store, &other, &headless)

	got, err := ServiceForAddress(context.Background(), client, "10.0.0.8")
	if err != nil || got.Ref != (Ref{Namespace: "shop", Name: "storefront"}) || !slices.Equal(got.Ports, []int32{80, 443}) {
		t.Fatalf("ServiceForAddress = %+v %v", got, err)
	}
	if _, err := ServiceForAddress(context.Background(), client, "10.9.9.9"); !errors.Is(err, ErrNoService) || !strings.Contains(err.Error(), "10.9.9.9") {
		t.Fatalf("missing address: %v", err)
	}

	dup := clusterService("ops", "also", "10.0.0.8", nil, "", 80)
	_, err = ServiceForAddress(context.Background(), fake.NewClientset(&store, &dup), "10.0.0.8")
	if err == nil || errors.Is(err, ErrNoService) || !strings.Contains(err.Error(), "shop/storefront") || !strings.Contains(err.Error(), "ops/also") {
		t.Fatalf("two matches: %v", err)
	}
}

func TestServiceEvidenceDescribesPodsBehindTheSelector(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "echo", Namespace: "default"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "echo"}},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-1", Namespace: "default", Labels: map[string]string{"app": "echo"}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
			Name: "app", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		}}},
	}
	other := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "quiet", Namespace: "default", Labels: map[string]string{"app": "quiet"}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	got := ServiceEvidence(context.Background(), fake.NewClientset(svc, pod, other), Ref{Namespace: "default", Name: "echo"})
	if !strings.Contains(got, "pod echo-1 phase=Running") || !strings.Contains(got, "container app running") || strings.Contains(got, "quiet") {
		t.Fatalf("evidence = %q", got)
	}

	missing := ServiceEvidence(context.Background(), fake.NewClientset(), Ref{Namespace: "default", Name: "echo"})
	if !strings.Contains(missing, "service default/echo") || !strings.Contains(missing, "not found") {
		t.Fatalf("missing service evidence = %q", missing)
	}

	noSelector := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "echo", Namespace: "default"}}
	got = ServiceEvidence(context.Background(), fake.NewClientset(noSelector, pod), Ref{Namespace: "default", Name: "echo"})
	if !strings.Contains(got, "service default/echo: no selector") || strings.Contains(got, "echo-1") {
		t.Fatalf("no selector evidence = %q", got)
	}
}
