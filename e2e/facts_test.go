//go:build e2e

package e2e_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	"github.com/ogarciacar/kubeharness"
	"github.com/ogarciacar/kubeharness/internal/kindcluster"
	"github.com/ogarciacar/kubeharness/internal/kube"
)

const (
	agnhostImage  = "registry.k8s.io/e2e-test-images/agnhost@sha256:352a050380078cb2a1c246357a0dfa2fcf243ee416b92ff28b44a01d1b4b0294"
	postgresImage = "postgres@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5"
)

func waitPodReady(t *testing.T, client kubernetes.Interface, name string, within time.Duration) time.Duration {
	t.Helper()
	began := time.Now()
	ctx, cancel := context.WithTimeout(t.Context(), within)
	defer cancel()
	var last corev1.PodStatus
	for {
		p, err := client.CoreV1().Pods("default").Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			last = p.Status
			for _, c := range p.Status.Conditions {
				if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
					return time.Since(began)
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pod %s not ready after %s; last status: %+v", name, within, last)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// TestFacts checks the three facts PR 1's methods rely on before they are
// built on: port-forward from the host, loading a local image, and the cold
// pull time of the pinned postgres image.
func TestFacts(t *testing.T) {
	h := kubeharness.New(t)
	client := kubernetes.NewForConfigOrDie(h.RESTConfig())

	t.Run("forward serves HTTP and releases its port on close", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "echo", Labels: map[string]string{"app": "echo"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "echo", Image: agnhostImage, Args: []string{"netexec", "--http-port=8080"},
				Ports:          []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}},
				ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromString("http")}}},
			}}},
		}
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "echo"},
			Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "echo"},
				Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromString("http")}}},
		}
		if _, err := client.CoreV1().Pods("default").Create(t.Context(), pod, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := client.CoreV1().Services("default").Create(t.Context(), svc, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		waitPodReady(t, client, "echo", 2*time.Minute)

		before := runtime.NumGoroutine()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		f, err := kube.StartForward(ctx, h.RESTConfig(), client, "default", "echo", 80)
		if err != nil {
			t.Fatalf("start forward: %v", err)
		}
		resp, err := http.Get("http://" + f.Addr + "/hostname")
		if err != nil {
			t.Fatalf("GET through forward: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "echo" {
			t.Fatalf("GET %s/hostname = %d %q, want 200 \"echo\"", f.Addr, resp.StatusCode, body)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close forward: %v", err)
		}
		_ = f.Close()
		if c, err := net.DialTimeout("tcp", f.Addr, time.Second); err == nil {
			c.Close()
			t.Fatalf("port %s still accepts connections after Close", f.Addr)
		}
		http.DefaultClient.CloseIdleConnections()
		var after int
		for range 40 {
			if after = runtime.NumGoroutine(); after <= before {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Logf("forward %s served /hostname; goroutines before=%d after close=%d", f.Addr, before, after)
		if after > before {
			t.Errorf("%d goroutines left after Close", after-before)
		}
	})

	t.Run("a locally built image loads and starts with imagePullPolicy Never", func(t *testing.T) {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		ref := "kubeharness.local/fact-build:" + hex.EncodeToString(b)
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM "+agnhostImage+"\nLABEL kubeharness.fact=build\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("docker", "build", "-q", "-t", ref, dir).CombinedOutput(); err != nil {
			t.Fatalf("docker build: %v\n%s", err, out)
		}
		t.Cleanup(func() { _ = exec.Command("docker", "rmi", ref).Run() })
		if err := kindcluster.New().LoadImage(h.ClusterName(), ref); err != nil {
			t.Fatalf("load image: %v", err)
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "built"},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "built", Image: ref, ImagePullPolicy: corev1.PullNever, Args: []string{"pause"},
			}}},
		}
		if _, err := client.CoreV1().Pods("default").Create(t.Context(), pod, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		t.Logf("built %s, pod ready %s after create", ref, waitPodReady(t, client, "built", time.Minute).Round(time.Millisecond))
	})

	t.Run("cold pull of the pinned postgres image", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "postgres"},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "postgres", Image: postgresImage,
				Env: []corev1.EnvVar{{Name: "POSTGRES_PASSWORD", Value: "test-only"}},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler:  corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"pg_isready", "-U", "postgres"}}},
					PeriodSeconds: 1,
				},
			}}},
		}
		if _, err := client.CoreV1().Pods("default").Create(t.Context(), pod, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		ready := waitPodReady(t, client, "postgres", 3*time.Minute)
		t.Logf("postgres pod ready %s after create on a node without the image", ready.Round(time.Millisecond))
	})
}

// TestFactTaggedImageLoads runs on its own cluster. kubelet refuses a loaded
// tag whose image it already pulled from a registry under another name, so
// sharing a cluster with the agnhost Pods above would make this depend on
// test order.
func TestFactTaggedImageLoads(t *testing.T) {
	h := kubeharness.New(t)
	client := kubernetes.NewForConfigOrDie(h.RESTConfig())

	b := make([]byte, 4)
	_, _ = rand.Read(b)
	ref := "kubeharness.local/fact-load:" + hex.EncodeToString(b)
	if out, err := exec.Command("docker", "pull", "-q", agnhostImage).CombinedOutput(); err != nil {
		t.Fatalf("pull agnhost: %v\n%s", err, out)
	}
	if out, err := exec.Command("docker", "tag", agnhostImage, ref).CombinedOutput(); err != nil {
		t.Fatalf("tag: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", ref).Run() })
	began := time.Now()
	if err := kindcluster.New().LoadImage(h.ClusterName(), ref); err != nil {
		t.Fatalf("load image: %v", err)
	}
	loaded := time.Since(began)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "loaded"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "loaded", Image: ref, ImagePullPolicy: corev1.PullNever, Args: []string{"pause"},
		}}},
	}
	if _, err := client.CoreV1().Pods("default").Create(t.Context(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	ready := waitPodReady(t, client, "loaded", time.Minute)
	t.Logf("loaded %s in %s; pod ready %s later", ref, loaded.Round(time.Millisecond), ready.Round(time.Millisecond))
}
