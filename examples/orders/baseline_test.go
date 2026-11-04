//go:build e2e

package orders_test

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/cluster/nodeutils"
)

// setupBaseline is the same setup as setupWithHarness, written the way a test
// does it without kubeharness: kind's Go library, kubectl, and Go. Like the
// harness, it stops at a failed migration and prints the Job's logs.
func setupBaseline(t *testing.T, migrations string) (orders, postgres string) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	name := "m01-baseline-" + hex.EncodeToString(b)
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	provider := cluster.NewProvider()
	if err := provider.Create(name, cluster.CreateWithKubeconfigPath(kubeconfig), cluster.CreateWithWaitForReady(2*time.Minute)); err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	t.Cleanup(func() { _ = provider.Delete(name, kubeconfig) })
	kubectl := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("kubectl", append([]string{"--kubeconfig", kubeconfig}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("kubectl %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for out, err := exec.Command("kubectl", "--kubeconfig", kubeconfig, "get", "serviceaccount", "default").CombinedOutput(); err != nil; out, err = exec.Command("kubectl", "--kubeconfig", kubeconfig, "get", "serviceaccount", "default").CombinedOutput() {
		if time.Now().After(deadline) {
			t.Fatalf("default ServiceAccount: %v\n%s", err, out)
		}
		time.Sleep(time.Second)
	}
	image, err := exec.Command("docker", "save", ordersImage).Output()
	if err != nil {
		t.Fatalf("docker save: %v", err)
	}
	nodes, _ := provider.ListInternalNodes(name)
	for _, n := range nodes {
		if err := nodeutils.LoadImageArchive(n, bytes.NewReader(image)); err != nil {
			t.Fatalf("load image: %v", err)
		}
	}
	kubectl("apply", "-f", "testdata/postgres")
	kubectl("rollout", "status", "deployment/postgres", "--timeout=3m")
	kubectl("apply", "-f", migrations)
	deadline = time.Now().Add(3 * time.Minute)
	for conditions := ""; !strings.Contains(conditions, "Complete"); conditions = kubectl("get", "job/migrate", "-o", "jsonpath={.status.conditions[*].type}") {
		if strings.Contains(conditions, "Failed") {
			t.Fatalf("migration failed:\n%s", kubectl("logs", "job/migrate"))
		}
		if time.Now().After(deadline) {
			t.Fatalf("migration did not finish in 3m; conditions: %q", conditions)
		}
		time.Sleep(time.Second)
	}
	kubectl("apply", "-f", "testdata/orders")
	kubectl("rollout", "status", "deployment/orders", "--timeout=3m")
	forward := func(target string, port int) string {
		cmd := exec.Command("kubectl", "--kubeconfig", kubeconfig, "port-forward", target, fmt.Sprintf(":%d", port))
		stdout, _ := cmd.StdoutPipe()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		r := bufio.NewReader(stdout)
		line, _ := r.ReadString('\n')
		go io.Copy(io.Discard, r)
		m := forwardingFrom.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("kubectl port-forward %s: %q", target, line)
		}
		return m[1]
	}
	return forward("svc/orders", 80), forward("svc/postgres", 5432)
}

var forwardingFrom = regexp.MustCompile(`Forwarding from (127\.0\.0\.1:\d+)`)

func TestCreateOrderBaseline(t *testing.T) {
	buildOrdersImage(t)
	orders, postgres := setupBaseline(t, "testdata/migrate")
	assertCreateOrder(t, orders, postgres)
}
