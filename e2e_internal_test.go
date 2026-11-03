//go:build e2e

package kubeharness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/kind/pkg/apis/config/defaults"
)

// A node that stops responding mid-create makes kind's Create block with no
// timeout. Deleting the paused node usually unblocks Create, so both outcomes
// are legitimate. The node image is pulled first, because kind pulls it inside
// Create and a cold pull would spend the create budget before any node exists.
func TestHungCreateOnRealKindIsBoundedAndNotReportedClean(t *testing.T) {
	if out, err := exec.Command("docker", "pull", defaults.Image).CombinedOutput(); err != nil {
		t.Fatalf("pull node image: %v\n%s", err, out)
	}
	to := defaultTimeouts
	to.create = 20 * time.Second
	e := newEnv(newKindProvider(), waitUsable, filepath.Join(t.TempDir(), "kubeconfig"), to)
	t.Cleanup(func() { e.close(CauseOK) })
	node := string(e.name) + "-control-plane"

	setupDone := make(chan struct{})
	paused := make(chan bool, 1)
	go func() {
		for {
			select {
			case <-setupDone:
				paused <- false
				return
			case <-time.After(100 * time.Millisecond):
			}
			out, _ := exec.Command("docker", "ps", "-q", "--filter", "name="+node).Output()
			if strings.TrimSpace(string(out)) != "" {
				time.Sleep(3 * time.Second)
				paused <- exec.Command("docker", "pause", node).Run() == nil
				return
			}
		}
	}()

	began := time.Now()
	err := e.start(context.Background())
	took := time.Since(began)
	close(setupDone)
	if !<-paused {
		t.Fatalf("the node never got paused during create, so this run proves nothing (start err: %v)", err)
	}
	if err == nil {
		t.Fatal("create succeeded although the node was paused")
	}
	if took > to.create+2*time.Second {
		t.Fatalf("start took %s, want about %s", took, to.create)
	}
	r := e.close(CauseOK)
	t.Log(r.String())
	if r.Cause != CauseDeadline || r.Phase != PhaseProvisioning {
		t.Fatalf("report = %s", r)
	}
	if r.Cleanup == CleanupSucceeded && e.createInFlight() {
		t.Fatalf("cleanup claimed success while create is still in flight: %s", r)
	}
	if r.Cleanup != CleanupSucceeded && r.Cleanup != CleanupUncertain {
		t.Fatalf("cleanup = %s, want succeeded (create returned) or uncertain (still in flight)", r.Cleanup)
	}
	out, _ := exec.Command("docker", "ps", "-a", "--filter", "label=io.x-k8s.kind.cluster="+string(e.name), "--format", "{{.Names}}").Output()
	if left := strings.Fields(string(out)); len(left) > 0 {
		t.Errorf("node containers still present after cleanup: %v", left)
	}
}

func TestNameCollisionOnRealKindLeavesTheOtherClusterAlone(t *testing.T) {
	kp := newKindProvider()
	other := newClusterName()
	otherKubeconfig := filepath.Join(t.TempDir(), "other-kubeconfig")
	if err := kp.Create(string(other), otherKubeconfig); err != nil {
		t.Fatalf("create the other cluster: %v", err)
	}
	t.Cleanup(func() { _ = kp.Delete(string(other), otherKubeconfig) })

	e := newEnv(kp, waitUsable, filepath.Join(t.TempDir(), "kubeconfig"), defaultTimeouts)
	e.name, e.report.Cluster = other, string(other)
	if err := e.start(context.Background()); err == nil {
		t.Fatal("create succeeded on a name that is already taken")
	}
	r := e.close(CauseOK)
	names, err := kp.List()
	if err != nil {
		t.Fatal(err)
	}
	if r.Cleanup != CleanupNotNeeded || !slices.Contains(names, string(other)) {
		t.Fatalf("collision path touched a cluster it does not own: report=%s present=%v", r, slices.Contains(names, string(other)))
	}
}

func TestDeleteNeverTouchesTheDeveloperKubeconfig(t *testing.T) {
	dev := filepath.Join(t.TempDir(), "developer-kubeconfig")
	const contents = `apiVersion: v1
kind: Config
clusters: [{name: kind-kh-none-1, cluster: {server: "https://127.0.0.1:6443"}}]
users: [{name: kind-kh-none-1, user: {}}]
contexts: [{name: kind-kh-none-1, context: {cluster: kind-kh-none-1, user: kind-kh-none-1}}]
current-context: kind-kh-none-1
`
	if err := os.WriteFile(dev, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", dev)
	_ = newKindProvider().Delete("kh-none-1", filepath.Join(t.TempDir(), "own-kubeconfig"))
	if after, _ := os.ReadFile(dev); string(after) != contents {
		t.Fatalf("Delete rewrote the developer kubeconfig:\n%s", after)
	}
}

func TestPrintedRemedyRemovesARealCluster(t *testing.T) {
	kp := newKindProvider()
	name := newClusterName()
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	t.Cleanup(func() { _ = kp.Delete(string(name), kubeconfig) })
	if err := kp.Create(string(name), kubeconfig); err != nil {
		t.Fatalf("create: %v", err)
	}
	node := string(name) + "-control-plane"
	vols, err := exec.Command("docker", "inspect", "-f", "{{range .Mounts}}{{if .Name}}{{.Name}} {{end}}{{end}}", node).Output()
	if err != nil || len(strings.Fields(string(vols))) == 0 {
		t.Fatalf("inspect node volumes: %v %q", err, vols)
	}
	if out, err := exec.Command("sh", "-c", remedy(string(name))).CombinedOutput(); err != nil {
		t.Fatalf("remedy failed: %v\n%s", err, out)
	}
	if out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label=io.x-k8s.kind.cluster="+string(name)).Output(); len(strings.TrimSpace(string(out))) > 0 {
		t.Fatalf("remedy left node containers: %s", out)
	}
	for _, v := range strings.Fields(string(vols)) {
		if err := exec.Command("docker", "volume", "inspect", v).Run(); err == nil {
			t.Errorf("remedy left volume %s", v)
		}
	}
}

func TestPrintedRemedySucceedsWhenNothingIsLeft(t *testing.T) {
	for _, shell := range []string{"sh", "bash", "zsh"} {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		if out, err := exec.Command(shell, "-c", remedy("kh-none-1")).CombinedOutput(); err != nil {
			t.Errorf("%s: remedy for a cluster that is already gone failed: %v\n%s", shell, err, out)
		}
	}
}

func TestPrintedRemedyFailsWhenDockerDoesNotAnswer(t *testing.T) {
	cmd := exec.Command("sh", "-c", remedy("kh-none-1"))
	cmd.Env = append(os.Environ(), "DOCKER_HOST=unix:///nonexistent/docker.sock")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("remedy exited 0 although Docker could not be reached:\n%s", out)
	}
}
