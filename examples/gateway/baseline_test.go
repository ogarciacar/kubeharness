//go:build e2e

package gateway_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/cluster/nodeutils"
)

const (
	gatewayAPICRDs      = "https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.6.1/standard-install.yaml"
	envoyGatewayCRDs    = "oci://docker.io/envoyproxy/gateway-crds-helm"
	envoyGatewayChart   = "oci://docker.io/envoyproxy/gateway-helm"
	envoyGatewayVersion = "v1.9.3"
)

// setupBaseline is the G01 setup written the way a test does it without
// kubeharness: kind's Go library, helm, kubectl, and Go. It returns a client
// that sends every request to the Gateway, and logs how long each step took.
func setupBaseline(t *testing.T) *http.Client {
	for _, tool := range []string{"kubectl", "helm"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	var timings []string
	last := start
	mark := func(step string) {
		now := time.Now()
		timings = append(timings, fmt.Sprintf("%s=%.1f", step, now.Sub(last).Seconds()))
		last = now
	}

	b := make([]byte, 4)
	_, _ = rand.Read(b)
	name := "g01-baseline-" + hex.EncodeToString(b)
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	provider := cluster.NewProvider()
	if err := provider.Create(name, cluster.CreateWithKubeconfigPath(kubeconfig), cluster.CreateWithWaitForReady(2*time.Minute)); err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	t.Cleanup(func() { _ = provider.Delete(name, kubeconfig) })
	run := func(stdin []byte, tool string, args ...string) string {
		t.Helper()
		cmd := exec.Command(tool, append([]string{"--kubeconfig", kubeconfig}, args...)...)
		cmd.Stdin = bytes.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", tool, args, err, out)
		}
		return string(out)
	}
	kubectl := func(args ...string) string { t.Helper(); return run(nil, "kubectl", args...) }
	deadline := time.Now().Add(2 * time.Minute)
	for out, err := exec.Command("kubectl", "--kubeconfig", kubeconfig, "get", "serviceaccount", "default").CombinedOutput(); err != nil; out, err = exec.Command("kubectl", "--kubeconfig", kubeconfig, "get", "serviceaccount", "default").CombinedOutput() {
		if time.Now().After(deadline) {
			t.Fatalf("default ServiceAccount: %v\n%s", err, out)
		}
		time.Sleep(time.Second)
	}
	var version struct{ GitVersion string }
	_ = json.Unmarshal([]byte(kubectl("get", "--raw", "/version")), &version)
	mark("create")

	kubectl("apply", "--server-side", "-f", gatewayAPICRDs)
	// helm v4 prints "Pulled:" to stdout when it templates an OCI chart,
	// which breaks a pipe into kubectl, so pull the charts first.
	charts := t.TempDir()
	for _, chart := range []string{envoyGatewayCRDs, envoyGatewayChart} {
		if out, err := exec.Command("helm", "pull", chart, "--version", envoyGatewayVersion, "--untar", "-d", charts).CombinedOutput(); err != nil {
			t.Fatalf("helm pull %s: %v\n%s", chart, err, out)
		}
	}
	egCRDs, err := exec.Command("helm", "template", "eg-crds", filepath.Join(charts, "gateway-crds-helm"),
		"--set", "crds.gatewayAPI.enabled=false", "--set", "crds.envoyGateway.enabled=true").Output()
	if err != nil {
		t.Fatalf("helm template gateway-crds-helm: %v", err)
	}
	run(egCRDs, "kubectl", "apply", "--server-side", "-f", "-")
	kubectl("wait", "--for=condition=Established", "crd", "--all", "--timeout=1m")
	mark("crds")

	run(nil, "helm", "install", "eg", filepath.Join(charts, "gateway-helm"),
		"-n", "envoy-gateway-system", "--create-namespace", "--set", "crds.enabled=false")
	kubectl("-n", "envoy-gateway-system", "rollout", "status", "deployment/envoy-gateway", "--timeout=3m")
	mark("controller-ready")

	kubectl("apply", "-f", "testdata/class")
	kubectl("wait", "--for=condition=Accepted", "gatewayclass/kubeharness", "--timeout=2m")
	mark("class-accepted")

	image, err := exec.Command("docker", "save", backendImage).Output()
	if err != nil {
		t.Fatalf("docker save: %v", err)
	}
	nodes, _ := provider.ListInternalNodes(name)
	for _, n := range nodes {
		if err := nodeutils.LoadImageArchive(n, bytes.NewReader(image)); err != nil {
			t.Fatalf("load image: %v", err)
		}
	}
	kubectl("apply", "-f", "testdata/g01/orders.yaml", "-f", "testdata/g01/payments.yaml")
	kubectl("rollout", "status", "deployment/orders", "--timeout=3m")
	kubectl("rollout", "status", "deployment/payments", "--timeout=3m")
	mark("backends-ready")

	kubectl("apply", "-f", "testdata/g01/gateway.yaml")
	kubectl("wait", "--for=condition=Programmed", "gateway/edge", "--timeout=3m")
	mark("programmed")

	address := kubectl("get", "gateway/edge", "-o", "jsonpath={.status.addresses[0].value}")
	var services struct {
		Items []struct {
			Metadata struct{ Name, Namespace string }
			Spec     struct{ ClusterIP string }
		}
	}
	if err := json.Unmarshal([]byte(kubectl("get", "services", "-A", "-o", "json")), &services); err != nil {
		t.Fatalf("list services: %v", err)
	}
	var namespace, service string
	matches := 0
	for _, s := range services.Items {
		if address != "" && s.Spec.ClusterIP == address {
			namespace, service = s.Metadata.Namespace, "svc/"+s.Metadata.Name
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("Gateway edge address %q matches the cluster IP of %d Services, want 1", address, matches)
	}
	t.Logf("Gateway edge address %s is the cluster IP of %s/%s", address, namespace, service)
	cmd := exec.Command("kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "port-forward", service, ":80")
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
		t.Fatalf("kubectl port-forward %s: %q", service, line)
	}
	var dialer net.Dialer
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, m[1])
		},
	}}
	deadline = time.Now().Add(2 * time.Minute)
	for {
		resp, err := client.Get(g01Routes[0].url)
		if err == nil {
			resp.Body.Close()
			if resp.Header.Get("X-Backend") == g01Routes[0].backend {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no response from %s through %s/%s in 2m: %v", g01Routes[0].backend, namespace, service, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
	mark("first-response")

	t.Logf("g01-baseline-timing kubernetes=%s %s total=%.1f", version.GitVersion, strings.Join(timings, " "), time.Since(start).Seconds())
	logImagePulls(t, kubectl("get", "events", "-A", "--field-selector", "reason=Pulled", "-o", "json"))
	return client
}

var (
	forwardingFrom = regexp.MustCompile(`Forwarding from (127\.0\.0\.1:\d+)`)
	pulledIn       = regexp.MustCompile(`^Successfully pulled image "([^"]+)" in (\S+) `)
)

// logImagePulls logs how long kubelet took to pull each image, from the
// messages of its Pulled events. Images already on the node are skipped.
func logImagePulls(t *testing.T, events string) {
	t.Helper()
	var list struct{ Items []struct{ Message string } }
	if err := json.Unmarshal([]byte(events), &list); err != nil {
		t.Fatalf("Pulled events: %v", err)
	}
	for _, e := range list.Items {
		m := pulledIn.FindStringSubmatch(e.Message)
		if m == nil {
			continue
		}
		d, err := time.ParseDuration(m[2])
		if err != nil {
			t.Fatalf("Pulled event %q: %v", e.Message, err)
		}
		t.Logf("g01-baseline-pull image=%s seconds=%.1f", m[1], d.Seconds())
	}
}

func TestG01RoutingBaseline(t *testing.T) {
	buildBackendImage(t)
	assertRouting(t, setupBaseline(t))
}
