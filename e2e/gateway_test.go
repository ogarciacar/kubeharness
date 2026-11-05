//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/ogarciacar/kubeharness"
	"github.com/ogarciacar/kubeharness/internal/kube"
	"github.com/ogarciacar/kubeharness/internal/profile"
)

var (
	gatewayClasses = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gatewayclasses"}
	crds           = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	reportLine     = regexp.MustCompile(`kubeharness cluster=(\S+) phase=ready cause=(\S+) cleanup=(\S+) remaining=(\d+) .*`)
)

// runHelper runs one helper test in a subprocess, because only its output
// shows the report line that the cluster's cleanup logs.
func runHelper(t *testing.T, test string, extraArgs ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^" + test + "$", "-test.v"}, extraArgs...)...)
	cmd.Env = append(os.Environ(), "KUBEHARNESS_E2E_HELPER=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func TestHelperInstallGateway(t *testing.T) {
	if os.Getenv("KUBEHARNESS_E2E_HELPER") != "1" {
		t.Skip("helper for TestInstallGateway")
	}
	h := kubeharness.New(t)
	gc := h.InstallGateway(kubeharness.EnvoyGateway())

	client := dynamic.NewForConfigOrDie(h.RESTConfig())
	class, err := client.Resource(gatewayClasses).Get(t.Context(), kubeharness.GatewayClass, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("GatewayClass %s after InstallGateway: %v", kubeharness.GatewayClass, err)
	}
	conds, _, _ := unstructured.NestedSlice(class.Object, "status", "conditions")
	accepted := false
	for _, c := range conds {
		m := c.(map[string]any)
		gen, _, _ := unstructured.NestedInt64(m, "observedGeneration")
		accepted = accepted || m["type"] == "Accepted" && m["status"] == "True" && gen == class.GetGeneration()
	}
	if !accepted {
		t.Fatalf("GatewayClass %s not Accepted for generation %d: %v", kubeharness.GatewayClass, class.GetGeneration(), conds)
	}

	crd, err := client.Resource(crds).Get(t.Context(), "gateways.gateway.networking.k8s.io", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pin := profile.EnvoyGateway().Pins.GatewayAPI
	if got := crd.GetAnnotations()["gateway.networking.k8s.io/bundle-version"]; got != pin.Version {
		t.Fatalf("Gateway CRD bundle-version %q in the cluster, pinned %q", got, pin.Version)
	}

	began := time.Now()
	if again := h.InstallGateway(kubeharness.EnvoyGateway()); again != gc {
		t.Fatal("second InstallGateway with the same profile returned a different controller")
	}
	if took := time.Since(began); took > time.Second {
		t.Fatalf("second InstallGateway took %s; it should install nothing", took)
	}
}

func TestInstallGateway(t *testing.T) {
	out, err := runHelper(t, "TestHelperInstallGateway")
	if err != nil {
		t.Fatalf("helper failed: %v\n%s", err, out)
	}
	m := reportLine.FindStringSubmatch(out)
	if m == nil || m[2] != "ok" || m[3] != "succeeded" || m[4] != "0" {
		t.Fatalf("no report line with cause=ok cleanup=succeeded remaining=0:\n%s", out)
	}
	install := regexp.MustCompile(` profile=gateway-api install=(\S+)`).FindStringSubmatch(m[0])
	if install == nil {
		t.Fatalf("report line lacks profile=gateway-api install=: %s", m[0])
	}
	t.Logf("install=%s (%s)", install[1], m[0])
	if left := nodeContainers(t, m[1]); len(left) > 0 {
		t.Errorf("cluster %s leaked containers %v", m[1], left)
	}
}

// badControllerImage names a digest no registry has, so kubelet can never
// pull the controller.
const badControllerImage = "docker.io/envoyproxy/gateway:v1.9.3@sha256:dead00000000000000000000000000000000000000000000000000000000beef"

func TestHelperInstallGatewayWithAMissingControllerImage(t *testing.T) {
	if os.Getenv("KUBEHARNESS_E2E_HELPER") != "1" {
		t.Skip("helper for TestInstallGatewayFailureCarriesControllerEvidence")
	}
	broken := *profile.EnvoyGateway()
	broken.Layers.Controller.YAML = withDeploymentImage(t, broken.Layers.Controller, "envoy-gateway", badControllerImage)
	profile.EnvoyGateway = func() *profile.Recipe { return &broken }

	h := kubeharness.New(t)
	h.InstallGateway(kubeharness.EnvoyGateway())
	t.Fatal("InstallGateway returned although the controller image does not exist")
}

// The helper's -timeout makes the install end at the harness's operation
// deadline, about 105s in, well before the 4m install timeout.
func TestInstallGatewayFailureCarriesControllerEvidence(t *testing.T) {
	began := time.Now()
	out, err := runHelper(t, "TestHelperInstallGatewayWithAMissingControllerImage", "-test.timeout=2m30s")
	took := time.Since(began)
	if err == nil {
		t.Fatalf("helper passed with a missing controller image:\n%s", out)
	}
	for _, want := range []string{
		"install gateway gateway-api failed after",
		"deployment envoy-gateway not available",
		"pod envoy-gateway-",
		"sha256:dead0000",
		"stopped early so cleanup fits before go test -timeout",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("failure output lacks %q", want)
		}
	}
	m := reportLine.FindStringSubmatch(out)
	if m == nil || m[2] != "test-failed" || m[3] != "succeeded" || m[4] != "0" || strings.Contains(m[0], "profile=") {
		t.Errorf("want a report line with cause=test-failed cleanup=succeeded remaining=0 and no profile")
	}
	if m != nil {
		if left := nodeContainers(t, m[1]); len(left) > 0 {
			t.Errorf("cluster %s leaked containers %v", m[1], left)
		}
	}
	if t.Failed() {
		t.Logf("helper output after %s:\n%s", took.Round(time.Second), out)
	}
	t.Logf("install failed and the cluster was removed within %s", took.Round(time.Second))
}

// withDeploymentImage returns l with every container of the Deployment named
// deployment set to image. JSON documents are valid YAML.
func withDeploymentImage(t *testing.T, l profile.Layer, deployment, image string) []byte {
	t.Helper()
	objs, err := kube.DecodeManifests(l.Path, l.YAML)
	if err != nil {
		t.Fatal(err)
	}
	var docs [][]byte
	changed := false
	for _, o := range objs {
		if o.GetKind() == "Deployment" && o.GetName() == deployment {
			containers, _, _ := unstructured.NestedSlice(o.Object, "spec", "template", "spec", "containers")
			for _, c := range containers {
				c.(map[string]any)["image"] = image
			}
			if err := unstructured.SetNestedSlice(o.Object, containers, "spec", "template", "spec", "containers"); err != nil {
				t.Fatal(err)
			}
			changed = true
		}
		doc, err := json.Marshal(o.Object)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, doc)
	}
	if !changed {
		t.Fatalf("no Deployment %s in %s", deployment, l.Path)
	}
	return bytes.Join(docs, []byte("\n---\n"))
}
