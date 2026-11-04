//go:build e2e

package e2e_test

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/ogarciacar/kubeharness"
)

func TestWorkloadMethodsOnRealKind(t *testing.T) {
	h := kubeharness.New(t)
	client := kubernetes.NewForConfigOrDie(h.RESTConfig())

	var addr string
	// Registered before Forward, so it runs after the forward's own cleanup
	// and before the cluster is deleted, which would also drop the port.
	t.Cleanup(func() {
		if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			c.Close()
			t.Errorf("forward %s still listening after its cleanup ran", addr)
		}
	})

	h.Apply("testdata/echo")
	h.WaitReady("echo")
	addr = h.Forward("echo", 80)
	resp, err := http.Get("http://" + addr + "/hostname")
	if err != nil {
		t.Fatalf("GET through Forward: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), "echo-") {
		t.Fatalf("GET %s/hostname = %d %q, want 200 from an echo Pod", addr, resp.StatusCode, body)
	}

	_, port, _ := net.SplitHostPort(addr)
	for _, ip := range nonLoopbackIPs(t) {
		if c, err := net.DialTimeout("tcp", net.JoinHostPort(ip, port), time.Second); err == nil {
			c.Close()
			t.Fatalf("forward %s also accepts connections on %s; it must listen on loopback only", addr, ip)
		}
	}

	applied, err := client.AppsV1().Deployments("default").Get(t.Context(), "echo", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var managers []string
	for _, m := range applied.ManagedFields {
		managers = append(managers, m.Manager)
	}
	if !slices.Contains(managers, "kubeharness") {
		t.Fatalf("Apply's field manager missing from %v", managers)
	}

	scale, err := client.AppsV1().Deployments("default").GetScale(t.Context(), "echo", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	scale.Spec.Replicas = 2
	if _, err := client.AppsV1().Deployments("default").UpdateScale(t.Context(), "echo", scale, metav1.UpdateOptions{FieldManager: "e2e-scaler"}); err != nil {
		t.Fatal(err)
	}
	h.Apply("testdata/echo")
	d, err := client.AppsV1().Deployments("default").Get(t.Context(), "echo", metav1.GetOptions{})
	if err != nil || d.Spec.Replicas == nil || *d.Spec.Replicas != 1 {
		t.Fatalf("re-Apply after another manager scaled to 2 left replicas at %v (%v)", d.Spec.Replicas, err)
	}

	h.Apply("testdata/namespaced")
	if _, err := client.CoreV1().ConfigMaps("other").Get(t.Context(), "placed", metav1.GetOptions{}); err != nil {
		t.Fatalf("ConfigMap with namespace: other not in other: %v", err)
	}

	h.Apply("testdata/crd")
	h.Apply("testdata/widget")

	h.RunJob("testdata/job-ok")
	first := jobPods(t, client, "migrate-ok")
	h.RunJob("testdata/job-ok")
	second := jobPods(t, client, "migrate-ok")
	if len(first) != 1 || len(second) != 1 || first[0] == second[0] {
		t.Fatalf("second RunJob did not run the Job again: pods before %v, after %v", first, second)
	}
	edited := t.TempDir()
	job, err := os.ReadFile("testdata/job-ok/job.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(edited, "job.yaml"), []byte(strings.Replace(string(job), "schema applied", "schema applied again", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	h.RunJob(edited)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM "+agnhostImage+"\nLABEL kubeharness.e2e=loaded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const ref = "kubeharness.local/e2e-loaded:v1"
	if out, err := exec.Command("docker", "build", "-q", "-t", ref, dir).CombinedOutput(); err != nil {
		t.Fatalf("docker build: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", ref).Run() })
	h.LoadImage(ref)
	h.Apply("testdata/loaded")
	h.WaitReady("loaded")
}

// TestHelperFailingJob is run in a subprocess by
// TestFailingJobStopsTheTestWithEvidence; it fails on purpose.
func TestHelperFailingJob(t *testing.T) {
	if os.Getenv("KUBEHARNESS_E2E_HELPER") != "failing-job" {
		t.Skip("helper for TestFailingJobStopsTheTestWithEvidence")
	}
	h := kubeharness.New(t)
	h.RunJob("testdata/job-fail")
	t.Log("business assertions reached")
}

func TestFailingJobStopsTheTestWithEvidence(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperFailingJob$", "-test.v")
	cmd.Env = append(os.Environ(), "KUBEHARNESS_E2E_HELPER=failing-job")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err == nil {
		t.Fatalf("helper passed, want RunJob to fail it:\n%s", out.String())
	}
	got := out.String()
	for _, want := range []string{
		"kubeharness: run job: job migrate-fail failed",
		"reason=BackoffLimitExceeded",
		"terminated exit=3",
		`relation "orders" does not exist`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if strings.Contains(got, "business assertions reached") {
		t.Error("the test kept running after RunJob failed")
	}
	m := regexp.MustCompile(`kubeharness cluster=(\S+) phase=ready cause=test-failed cleanup=succeeded remaining=0`).FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("no report line with test-failed and succeeded cleanup:\n%s", got)
	}
	if left := nodeContainers(t, m[1]); len(left) > 0 {
		t.Errorf("cluster %s leaked containers %v", m[1], left)
	}
	if t.Failed() {
		t.Logf("helper output:\n%s", got)
	}
}

func jobPods(t *testing.T, client kubernetes.Interface, job string) []string {
	t.Helper()
	pods, err := client.CoreV1().Pods("default").List(t.Context(), metav1.ListOptions{LabelSelector: "batch.kubernetes.io/job-name=" + job})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range pods.Items {
		names = append(names, p.Name)
	}
	return names
}

func TestForwardCleanupDoesNotWaitOnABlockedWrite(t *testing.T) {
	var bodyDone time.Time
	ok := t.Run("blocked write", func(t *testing.T) {
		h := kubeharness.New(t)
		client := kubernetes.NewForConfigOrDie(h.RESTConfig())
		h.Apply("testdata/sink")
		waitPodReady(t, client, "sink", 2*time.Minute)
		addr := h.Forward("sink", 8080)
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		n, err := c.Write(make([]byte, 512<<20))
		c.Close()
		t.Logf("wrote %d MiB before the write blocked: %v", n>>20, err)
		bodyDone = time.Now()
	})
	if took := time.Since(bodyDone); !ok || took > 30*time.Second {
		t.Fatalf("cleanup took %s after the test body returned (subtest ok=%v)", took.Round(time.Second), ok)
	}
}

func nonLoopbackIPs(t *testing.T) []string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var ips []string
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			ips = append(ips, n.IP.String())
		}
	}
	if len(ips) == 0 {
		t.Skip("no non-loopback IPv4 address to check against")
	}
	return ips
}
