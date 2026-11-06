package kube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/streaming/pkg/httpstream"
)

// Forwarding is a live port-forward from 127.0.0.1 to a Pod behind a Service.
type Forwarding struct {
	// Addr is the local address of the forwarded port, such as 127.0.0.1:53017.
	Addr   string
	cancel context.CancelFunc
	done   chan error
	// exited is closed when ForwardPorts returns, for any reason.
	exited    chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// StartForward forwards a free loopback port to the given Service port. The
// forwarder keeps its listener open until Close, so the port is never released
// and reclaimed by someone else in between.
func StartForward(ctx context.Context, cfg *rest.Config, client kubernetes.Interface, namespace, service string, port int32) (*Forwarding, error) {
	pod, target, err := forwardTarget(ctx, client, namespace, service, port)
	if err != nil {
		return nil, err
	}
	url := client.CoreV1().RESTClient().Post().Resource("pods").Namespace(namespace).Name(pod).SubResource("portforward").URL()
	websocket, err := portforward.NewSPDYOverWebsocketDialer(url, cfg)
	if err != nil {
		return nil, fmt.Errorf("websocket dialer: %w", err)
	}
	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("spdy dialer: %w", err)
	}
	dialer := portforward.NewFallbackDialer(websocket, spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, url),
		func(err error) bool { return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err) })

	fwdCtx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	pf, err := portforward.NewOnAddressesWithContext(fwdCtx, dialer, []string{"127.0.0.1"}, []string{fmt.Sprintf("0:%d", target)}, ready, io.Discard, io.Discard)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("port-forward to pod %s: %w", pod, err)
	}
	f := &Forwarding{cancel: cancel, done: make(chan error, 1), exited: make(chan struct{})}
	go func() {
		err := pf.ForwardPorts()
		f.done <- err
		close(f.exited)
	}()
	select {
	case <-ready:
	case err := <-f.done:
		cancel()
		return nil, fmt.Errorf("port-forward to pod %s: %w", pod, err)
	case <-ctx.Done():
		// client-go's port-forward dial takes no context, so a stalled
		// upgrade would hold Close forever. Cancel and leave the goroutine.
		cancel()
		return nil, fmt.Errorf("port-forward to pod %s not ready: %w", pod, ctx.Err())
	}
	ports, err := pf.GetPorts()
	if err != nil || len(ports) != 1 {
		_ = f.Close()
		return nil, fmt.Errorf("port-forward to pod %s: local port unknown: %v", pod, err)
	}
	f.Addr = fmt.Sprintf("127.0.0.1:%d", ports[0].Local)
	return f, nil
}

const closeTimeout = 5 * time.Second

// Close stops the forward and waits up to closeTimeout until its listener is
// closed. It is safe to call more than once.
func (f *Forwarding) Close() error {
	f.closeOnce.Do(func() {
		f.cancel()
		select {
		case err := <-f.done:
			f.closeErr = err
		case <-time.After(closeTimeout):
			f.closeErr = fmt.Errorf("port-forward to %s did not stop within %s", f.Addr, closeTimeout)
		}
	})
	return f.closeErr
}

// Stopped reports whether the forward has ended, without blocking. Close ends
// it, and so does client-go giving up on the Pod. A stopped forward's listener
// refuses connections, so a dialer replaces it before dialing.
func (f *Forwarding) Stopped() bool {
	select {
	case <-f.exited:
		return true
	default:
		return false
	}
}

// forwardTarget picks a ready Pod behind the Service and the container port
// that the Service port maps to.
func forwardTarget(ctx context.Context, client kubernetes.Interface, namespace, service string, port int32) (string, int32, error) {
	svc, err := client.CoreV1().Services(namespace).Get(ctx, service, metav1.GetOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("get service %s: %w", service, err)
	}
	var sp *corev1.ServicePort
	for i := range svc.Spec.Ports {
		if svc.Spec.Ports[i].Port == port {
			sp = &svc.Spec.Ports[i]
		}
	}
	if sp == nil {
		return "", 0, fmt.Errorf("service %s has no port %d", service, port)
	}
	if len(svc.Spec.Selector) == 0 {
		return "", 0, fmt.Errorf("service %s has no selector", service)
	}
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(svc.Spec.Selector).String()})
	if err != nil {
		return "", 0, fmt.Errorf("list pods for service %s: %w", service, err)
	}
	for _, p := range pods.Items {
		if p.DeletionTimestamp != nil || !podReady(p) {
			continue
		}
		target, err := containerPort(p, *sp)
		if err != nil {
			return "", 0, fmt.Errorf("service %s: %w", service, err)
		}
		return p.Name, target, nil
	}
	return "", 0, errors.New("service " + service + " has no ready pod")
}

func podReady(p corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func containerPort(p corev1.Pod, sp corev1.ServicePort) (int32, error) {
	if sp.TargetPort.IntVal != 0 {
		return sp.TargetPort.IntVal, nil
	}
	if sp.TargetPort.StrVal == "" {
		return sp.Port, nil
	}
	for _, c := range append(p.Spec.InitContainers, p.Spec.Containers...) {
		for _, cp := range c.Ports {
			if cp.Name == sp.TargetPort.StrVal {
				return cp.ContainerPort, nil
			}
		}
	}
	return 0, fmt.Errorf("pod %s has no container port named %q", p.Name, sp.TargetPort.StrVal)
}
