package lifecycle

import (
	"os/exec"
	"strings"
	"testing"
)

func TestLifecycleImportsNoClusterLibraries(t *testing.T) {
	for _, tags := range []string{"", "e2e"} {
		out, err := exec.Command("go", "list", "-tags", tags, "-test", "-deps", "-f", "{{.ImportPath}}", ".", "./lifecycletest").Output()
		if err != nil {
			t.Fatalf("go list -tags %q: %v", tags, err)
		}
		for _, dep := range strings.Fields(string(out)) {
			if strings.HasPrefix(dep, "k8s.io/") || strings.HasPrefix(dep, "sigs.k8s.io/") {
				t.Errorf("lifecycle depends on %s with tags %q; keep kind and client-go in kindcluster and kube", dep, tags)
			}
		}
	}
}
