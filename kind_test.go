package kubeharness

import (
	"errors"
	"strings"
	"testing"

	"sigs.k8s.io/kind/pkg/exec"
)

func TestKindNameCollisionErrorMapsToErrNameTaken(t *testing.T) {
	kindErr := errors.New(`node(s) already exist for a cluster with the name "kh-x-1"`)
	if !errors.Is(kindCreateErr(kindErr), errNameTaken) {
		t.Fatal("kind's collision error is not recognized, so cleanup would delete a cluster this test does not own")
	}
	if errors.Is(kindCreateErr(errors.New("failed to pull image")), errNameTaken) {
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
