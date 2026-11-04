package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

func echoService() *corev1.Service {
	return &corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{Name: "echo", Namespace: "default"},
		Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "echo"},
			Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt32(8080)}}},
	}
}

func echoPod(name string, ready corev1.ConditionStatus) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: map[string]string{"app": "echo"}},
		Status:     corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: ready}}},
	}
}

func TestForwardStopsAtItsDeadlineWhenThePortForwardHangs(t *testing.T) {
	pods := &corev1.PodList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"}, Items: []corev1.Pod{echoPod("echo-1", corev1.ConditionTrue)}}
	cfg, client := stubAPI(t, map[string]runtime.Object{"/services/echo": echoService(), "/pods": pods, "/portforward": nil})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	began := time.Now()
	_, err := StartForward(ctx, cfg, client, "default", "echo", 80)
	if took := time.Since(began); err == nil || took > 2*time.Second {
		t.Fatalf("StartForward took %s after a 300ms deadline: %v", took, err)
	}
}

func TestForwardTargetsAReadyPodAndTheMappedContainerPort(t *testing.T) {
	notReady, ready := echoPod("echo-a", corev1.ConditionFalse), echoPod("echo-b", corev1.ConditionTrue)
	client := fake.NewClientset(echoService(), &notReady, &ready)
	pod, port, err := forwardTarget(context.Background(), client, "default", "echo", 80)
	if err != nil || pod != "echo-b" || port != 8080 {
		t.Fatalf("forwardTarget = %s %d %v, want echo-b 8080", pod, port, err)
	}
	terminating := echoPod("echo-0", corev1.ConditionTrue)
	terminating.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	terminating.Finalizers = []string{"e2e/hold"}
	if pod, _, _ := forwardTarget(context.Background(), fake.NewClientset(echoService(), &terminating, &ready), "default", "echo", 80); pod != "echo-b" {
		t.Fatalf("forwardTarget picked %s, want the ready Pod that is not terminating", pod)
	}
	only := echoPod("echo-c", corev1.ConditionFalse)
	if _, _, err := forwardTarget(context.Background(), fake.NewClientset(echoService(), &only), "default", "echo", 80); err == nil || !strings.Contains(err.Error(), "no ready pod") {
		t.Fatalf("no ready pod: %v", err)
	}
}

func TestCloseGivesUpOnAForwarderThatNeverStops(t *testing.T) {
	f := &Forwarding{Addr: "127.0.0.1:1", cancel: func() {}, done: make(chan error)}
	began := time.Now()
	err := f.Close()
	if took := time.Since(began); err == nil || took > closeTimeout+time.Second {
		t.Fatalf("Close took %s: %v", took, err)
	}
	if again := f.Close(); again == nil || again.Error() != err.Error() {
		t.Fatalf("second Close = %v, want the same error", again)
	}
}

func TestNamedTargetPortResolvesOnANativeSidecar(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	p := echoPod("side-1", corev1.ConditionTrue)
	p.Spec.InitContainers = []corev1.Container{{Name: "side", RestartPolicy: &always, Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}}}}
	port, err := containerPort(p, corev1.ServicePort{Port: 80, TargetPort: intstr.FromString("http")})
	if err != nil || port != 8080 {
		t.Fatalf("containerPort = %d, %v, want 8080 from the sidecar", port, err)
	}
}
