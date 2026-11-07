package kube

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

func TestApplyStopsAtItsDeadlineWhenDiscoveryHangs(t *testing.T) {
	cfg, _ := stubAPI(t, map[string]runtime.Object{"/api": nil, "/apis": nil})
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "a"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	began := time.Now()
	_, err := Apply(ctx, cfg, []*unstructured.Unstructured{obj})
	if took := time.Since(began); err == nil || took > 3*time.Second {
		t.Fatalf("Apply took %s past a 300ms deadline: %v", took, err)
	}
}

// The API server answers discovery normally (Widget not served yet) and then
// stops answering 1.5s in. Apply has a 2s limit.
func TestApplyStopsAtItsDeadlineWhenDiscoveryStallsDuringACRDRetry(t *testing.T) {
	start := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if time.Since(start) > 1500*time.Millisecond {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api":
			_, _ = w.Write([]byte(`{"kind":"APIVersions","versions":["v1"]}`))
		case "/apis":
			_, _ = w.Write([]byte(`{"kind":"APIGroupList","apiVersion":"v1","groups":[]}`))
		case "/api/v1":
			_, _ = w.Write([]byte(`{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"configmaps","namespaced":true,"kind":"ConfigMap","verbs":["get","patch"]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	cfg := &rest.Config{Host: srv.URL}
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "e2e.kubeharness.dev/v1", "kind": "Widget", "metadata": map[string]any{"name": "w"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	began := time.Now()
	_, err := Apply(ctx, cfg, []*unstructured.Unstructured{obj})
	took := time.Since(began)
	t.Logf("Apply with a 2s limit returned after %s: %v", took.Round(10*time.Millisecond), err)
	if took > 2500*time.Millisecond {
		t.Errorf("Apply blocked %s past its 2s limit", (took - 2*time.Second).Round(10*time.Millisecond))
	}
}

func TestDeleteVisitsObjectsInReverseFileOrder(t *testing.T) {
	ns := object("v1", "Namespace", "", "shop")
	grant := object("gateway.networking.k8s.io/v1beta1", "ReferenceGrant", "payments", "payments")
	objs := []*unstructured.Unstructured{ns, grant}

	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		{Version: "v1"},
		{Group: "gateway.networking.k8s.io", Version: "v1beta1"},
	})
	m.Add(ns.GroupVersionKind(), meta.RESTScopeRoot)
	m.Add(grant.GroupVersionKind(), meta.RESTScopeNamespace)
	var mapper meta.RESTMapper = m
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), ns.DeepCopy(), grant.DeepCopy())

	known := map[string]*unstructured.Unstructured{ns.GetName(): ns, grant.GetName(): grant}
	var deleted []string
	var propagation []metav1.DeletionPropagation
	client.PrependReactor("delete", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		del := a.(k8stesting.DeleteAction)
		if opts := del.GetDeleteOptions(); opts.PropagationPolicy != nil {
			propagation = append(propagation, *opts.PropagationPolicy)
		} else {
			propagation = append(propagation, "")
		}
		obj := known[del.GetName()]
		deleted = append(deleted, ObjectRef{GVK: obj.GroupVersionKind(), Namespace: a.GetNamespace(), Name: del.GetName()}.String())
		return false, nil, nil
	})

	if err := deleteEach(context.Background(), &rest.Config{}, &mapper, client, objs); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(deleted, " "), "ReferenceGrant/payments/payments Namespace/shop"; got != want {
		t.Fatalf("delete order = %q, want %q", got, want)
	}
	for _, p := range propagation {
		if p != metav1.DeletePropagationBackground {
			t.Errorf("propagation = %q, want Background", p)
		}
	}
	if objs[0] != ns || objs[1] != grant {
		t.Fatal("deleteEach reversed the caller's slice")
	}
}

func TestDeleteTreatsNotFoundAsDone(t *testing.T) {
	keep := object("v1", "ConfigMap", "default", "keep")
	gone := object("v1", "ConfigMap", "default", "gone")
	objs := []*unstructured.Unstructured{keep, gone}

	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Version: "v1"}})
	m.Add(keep.GroupVersionKind(), meta.RESTScopeNamespace)
	var mapper meta.RESTMapper = m
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), keep.DeepCopy())

	var gets []string
	client.PrependReactor("get", "configmaps", func(a k8stesting.Action) (bool, runtime.Object, error) {
		gets = append(gets, a.(k8stesting.GetAction).GetName())
		return false, nil, nil
	})

	if err := deleteEach(context.Background(), &rest.Config{}, &mapper, client, objs); err != nil {
		t.Fatal(err)
	}
	for _, name := range gets {
		if name == "gone" {
			t.Fatalf("GET-polled an object Delete already reported NotFound: %v", gets)
		}
	}
	if err := deleteEach(context.Background(), &rest.Config{}, &mapper, client, objs); err != nil {
		t.Fatalf("second deleteEach = %v", err)
	}
}

func TestDeleteTimeoutNamesFinalizers(t *testing.T) {
	hold := object("v1", "ConfigMap", "default", "hold")
	hold.SetFinalizers([]string{"example.com/hold"})
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Version: "v1"}})
	m.Add(hold.GroupVersionKind(), meta.RESTScopeNamespace)
	var mapper meta.RESTMapper = m
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), hold.DeepCopy())
	client.PrependReactor("delete", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	began := time.Now()
	err := deleteEach(ctx, &rest.Config{}, &mapper, client, []*unstructured.Unstructured{hold})
	if took := time.Since(began); err == nil || took > 2*time.Second {
		t.Fatalf("deleteEach took %s: %v", took, err)
	}
	ref := ObjectRef{GVK: hold.GroupVersionKind(), Namespace: "default", Name: "hold"}
	want := ref.String() + " still present, finalizers [example.com/hold]"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want substring %q", err, want)
	}

	got, gerr := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}).Namespace("default").Get(context.Background(), "hold", metav1.GetOptions{})
	if gerr != nil {
		t.Fatal(gerr)
	}
	if !got.GetDeletionTimestamp().IsZero() {
		t.Fatalf("deletionTimestamp = %v, want unset", got.GetDeletionTimestamp())
	}
}

func TestDeleteSkipsAnUnservedKind(t *testing.T) {
	obj := object("example.com/v1alpha1", "Widget", "default", "w")
	var mapper meta.RESTMapper = meta.NewDefaultRESTMapper(nil)
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	var deletes int
	client.PrependReactor("delete", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		deletes++
		return false, nil, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	began := time.Now()
	err := deleteEach(ctx, &rest.Config{}, &mapper, client, []*unstructured.Unstructured{obj})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took > 400*time.Millisecond {
		t.Fatalf("deleteEach waited %s for a kind it should skip", took)
	}
	if deletes != 0 {
		t.Fatalf("delete calls = %d, want 0", deletes)
	}
}

func TestDeletePollsAnObjectThatIsAlreadyDeleting(t *testing.T) {
	live := object("v1", "ConfigMap", "default", "hold")
	ts := metav1.Now()
	live.SetDeletionTimestamp(&ts)
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Version: "v1"}})
	m.Add(live.GroupVersionKind(), meta.RESTScopeNamespace)
	var mapper meta.RESTMapper = m
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), live)
	var gets int
	client.PrependReactor("delete", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("connection reset")
	})
	client.PrependReactor("get", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		gets++
		if gets == 1 {
			return false, nil, nil
		}
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, "hold")
	})

	manifest := object("v1", "ConfigMap", "default", "hold")
	if err := deleteEach(context.Background(), &rest.Config{}, &mapper, client, []*unstructured.Unstructured{manifest}); err != nil {
		t.Fatal(err)
	}
	if gets < 2 {
		t.Fatalf("gets = %d, want the deletionTimestamp read and then a NotFound poll", gets)
	}
}

func TestDeleteReturnsTheDeleteErrorWhenTheObjectIsStillLive(t *testing.T) {
	live := object("v1", "ConfigMap", "default", "hold")
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Version: "v1"}})
	m.Add(live.GroupVersionKind(), meta.RESTScopeNamespace)
	var mapper meta.RESTMapper = m
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), live.DeepCopy())
	client.PrependReactor("delete", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("connection reset")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	began := time.Now()
	err := deleteEach(ctx, &rest.Config{}, &mapper, client, []*unstructured.Unstructured{live})
	if took := time.Since(began); err == nil || took > 2*time.Second || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("deleteEach took %s: %v", took, err)
	}
}
