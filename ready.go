package kubeharness

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// waitUsable polls until the API answers, every node is Ready, and the
// default ServiceAccount exists. Pods cannot be created before that account
// appears, so node readiness alone is not usable readiness.
func waitUsable(ctx context.Context, kubeconfigPath string) error {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("load kubeconfig: %w", err)
	}
	cfg.Timeout = 5 * time.Second
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build client: %w", err)
	}
	return pollUsable(ctx, func(ctx context.Context) error { return observeUsable(ctx, client) }, 500*time.Millisecond)
}

func pollUsable(ctx context.Context, observe func(context.Context) error, interval time.Duration) error {
	var last error
	for {
		notUsable := observe(ctx)
		if notUsable == nil {
			return nil
		}
		if ctx.Err() == nil || last == nil {
			last = notUsable
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("cluster not usable; last observed: %v: %w", last, ctx.Err())
		case <-time.After(interval):
		}
	}
}

func observeUsable(ctx context.Context, client kubernetes.Interface) error {
	if _, err := discovery.ToServerVersionInterfaceWithContext(client.Discovery()).ServerVersionWithContext(ctx); err != nil {
		return fmt.Errorf("API server unreachable: %v", err)
	}
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list nodes: %v", err)
	}
	if len(nodes.Items) == 0 {
		return errors.New("no nodes registered")
	}
	for _, n := range nodes.Items {
		if status, reason := nodeReady(n); status != corev1.ConditionTrue {
			return fmt.Errorf("node %s Ready=%s (%s)", n.Name, status, reason)
		}
	}
	if _, err := client.CoreV1().ServiceAccounts("default").Get(ctx, "default", metav1.GetOptions{}); err != nil {
		return fmt.Errorf("default ServiceAccount missing: %v", err)
	}
	return nil
}

func nodeReady(n corev1.Node) (corev1.ConditionStatus, string) {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status, c.Reason
		}
	}
	return corev1.ConditionUnknown, "no Ready condition"
}
