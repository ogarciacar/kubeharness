package kubeharness

import (
	"bytes"
	"fmt"
	"strings"

	"sigs.k8s.io/kind/pkg/apis/config/v1alpha4"
	"sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/exec"
	"sigs.k8s.io/kind/pkg/log"
)

type kindProvider struct{ p *cluster.Provider }

func newKindProvider() kindProvider {
	return kindProvider{p: cluster.NewProvider(cluster.ProviderWithLogger(log.NoopLogger{}))}
}

func (k kindProvider) Create(name, kubeconfigPath string) error {
	cfg := &v1alpha4.Cluster{}
	// -1 lets the container runtime bind the API server port atomically.
	// kind's default (0) probes a free port, releases it, then reuses it,
	// which races with parallel creates.
	cfg.Networking.APIServerPort = -1
	err := k.p.Create(name,
		cluster.CreateWithV1Alpha4Config(cfg),
		cluster.CreateWithKubeconfigPath(kubeconfigPath),
		cluster.CreateWithDisplayUsage(false),
		cluster.CreateWithDisplaySalutation(false),
	)
	return kindCreateErr(kindErr(err))
}

func kindCreateErr(err error) error {
	if err != nil && strings.Contains(err.Error(), "already exist for a cluster with the name") {
		return fmt.Errorf("%w: %v", errNameTaken, err)
	}
	return err
}

func (k kindProvider) Delete(name, kubeconfigPath string) error {
	return kindErr(k.p.Delete(name, kubeconfigPath))
}

func (k kindProvider) List() ([]string, error) {
	names, err := k.p.List()
	return names, kindErr(err)
}

// kindErr appends the command output kind captured, because RunError.Error
// omits it and Docker's reason lives there.
func kindErr(err error) error {
	if re := exec.RunErrorForError(err); re != nil {
		if out := bytes.TrimSpace(re.Output); len(out) > 0 {
			return fmt.Errorf("%w: %s", err, out)
		}
	}
	return err
}
