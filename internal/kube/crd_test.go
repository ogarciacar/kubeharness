package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func crd(name string, conds ...map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
		"metadata": map[string]any{"name": name},
	}}
	list := make([]any, len(conds))
	for i, c := range conds {
		list[i] = c
	}
	_ = unstructured.SetNestedSlice(u.Object, list, "status", "conditions")
	return u
}

func TestWaitEstablishedNamesEachCRDThatIsNotEstablished(t *testing.T) {
	established := map[string]any{"type": "Established", "status": "True"}
	conflict := map[string]any{"type": "NamesAccepted", "status": "False", "reason": "ListKindConflict", "message": "kind WidgetList is already in use"}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(),
		crd("good.example.dev", established),
		crd("clash.example.dev", conflict))

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	err := WaitEstablished(ctx, client, []string{"good.example.dev", "clash.example.dev", "absent.example.dev"})
	if err == nil {
		t.Fatal("WaitEstablished returned nil with a CRD that is not established")
	}
	msg := err.Error()
	for _, want := range []string{"2 of 3 CRDs not established", "crd clash.example.dev: NamesAccepted=False reason=ListKindConflict", "crd absent.example.dev:"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "good.example.dev") {
		t.Errorf("error names an established CRD:\n%s", msg)
	}

	if err := WaitEstablished(context.Background(), client, []string{"good.example.dev"}); err != nil {
		t.Fatalf("established CRD: %v", err)
	}
}
