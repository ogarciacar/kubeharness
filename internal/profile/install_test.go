package profile

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func gatewayClass(generation, observed int64) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": "GatewayClass",
		"metadata": map[string]any{"name": GatewayClass, "generation": generation},
		"status": map[string]any{"conditions": []any{map[string]any{
			"type": "Accepted", "status": "True", "reason": "Accepted", "observedGeneration": observed, "message": "Valid GatewayClass",
		}}},
	}}
	return u
}

func TestGatewayClassAcceptedForAnEarlierGenerationIsNotAccepted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	stale := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), gatewayClass(2, 1))
	err := waitAccepted(ctx, stale)
	if err == nil || !strings.Contains(err.Error(), "condition Accepted=True reason=Accepted observedGeneration=1 (generation 2)") {
		t.Fatalf("waitAccepted on a stale status = %v", err)
	}

	current := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), gatewayClass(2, 2))
	if err := waitAccepted(context.Background(), current); err != nil {
		t.Fatalf("waitAccepted on a current status = %v", err)
	}
}
