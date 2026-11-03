//go:build e2e

package e2e_test

import (
	"bytes"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/kind/pkg/cluster"

	"github.com/ogarciacar/kubeharness"
)

func nodeContainers(t *testing.T, name string) []string {
	t.Helper()
	out, err := exec.Command("docker", "ps", "-a", "--filter", "label=io.x-k8s.kind.cluster="+name, "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	return strings.Fields(string(out))
}

func foreignClusters(t *testing.T) []string {
	t.Helper()
	names, err := cluster.NewProvider().List()
	if err != nil {
		t.Fatalf("container runtime unavailable, e2e tests need Docker: %v", err)
	}
	return slices.Sorted(slices.Values(slices.DeleteFunc(names, func(n string) bool { return strings.HasPrefix(n, "kh-") })))
}

// ensureDecoyKindCluster makes sure a cluster named "kind" exists, because
// kind maps an empty name to it.
func ensureDecoyKindCluster(t *testing.T) {
	t.Helper()
	if slices.Contains(foreignClusters(t), "kind") {
		return
	}
	p := cluster.NewProvider()
	kubeconfig := filepath.Join(t.TempDir(), "decoy-kubeconfig")
	if err := p.Create("kind", cluster.CreateWithKubeconfigPath(kubeconfig)); err != nil {
		t.Fatalf("create decoy cluster: %v", err)
	}
	t.Cleanup(func() {
		if err := p.Delete("kind", kubeconfig); err != nil {
			t.Errorf("delete decoy cluster: %v", err)
		}
	})
}

func TestNewOwnedClusterIsUsableAndDisposed(t *testing.T) {
	ensureDecoyKindCluster(t)
	before := foreignClusters(t)
	decoyNodes := nodeContainers(t, "kind")
	sentinel := filepath.Join(t.TempDir(), "developer-kubeconfig")
	if err := os.WriteFile(sentinel, []byte("developer: untouched\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := func() [32]byte { b, _ := os.ReadFile(sentinel); return sha256.Sum256(b) }
	want := sum()
	t.Setenv("KUBECONFIG", sentinel)

	var name string
	t.Run("owned", func(t *testing.T) {
		h := kubeharness.New(t)
		name = h.ClusterName()
		client := kubernetes.NewForConfigOrDie(h.RESTConfig())
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "probe"},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "pause", Image: "registry.k8s.io/pause:3.10"}}},
		}
		if _, err := client.CoreV1().Pods("default").Create(t.Context(), pod, metav1.CreateOptions{}); err != nil {
			t.Fatalf("cluster reported usable but rejected a Pod: %v", err)
		}
	})

	if left := nodeContainers(t, name); len(left) > 0 {
		t.Errorf("owned cluster %s leaked containers %v", name, left)
	}
	if sum() != want {
		t.Error("developer KUBECONFIG was modified")
	}
	if after := foreignClusters(t); !slices.Equal(before, after) {
		t.Errorf("clusters not owned by the harness changed: before=%v after=%v", before, after)
	}
	if after := nodeContainers(t, "kind"); !slices.Equal(decoyNodes, after) || len(after) == 0 {
		t.Errorf("decoy \"kind\" cluster nodes changed: before=%v after=%v", decoyNodes, after)
	}
}

func TestParallelClustersAreDistinctAndDisposed(t *testing.T) {
	var mu sync.Mutex
	var names []string
	t.Run("group", func(t *testing.T) {
		for _, n := range []string{"a", "b", "c"} {
			t.Run(n, func(t *testing.T) {
				t.Parallel()
				h := kubeharness.New(t)
				mu.Lock()
				names = append(names, h.ClusterName())
				mu.Unlock()
			})
		}
	})
	if distinct := slices.Compact(slices.Sorted(slices.Values(names))); len(distinct) != 3 {
		t.Fatalf("names = %v", names)
	}
	for _, n := range names {
		if left := nodeContainers(t, n); len(left) > 0 {
			t.Errorf("cluster %s leaked containers %v", n, left)
		}
	}
}

func TestHelperFailingTest(t *testing.T) {
	if os.Getenv("KUBEHARNESS_E2E_HELPER") != "1" {
		t.Skip("helper for TestFailingTestStillDisposesCluster")
	}
	kubeharness.New(t)
	t.Fatal("deliberate test failure")
}

func TestFailingTestStillDisposesCluster(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperFailingTest$", "-test.v")
	cmd.Env = append(os.Environ(), "KUBEHARNESS_E2E_HELPER=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err == nil {
		t.Fatalf("helper test passed, want failure:\n%s", out.String())
	}
	m := regexp.MustCompile(`kubeharness cluster=(\S+) phase=ready cause=test-failed cleanup=succeeded remaining=0`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no report line with test-failed and succeeded cleanup:\n%s", out.String())
	}
	if left := nodeContainers(t, m[1]); len(left) > 0 {
		t.Errorf("cluster %s leaked containers %v", m[1], left)
	}
}
