package kubeharness

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func readyNode(status corev1.ConditionStatus, reason string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "kh-x-control-plane"},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status, Reason: reason}}},
	}
}

func TestUsableRequiresReadyNodesAndDefaultServiceAccount(t *testing.T) {
	ctx := context.Background()
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "default"}}
	cases := []struct {
		name    string
		objects []any
		want    string
	}{
		{"no nodes", nil, "no nodes registered"},
		{"node not ready", []any{readyNode(corev1.ConditionFalse, "KubeletNotReady"), sa}, "KubeletNotReady"},
		{"node ready unknown", []any{readyNode(corev1.ConditionUnknown, "NodeStatusUnknown"), sa}, "NodeStatusUnknown"},
		{"no default service account", []any{readyNode(corev1.ConditionTrue, "")}, "ServiceAccount"},
		{"usable", []any{readyNode(corev1.ConditionTrue, ""), sa}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := fake.NewClientset()
			for _, o := range c.objects {
				switch o := o.(type) {
				case *corev1.Node:
					client.Tracker().Add(o)
				case *corev1.ServiceAccount:
					client.Tracker().Add(o)
				}
			}
			err := observeUsable(ctx, client)
			if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
				t.Fatalf("observeUsable = %v, want %q", err, c.want)
			}
		})
	}
}

func TestReadinessTimeoutReportsLastRealObservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	calls := 0
	observe := func(ctx context.Context) error {
		calls++
		if calls == 1 {
			return errors.New("node kh-x-control-plane Ready=False (KubeletNotReady)")
		}
		<-ctx.Done()
		return errors.New("list nodes: " + ctx.Err().Error())
	}
	err := pollUsable(ctx, observe, 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "KubeletNotReady") {
		t.Fatalf("last real observation lost: %v", err)
	}
}

func TestWaitUsableStopsAtItsDeadlineWhenTheAPIServerNeverAnswers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	cfg := strings.ReplaceAll(fakeKubeconfig, "https://127.0.0.1:1", "http://"+ln.Addr().String())
	if err := os.WriteFile(kubeconfig, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	began := time.Now()
	err = waitUsable(ctx, kubeconfig)
	if took := time.Since(began); err == nil || took > time.Second {
		t.Fatalf("waitUsable took %s past a 300ms deadline: %v", took, err)
	}
}

func TestEveryReadinessCallHonoursTheDeadline(t *testing.T) {
	for _, hang := range []string{"/api/v1/nodes", "/api/v1/namespaces/default/serviceaccounts/default"} {
		t.Run(hang, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case hang:
					<-r.Context().Done()
				case "/version":
					fmt.Fprint(w, `{"major":"1","minor":"37","gitVersion":"v1.37.0"}`)
				case "/api/v1/nodes":
					fmt.Fprint(w, `{"kind":"NodeList","apiVersion":"v1","items":[{"metadata":{"name":"n"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
			cfg := strings.ReplaceAll(fakeKubeconfig, "https://127.0.0.1:1", srv.URL)
			if err := os.WriteFile(kubeconfig, []byte(cfg), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			began := time.Now()
			err := waitUsable(ctx, kubeconfig)
			if took := time.Since(began); err == nil || took > time.Second {
				t.Fatalf("waitUsable took %s past a 300ms deadline while %s hung: %v", took, hang, err)
			}
		})
	}
}
