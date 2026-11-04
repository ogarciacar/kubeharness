package kube

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

const (
	pollInterval = 500 * time.Millisecond
	logTailBytes = 4096
	logTailLines = 100
)

// Progress is where a Deployment rollout or a Job stands.
type Progress int

const (
	InProgress Progress = iota
	Done
	Failed
)

// rolloutState applies kubectl rollout status's rules: the controller has seen
// the latest spec, every replica runs it, no old replica remains, and all
// updated replicas are available.
func rolloutState(d *appsv1.Deployment) (Progress, string) {
	if d.Generation > d.Status.ObservedGeneration {
		return InProgress, "controller has not observed the latest spec"
	}
	for _, c := range d.Status.Conditions {
		if c.Type == appsv1.DeploymentProgressing && c.Reason == "ProgressDeadlineExceeded" {
			return Failed, "progress deadline exceeded: " + c.Message
		}
	}
	want := int32(1)
	if d.Spec.Replicas != nil {
		want = *d.Spec.Replicas
	}
	s := d.Status
	switch {
	case s.UpdatedReplicas < want:
		return InProgress, fmt.Sprintf("%d of %d replicas updated", s.UpdatedReplicas, want)
	case s.Replicas > s.UpdatedReplicas:
		return InProgress, fmt.Sprintf("%d old replicas pending termination", s.Replicas-s.UpdatedReplicas)
	case s.AvailableReplicas < s.UpdatedReplicas:
		return InProgress, fmt.Sprintf("%d of %d updated replicas available", s.AvailableReplicas, s.UpdatedReplicas)
	}
	return Done, fmt.Sprintf("%d of %d replicas available", s.AvailableReplicas, want)
}

// jobState reads a Job's terminal conditions.
func jobState(j *batchv1.Job) Progress {
	for _, c := range j.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case batchv1.JobComplete:
			return Done
		case batchv1.JobFailed:
			return Failed
		}
	}
	return InProgress
}

// WaitDeployment polls until the Deployment is rolled out and available.
// When it fails or ctx ends, the error carries the last observation and the
// state of the Deployment's Pods, gathered within evidenceCtx.
func WaitDeployment(ctx, evidenceCtx context.Context, client kubernetes.Interface, namespace, name string) error {
	last := "not observed yet"
	var seen *appsv1.Deployment
	for {
		d, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			seen = d
			var p Progress
			p, last = rolloutState(d)
			switch p {
			case Done:
				return nil
			case Failed:
				return fmt.Errorf("deployment %s failed: %s%s%s", name, last, deploymentConditions(d), podEvidence(evidenceCtx, client, namespace, d.Spec.Selector))
			}
		} else if ctx.Err() == nil {
			last = err.Error()
		}
		select {
		case <-ctx.Done():
			evidence := ""
			if seen != nil {
				evidence = deploymentConditions(seen) + podEvidence(evidenceCtx, client, namespace, seen.Spec.Selector)
			}
			return fmt.Errorf("deployment %s not available: %w; last observed: %s%s", name, ctx.Err(), last, evidence)
		case <-time.After(pollInterval):
		}
	}
}

// WaitJob polls until the Job completes. When the Job fails or ctx ends, the
// error carries the Job's conditions and each Pod's container states and log
// tails, gathered within evidenceCtx.
func WaitJob(ctx, evidenceCtx context.Context, client kubernetes.Interface, namespace, name string) error {
	var job *batchv1.Job
	last := "not observed yet"
	for {
		j, err := client.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if job != nil && apierrors.IsNotFound(err) {
			return fmt.Errorf("job %s was deleted before RunJob saw it finish, as ttlSecondsAfterFinished: 0 does; last observed: %s", name, last)
		}
		if err == nil {
			job = j
			switch jobState(j) {
			case Done:
				return nil
			case Failed:
				return fmt.Errorf("job %s failed%s", name, jobEvidence(evidenceCtx, client, j))
			}
			last = fmt.Sprintf("active=%d succeeded=%d failed=%d", j.Status.Active, j.Status.Succeeded, j.Status.Failed)
		} else if ctx.Err() == nil {
			last = err.Error()
		}
		select {
		case <-ctx.Done():
			evidence := ""
			if job != nil {
				evidence = jobEvidence(evidenceCtx, client, job)
			}
			return fmt.Errorf("job %s did not complete: %w; last observed: %s%s", name, ctx.Err(), last, evidence)
		case <-time.After(pollInterval):
		}
	}
}

func jobEvidence(ctx context.Context, client kubernetes.Interface, j *batchv1.Job) string {
	var b strings.Builder
	for _, c := range j.Status.Conditions {
		fmt.Fprintf(&b, "\n  condition %s=%s reason=%s: %s", c.Type, c.Status, c.Reason, c.Message)
	}
	b.WriteString(warningEvents(ctx, client, j.Namespace, "Job", j.Name))
	b.WriteString(podEvidence(ctx, client, j.Namespace, j.Spec.Selector))
	return b.String()
}

// warningEvents lists the warning events of one object, such as a Job whose
// Pods could not be created at all.
func warningEvents(ctx context.Context, client kubernetes.Interface, namespace, kind, name string) string {
	events, err := client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.kind=" + kind + ",involvedObject.name=" + name,
	})
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range events.Items {
		if e.Type == corev1.EventTypeWarning && e.InvolvedObject.Kind == kind && e.InvolvedObject.Name == name {
			fmt.Fprintf(&b, "\n  event %s: %s", e.Reason, e.Message)
		}
	}
	return b.String()
}

