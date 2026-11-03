package kindcluster

import (
	"errors"
	"github.com/ogarciacar/kubeharness/internal/lifecycle"
	"strings"
	"testing"

	"sigs.k8s.io/kind/pkg/exec"
)

func TestKindNameCollisionErrorMapsToErrNameTaken(t *testing.T) {
	kindErr := errors.New(`node(s) already exist for a cluster with the name "kh-x-1"`)
	if !errors.Is(kindCreateErr(kindErr), lifecycle.ErrNameTaken) {
		t.Fatal("kind's collision error is not recognized, so cleanup would delete a cluster this test does not own")
	}
	if errors.Is(kindCreateErr(errors.New("failed to pull image")), lifecycle.ErrNameTaken) {
		t.Fatal("an unrelated create error was treated as a name collision")
	}
}

func TestKindErrorsKeepDockersReason(t *testing.T) {
	err := kindErr(&exec.RunError{
		Command: []string{"docker", "rm", "-f", "kh-x-1-control-plane"},
		Output:  []byte("Error response from daemon: permission denied\n"),
		Inner:   errors.New("exit status 1"),
	})
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("docker's reason lost: %v", err)
	}
	if kindErr(nil) != nil {
		t.Fatal("nil error wrapped")
	}
}

func TestRemedyRemovesExactlyTheClusterNodesAndTheirVolumes(t *testing.T) {
	ps := "docker ps -aq --filter label=io.x-k8s.kind.cluster=kh-abc-1"
	want := ps + " >/dev/null && for c in $(" + ps + "); do docker rm -f -v \"$c\"; done"
	if got := Remedy("kh-abc-1"); got != want {
		t.Fatalf("remedy = %q, want %q", got, want)
	}
}
