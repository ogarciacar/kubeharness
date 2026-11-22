package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/ogarciacar/kubeharness/internal/trace"
)

func traced(t *testing.T) (context.Context, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	s := trace.Start(&buf, t.Name(), "kh-x").Call(trace.FnWaitReady, "default/d")
	return trace.With(context.Background(), s), &buf
}

func tracedAs(t *testing.T, buf *bytes.Buffer, fields map[string][]string) []string {
	t.Helper()
	var out []string
	for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var l map[string]any
		if err := json.Unmarshal(raw, &l); err != nil {
			t.Fatal(err)
		}
		keys, ok := fields[l["ev"].(string)]
		if !ok {
			continue
		}
		s := l["ev"].(string)
		for _, k := range keys {
			s += fmt.Sprintf(" %s=%v", k, l[k])
		}
		out = append(out, s)
	}
	return out
}

func readyPod(name string, ready bool) *corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: map[string]string{"app": "d"}},
		Status:     corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}}},
	}
}

func TestARolloutTracesItsStateAndThenThePodsBehindIt(t *testing.T) {
	d := deployment(1, 1, 1, appsv1.DeploymentStatus{UpdatedReplicas: 1, Replicas: 1, AvailableReplicas: 1})
	leaving := readyPod("d-old", false)
	leaving.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	leaving.Finalizers = []string{"kubeharness.test/hold"}
	other := readyPod("other", true)
	other.Labels = map[string]string{"app": "other"}
	ctx, buf := traced(t)
	client := fake.NewClientset(d, readyPod("d-1", true), readyPod("d-2", false), leaving, other)
	if err := WaitDeployment(ctx, context.Background(), client, "default", "d"); err != nil {
		t.Fatal(err)
	}
	got := tracedAs(t, buf, map[string][]string{"wait": {"on", "subject", "state", "done"}, "pod": {"api", "owner", "pod", "ready"}})
	want := []string{
		"wait on=rollout subject=default/d state=1 of 1 replicas available done=true",
		"pod api=v1 owner=Deployment/default/d pod=d-1 ready=true",
		"pod api=v1 owner=Deployment/default/d pod=d-2 ready=false",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("trace:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestDeleteTracesEachObjectUntilItIsGone(t *testing.T) {
	live := object("v1", "ConfigMap", "default", "live")
	gone := object("v1", "ConfigMap", "default", "gone")
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Version: "v1"}})
	m.Add(live.GroupVersionKind(), meta.RESTScopeNamespace)
	var mapper meta.RESTMapper = m
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), live.DeepCopy())
	ctx, buf := traced(t)
	if err := deleteEach(ctx, &rest.Config{}, &mapper, client, []*unstructured.Unstructured{live, gone}); err != nil {
		t.Fatal(err)
	}
	got := tracedAs(t, buf, map[string][]string{"object": {"api", "obj", "state"}})
	want := []string{
		"object api=v1 obj=ConfigMap/default/gone state=gone",
		"object api=v1 obj=ConfigMap/default/live state=deleting",
		"object api=v1 obj=ConfigMap/default/live state=gone",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("trace:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestApplyTracesEachObjectWithItsAPIVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api":
			_, _ = w.Write([]byte(`{"kind":"APIVersions","versions":["v1"]}`))
		case "/apis":
			_, _ = w.Write([]byte(`{"kind":"APIGroupList","apiVersion":"v1","groups":[{"name":"gateway.networking.k8s.io","versions":[{"groupVersion":"gateway.networking.k8s.io/v1","version":"v1"}],"preferredVersion":{"groupVersion":"gateway.networking.k8s.io/v1","version":"v1"}}]}`))
		case "/api/v1":
			_, _ = w.Write([]byte(`{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"namespaces","namespaced":false,"kind":"Namespace","verbs":["get","patch"]}]}`))
		case "/apis/gateway.networking.k8s.io/v1":
			_, _ = w.Write([]byte(`{"kind":"APIResourceList","groupVersion":"gateway.networking.k8s.io/v1","resources":[{"name":"httproutes","namespaced":true,"kind":"HTTPRoute","verbs":["get","patch"]}]}`))
		default:
			if r.Method != http.MethodPatch {
				http.NotFound(w, r)
				return
			}
			_, _ = io.Copy(w, r.Body)
		}
	}))
	t.Cleanup(srv.Close)
	objs := []*unstructured.Unstructured{
		object("v1", "Namespace", "", "shop"),
		object("gateway.networking.k8s.io/v1", "HTTPRoute", "shop", "pay"),
	}
	ctx, buf := traced(t)
	if _, err := Apply(ctx, &rest.Config{Host: srv.URL}, objs); err != nil {
		t.Fatal(err)
	}
	got := tracedAs(t, buf, map[string][]string{"object": {"api", "obj", "state"}})
	want := []string{
		"object api=v1 obj=Namespace/shop state=applied",
		"object api=gateway.networking.k8s.io/v1 obj=HTTPRoute/shop/pay state=applied",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("trace:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
