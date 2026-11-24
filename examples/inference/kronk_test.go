//go:build e2e

package inference_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/ogarciacar/kubeharness"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const kronkImage = "kubeharness.local/kronk-k01:test"

var buildOnce = sync.OnceValues(func() ([]byte, error) {
	return exec.Command("docker", "build", "-q", "-t", kronkImage, "kronk").CombinedOutput()
})

func loadKronk(t *testing.T, h *kubeharness.Harness) {
	t.Helper()
	if out, err := buildOnce(); err != nil {
		t.Fatalf("docker build %s: %v\n%s", kronkImage, err, out)
	}
	h.LoadImage(kronkImage)
}

type kronk struct {
	metrics string
	scrape  *http.Client
	last    served
}

func watchKronk(t *testing.T, h *kubeharness.Harness) *kronk {
	t.Helper()
	cs, err := kubernetes.NewForConfig(h.RESTConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	d, err := cs.AppsV1().Deployments("default").Get(ctx, "kronk", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Status.Replicas != 1 {
		t.Fatalf("deployment default/kronk has %d replicas, want 1, because the counters come from one Pod", d.Status.Replicas)
	}
	k := &kronk{metrics: "http://" + h.Forward("kronk", 11445) + "/metrics", scrape: &http.Client{Timeout: 3 * time.Second}}
	if k.last, err = k.read(); err != nil {
		t.Fatal(err)
	}
	return k
}

func (k *kronk) read() (served, error) {
	resp, err := k.scrape.Get(k.metrics)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", k.metrics, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseServed(string(b))
}

func (k *kronk) observe(resp *http.Response) (observed, error) {
	now, err := k.read()
	if err != nil {
		return observed{}, err
	}
	o := observed{status: resp.StatusCode, grew: grewSince(k.last, now)}
	k.last = now
	var reply struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil && o.status == http.StatusOK {
		return o, fmt.Errorf("%v: decode reply: %w", o, err)
	}
	o.model = reply.Model
	return o, nil
}

func (k *kronk) await(t *testing.T, gw *kubeharness.Gateway, req *http.Request, judge func(observed) error) observed {
	t.Helper()
	var seen observed
	gw.AwaitResponse(req, func(resp *http.Response) error {
		o, err := k.observe(resp)
		if err != nil {
			return err
		}
		seen = o
		return judge(o)
	})
	return seen
}
