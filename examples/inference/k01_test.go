//go:build e2e

package inference_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ogarciacar/kubeharness"
)

const (
	goodRoute    = "testdata/k01-route/good"
	crossedRoute = "testdata/k01-route/crossed"
)

func chat(t *testing.T, alias string) *http.Request {
	t.Helper()
	body := `{"model":"` + alias + `","messages":[{"role":"user","content":"Say hello in one word."}],"max_tokens":8,"temperature":0}`
	req, err := http.NewRequest(http.MethodPost, "http://llm.example/m/"+alias+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestK01Routing(t *testing.T) {
	h := kubeharness.New(t)
	gc := h.InstallGateway(kubeharness.AgentGateway())
	loadKronk(t, h)
	h.Apply("testdata/k01")
	h.Apply(goodRoute)
	h.WaitReady("kronk")
	k := watchKronk(t, h)
	gw := gc.Gateway("edge")
	gw.AwaitRoute("llm", kubeharness.RouteReady)

	for _, m := range models {
		o := k.await(t, gw, chat(t, m.alias), func(o observed) error { return o.answeredBy(m) })
		t.Logf("/m/%s/ answered as %s", m.alias, o.model)
	}
	k.await(t, gw, chat(t, "unknown"), observed.refused)
	t.Log("/m/unknown/ refused with no model serving")

	h.Apply(crossedRoute)
	gw.AwaitRoute("llm", kubeharness.RouteReady)
	o := k.await(t, gw, chat(t, smol.alias), func(o observed) error { return o.crossed(smol, qwen) })
	t.Logf("/m/smol/ answered as %s and was rejected as smol", o.model)

	h.Apply(goodRoute)
	gw.AwaitRoute("llm", kubeharness.RouteReady)
	o = k.await(t, gw, chat(t, smol.alias), func(o observed) error { return o.answeredBy(smol) })
	t.Logf("/m/smol/ answered as %s again", o.model)
}
