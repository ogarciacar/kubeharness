// Package kindcluster adapts kind's Go library to the lifecycle provider.
package kindcluster

import (
	"bytes"
	"fmt"
	"strings"

	"sigs.k8s.io/kind/pkg/apis/config/v1alpha4"
	"sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/exec"
	"sigs.k8s.io/kind/pkg/log"

	"github.com/ogarciacar/kubeharness/internal/lifecycle"
)

type Provider struct{ p *cluster.Provider }

// New returns a provider that drives kind with Docker and logs nothing.
func New() Provider {
	return Provider{p: cluster.NewProvider(cluster.ProviderWithLogger(log.NoopLogger{}))}
}

func (k Provider) Create(name, kubeconfigPath string) error {
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
		return fmt.Errorf("%w: %v", lifecycle.ErrNameTaken, err)
	}
	return err
}

func (k Provider) Delete(name, kubeconfigPath string) error {
	return kindErr(k.p.Delete(name, kubeconfigPath))
}

func (k Provider) List() ([]string, error) {
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

// Remedy removes a cluster's node containers with Docker alone, because the
// kind CLI is not a prerequisite.
func Remedy(name string) string {
	ps := "docker ps -aq --filter label=io.x-k8s.kind.cluster=" + name
	return fmt.Sprintf(`%s >/dev/null && for c in $(%s); do docker rm -f -v "$c"; done`, ps, ps)
}