// podEvidence describes the Pods matching sel: phase, each container's state,
// and the tail of its logs.
func podEvidence(ctx context.Context, client kubernetes.Interface, namespace string, sel *metav1.LabelSelector) string {
	if sel == nil {
		return ""
	}
	selector, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return "\n  pods: bad selector: " + err.Error()
	}
	return podsEvidence(ctx, client, namespace, selector)
}

func podsEvidence(ctx context.Context, client kubernetes.Interface, namespace string, selector labels.Selector) string {
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return "\n  pods: " + err.Error()
	}
	if len(pods.Items) == 0 {
		return "\n  pods: none"
	}
	var b strings.Builder
	for _, p := range pods.Items {
		fmt.Fprintf(&b, "\n  pod %s phase=%s", p.Name, p.Status.Phase)
		b.WriteString(indent(strings.TrimPrefix(warningEvents(ctx, client, namespace, "Pod", p.Name), "\n"), "  "))
		for _, c := range p.Status.Conditions {
			if c.Status == corev1.ConditionFalse && c.Message != "" {
				fmt.Fprintf(&b, "\n    condition %s=False %s: %s", c.Type, c.Reason, c.Message)
			}
		}
		for _, cs := range p.Status.InitContainerStatuses {
			b.WriteString(containerEvidence(ctx, client, namespace, p.Name, "init container", cs))
		}
		for _, cs := range p.Status.ContainerStatuses {
			b.WriteString(containerEvidence(ctx, client, namespace, p.Name, "container", cs))
		}
	}
	return b.String()
}

// containerEvidence describes one container. For a restarted container it
// adds the previous instance's exit and logs, where a crash usually shows.
func containerEvidence(ctx context.Context, client kubernetes.Interface, namespace, pod, label string, cs corev1.ContainerStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n    %s %s %s", label, cs.Name, containerState(cs.State))
	if cs.RestartCount > 0 {
		fmt.Fprintf(&b, " restarts=%d", cs.RestartCount)
		if cs.LastTerminationState.Terminated != nil {
			fmt.Fprintf(&b, " last %s", containerState(cs.LastTerminationState))
		}
		if logs := logTail(ctx, client, namespace, pod, cs.Name, true); logs != "" {
			fmt.Fprintf(&b, "\n    previous logs:\n%s", indent(logs, "      "))
		}
	}
	if logs := logTail(ctx, client, namespace, pod, cs.Name, false); logs != "" {
		fmt.Fprintf(&b, "\n    logs:\n%s", indent(logs, "      "))
	}
	return b.String()
}

func containerState(s corev1.ContainerState) string {
	switch {
	case s.Waiting != nil:
		return fmt.Sprintf("waiting reason=%s: %s", s.Waiting.Reason, s.Waiting.Message)
	case s.Terminated != nil:
		return fmt.Sprintf("terminated exit=%d reason=%s: %s", s.Terminated.ExitCode, s.Terminated.Reason, s.Terminated.Message)
	case s.Running != nil:
		return "running"
	}
	return "unknown"
}

func logTail(ctx context.Context, client kubernetes.Interface, namespace, pod, container string, previous bool) string {
	stream, err := client.CoreV1().Pods(namespace).GetLogs(pod, &corev1.PodLogOptions{Container: container, Previous: previous, TailLines: ptr(int64(logTailLines))}).Stream(ctx)
	if err != nil {
		return ""
	}
	defer stream.Close()
	return string(bytes.TrimRight(tail(stream, logTailBytes), "\n"))
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

// deploymentConditions lists conditions that explain a stuck rollout, such as
// a ReplicaFailure when Pods cannot be created at all.
func deploymentConditions(d *appsv1.Deployment) string {
	var b strings.Builder
	for _, c := range d.Status.Conditions {
		healthy := c.Status == corev1.ConditionTrue && c.Type != appsv1.DeploymentReplicaFailure
		if c.Message == "" || healthy {
			continue
		}
		fmt.Fprintf(&b, "\n  condition %s=%s reason=%s: %s", c.Type, c.Status, c.Reason, c.Message)
	}
	return b.String()
}

// ResetJob deletes an earlier run of the Job, with its Pods, and waits until
// it is gone. Without it a second RunJob of the same manifests would apply a
// no-op and read the earlier run's Complete condition.
func ResetJob(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	jobs := client.BatchV1().Jobs(namespace)
	foreground := metav1.DeletePropagationForeground
	if err := jobs.Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &foreground}); apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("delete earlier run of job %s: %w", name, err)
	}
	for {
		if _, err := jobs.Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("earlier run of job %s still being deleted: %w", name, ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

// tail reads r to the end and keeps its last n bytes, so a long log still
// shows the line a failing process printed last.
func tail(r io.Reader, n int) []byte {
	buf := make([]byte, 0, 2*n)
	chunk := make([]byte, 32*1024)
	for {
		k, err := r.Read(chunk)
		buf = append(buf, chunk[:k]...)
		if len(buf) > n {
			buf = append(buf[:0], buf[len(buf)-n:]...)
		}
		if err != nil {
			return buf
		}
	}
}
