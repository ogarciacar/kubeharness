package kube

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func deployment(gen, observed int64, want int32, status appsv1.DeploymentStatus) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "d", Namespace: "default", Generation: gen},
		Spec: appsv1.DeploymentSpec{Replicas: &want,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "d"}}},
		Status: func() appsv1.DeploymentStatus { status.ObservedGeneration = observed; return status }(),
	}
}

func TestRolloutStateFollowsKubectlRolloutStatus(t *testing.T) {
	deadline := appsv1.DeploymentCondition{Type: appsv1.DeploymentProgressing, Reason: "ProgressDeadlineExceeded", Message: "took too long"}
	cases := []struct {
		name string
		d    *appsv1.Deployment
		want Progress
		says string
	}{
		{"spec not observed", deployment(2, 1, 1, appsv1.DeploymentStatus{UpdatedReplicas: 1, Replicas: 1, AvailableReplicas: 1}), InProgress, "not observed"},
		{"replicas not updated", deployment(1, 1, 2, appsv1.DeploymentStatus{UpdatedReplicas: 1, Replicas: 2, AvailableReplicas: 2}), InProgress, "1 of 2 replicas updated"},
		{"old replica still running", deployment(1, 1, 1, appsv1.DeploymentStatus{UpdatedReplicas: 1, Replicas: 2, AvailableReplicas: 1}), InProgress, "old replicas"},
		{"updated replica not available", deployment(1, 1, 1, appsv1.DeploymentStatus{UpdatedReplicas: 1, Replicas: 1, AvailableReplicas: 0}), InProgress, "0 of 1 updated replicas available"},
		{"available", deployment(1, 1, 1, appsv1.DeploymentStatus{UpdatedReplicas: 1, Replicas: 1, AvailableReplicas: 1}), Done, ""},
		{"progress deadline exceeded", deployment(1, 1, 1, appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{deadline}}), Failed, "took too long"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, says := rolloutState(c.d)
			if got != c.want || !strings.Contains(says, c.says) {
				t.Fatalf("rolloutState = %v %q, want %v containing %q", got, says, c.want, c.says)
			}
		})
	}
}

func job(conds ...batchv1.JobCondition) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "migrate", Namespace: "default"},
		Spec:       batchv1.JobSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"job-name": "migrate"}}},
		Status:     batchv1.JobStatus{Conditions: conds},
	}
}

func TestJobStateReadsTerminalConditions(t *testing.T) {
	complete := batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}
	failed := batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"}
	notYet := batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionFalse}
	for name, c := range map[string]struct {
		j    *batchv1.Job
		want Progress
	}{
		"no conditions":        {job(), InProgress},
		"false condition only": {job(notYet), InProgress},
		"complete":             {job(complete), Done},
		"failed":               {job(failed), Failed},
	} {
		if got := jobState(c.j); got != c.want {
			t.Errorf("%s: jobState = %v, want %v", name, got, c.want)
		}
	}
}

func failedJobPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "migrate-abc", Namespace: "default", Labels: map[string]string{"job-name": "migrate"}},
		Status: corev1.PodStatus{Phase: corev1.PodFailed, ContainerStatuses: []corev1.ContainerStatus{{
			Name:  "migrate",
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 3, Reason: "Error"}},
		}}},
	}
}

func TestFailedJobErrorCarriesConditionsContainerStateAndLogs(t *testing.T) {
	failed := batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded", Message: "Job has reached the specified backoff limit"}
	client := fake.NewClientset(job(failed), failedJobPod())
	err := WaitJob(context.Background(), context.Background(), client, "default", "migrate")
	if err == nil {
		t.Fatal("WaitJob succeeded for a failed Job")
	}
	for _, want := range []string{"job migrate failed", "BackoffLimitExceeded", "pod migrate-abc phase=Failed", "terminated exit=3", "fake logs"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestCompletedJobReturnsNil(t *testing.T) {
	complete := batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}
	if err := WaitJob(context.Background(), context.Background(), fake.NewClientset(job(complete)), "default", "migrate"); err != nil {
		t.Fatalf("WaitJob = %v", err)
	}
}

func TestRunningJobStopsAtItsDeadlineWithEvidence(t *testing.T) {
	running := job()
	running.Status.Active = 1
	client := fake.NewClientset(running, failedJobPod())
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	began := time.Now()
	err := WaitJob(ctx, context.Background(), client, "default", "migrate")
	if took := time.Since(began); err == nil || took > 2*time.Second {
		t.Fatalf("WaitJob took %s: %v", took, err)
	}
	for _, want := range []string{"did not complete", "active=1", "pod migrate-abc", "context deadline exceeded"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestUnavailableDeploymentStopsAtItsDeadlineWithLastObservation(t *testing.T) {
	d := deployment(1, 1, 1, appsv1.DeploymentStatus{UpdatedReplicas: 1, Replicas: 1})
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "d-1", Namespace: "default", Labels: map[string]string{"app": "d"}},
		Status: corev1.PodStatus{Phase: corev1.PodPending, ContainerStatuses: []corev1.ContainerStatus{{
			Name:  "app",
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImageNeverPull", Message: "not present"}},
		}}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := WaitDeployment(ctx, context.Background(), fake.NewClientset(d, pod), "default", "d")
	for _, want := range []string{"not available", "0 of 1 updated replicas available", "ErrImageNeverPull", "context deadline exceeded"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestAvailableDeploymentReturnsNil(t *testing.T) {
	d := deployment(1, 1, 1, appsv1.DeploymentStatus{UpdatedReplicas: 1, Replicas: 1, AvailableReplicas: 1})
	if err := WaitDeployment(context.Background(), context.Background(), fake.NewClientset(d), "default", "d"); err != nil {
		t.Fatalf("WaitDeployment = %v", err)
	}
}

func TestReadManifestsParsesEveryDocumentInFileOrder(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("20-svc.yaml", "apiVersion: v1\nkind: Service\nmetadata: {name: b}\n---\n---\napiVersion: v1\nkind: ConfigMap\nmetadata: {name: c}\n")
	write("10-deploy.yml", "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: a}\n")
	write("README.md", "not a manifest")
	objs, err := ReadManifests(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range objs {
		got = append(got, o.GetKind()+"/"+o.GetName())
	}
	if strings.Join(got, " ") != "Deployment/a Service/b ConfigMap/c" {
		t.Fatalf("objects = %v", got)
	}
}

func TestReadManifestsRejectsAnEmptyDirectoryAndNamelessObjects(t *testing.T) {
	if _, err := ReadManifests(t.TempDir()); err == nil {
		t.Error("empty directory accepted")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "x.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata: {}\n"), 0o600)
	if _, err := ReadManifests(dir); err == nil || !strings.Contains(err.Error(), "metadata.name") {
		t.Errorf("nameless object: %v", err)
	}
}

func object(apiVersion, kind, namespace, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": apiVersion, "kind": kind}}
	u.SetName(name)
	u.SetNamespace(namespace)
	return u
}

func TestOnlyJobWantsExactlyOneJobAndDefaultsItsNamespace(t *testing.T) {
	jobObj, cm := object("batch/v1", "Job", "", "migrate"), object("v1", "ConfigMap", "", "sql")
	want := ObjectRef{GVK: schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}, Namespace: "default", Name: "migrate"}
	if got, err := OnlyJob([]*unstructured.Unstructured{cm, jobObj}); err != nil || got != want {
		t.Errorf("OnlyJob = %v, %v", got, err)
	}
	if _, err := OnlyJob([]*unstructured.Unstructured{cm}); err == nil {
		t.Error("no Job accepted")
	}
	if _, err := OnlyJob([]*unstructured.Unstructured{jobObj, jobObj}); err == nil {
		t.Error("two Jobs accepted")
	}
}

func TestRolloutWithoutReplicasWantsOne(t *testing.T) {
	d := deployment(1, 1, 1, appsv1.DeploymentStatus{})
	d.Spec.Replicas = nil
	if got, says := rolloutState(d); got != InProgress {
		t.Fatalf("rolloutState with nil replicas and none updated = %v %q, want InProgress", got, says)
	}
}

func TestDeploymentEvidenceNamesAReplicaFailure(t *testing.T) {
	d := deployment(1, 1, 1, appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{{
		Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue, Reason: "FailedCreate",
		Message: `pods "d-1" is forbidden: serviceaccount "missing-sa" not found`,
	}}})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := WaitDeployment(ctx, context.Background(), fake.NewClientset(d), "default", "d")
	if err == nil || !strings.Contains(err.Error(), `serviceaccount "missing-sa" not found`) {
		t.Fatalf("evidence lacks the ReplicaFailure cause:\n%v", err)
	}
}

func TestProgressDeadlineFailureIncludesPodEvidence(t *testing.T) {
	d := deployment(1, 1, 1, appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{{
		Type: appsv1.DeploymentProgressing, Reason: "ProgressDeadlineExceeded", Message: "too slow"}}})
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "d-1", Namespace: "default", Labels: map[string]string{"app": "d"}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "app",
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}}}},
	}
	err := WaitDeployment(context.Background(), context.Background(), fake.NewClientset(d, pod), "default", "d")
	if err == nil || !strings.Contains(err.Error(), "ImagePullBackOff") {
		t.Fatalf("progress-deadline failure lacks pod evidence:\n%v", err)
	}
}

func TestMissingDeploymentIsReportedAsNotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := WaitDeployment(ctx, context.Background(), fake.NewClientset(), "default", "typo")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing deployment: %v", err)
	}
}

func TestADeadlineDoesNotCutTheEvidence(t *testing.T) {
	d := deployment(1, 1, 1, appsv1.DeploymentStatus{UpdatedReplicas: 1, Replicas: 1})
	d.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}
	j := job()
	j.TypeMeta = metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"}
	pods := &corev1.PodList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"}, Items: []corev1.Pod{{
		ObjectMeta: metav1.ObjectMeta{Name: "p-1", Namespace: "default"},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "app",
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull"}}}}},
	}}}
	_, client := stubAPI(t, map[string]runtime.Object{"/deployments/d": d, "/jobs/migrate": j, "/pods": pods})
	for name, wait := range map[string]func(ctx, evidence context.Context) error{
		"deployment": func(ctx, ev context.Context) error { return WaitDeployment(ctx, ev, client, "default", "d") },
		"job":        func(ctx, ev context.Context) error { return WaitJob(ctx, ev, client, "default", "migrate") },
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		err := wait(ctx, context.Background())
		cancel()
		if err == nil || !strings.Contains(err.Error(), "ErrImagePull") || !strings.Contains(err.Error(), "stub log line") {
			t.Errorf("%s: evidence cut by the operation deadline:\n%v", name, err)
		}
	}
}

func TestOnlyJobIgnoresJobsFromOtherGroups(t *testing.T) {
	other := object("example.io/v1", "Job", "", "x")
	if _, err := OnlyJob([]*unstructured.Unstructured{other}); err == nil {
		t.Fatal("a Job from another API group counted as a batch Job")
	}
}

func TestReadManifestsSkipsCommentOnlyDocumentsAndExpandsLists(t *testing.T) {
	dir := t.TempDir()
	body := "# web tier\n---\napiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\n---\n# nothing here\n---\n" +
		"apiVersion: v1\nkind: List\nitems:\n- apiVersion: v1\n  kind: ConfigMap\n  metadata: {name: b}\n- apiVersion: v1\n  kind: Secret\n  metadata: {name: c}\n"
	if err := os.WriteFile(filepath.Join(dir, "m.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	objs, err := ReadManifests(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range objs {
		got = append(got, o.GetKind()+"/"+o.GetName())
	}
	if strings.Join(got, " ") != "ConfigMap/a ConfigMap/b Secret/c" {
		t.Fatalf("objects = %v", got)
	}
}

func TestJobDeletedAfterBeingSeenFailsFastAndSaysSo(t *testing.T) {
	running := job()
	running.Status.Active = 1
	client := fake.NewClientset(running)
	go func() {
		time.Sleep(700 * time.Millisecond)
		_ = client.BatchV1().Jobs("default").Delete(context.Background(), "migrate", metav1.DeleteOptions{})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	began := time.Now()
	err := WaitJob(ctx, context.Background(), client, "default", "migrate")
	if took := time.Since(began); err == nil || took > 3*time.Second || !strings.Contains(err.Error(), "deleted") {
		t.Fatalf("WaitJob after the Job vanished: took %s, err %v", took, err)
	}
}

func TestResetJobDeletesAnEarlierRunAndWaitsUntilItIsGone(t *testing.T) {
	complete := batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}
	client := fake.NewClientset(job(complete))
	var propagation metav1.DeletionPropagation
	client.PrependReactor("delete", "jobs", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if opts := a.(k8stesting.DeleteActionImpl).DeleteOptions; opts.PropagationPolicy != nil {
			propagation = *opts.PropagationPolicy
		}
		return false, nil, nil
	})
	if err := ResetJob(context.Background(), client, "default", "migrate"); err != nil {
		t.Fatal(err)
	}
	if propagation != metav1.DeletePropagationForeground {
		t.Fatalf("delete propagation = %q, want Foreground so the old run's Pods go before the Job", propagation)
	}
	if _, err := client.BatchV1().Jobs("default").Get(context.Background(), "migrate", metav1.GetOptions{}); err == nil {
		t.Fatal("earlier Job still present, so RunJob would read its old Complete condition")
	}
	if err := ResetJob(context.Background(), client, "default", "never-ran"); err != nil {
		t.Fatalf("resetting a Job that does not exist: %v", err)
	}
}

func TestPodEvidenceCoversInitContainersRestartsSchedulingAndEvents(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "migrate-abc", Namespace: "default", Labels: map[string]string{"job-name": "migrate"}},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable",
				Message: "0/1 nodes are available: 1 Insufficient memory."}},
			InitContainerStatuses: []corev1.ContainerStatus{{Name: "wait-for-db",
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 7, Reason: "Error"}}}},
			ContainerStatuses: []corev1.ContainerStatus{{Name: "migrate", RestartCount: 2,
				State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
				LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error"}}}},
		},
	}
	event := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "migrate.1", Namespace: "default"},
		InvolvedObject: corev1.ObjectReference{Kind: "Job", Name: "migrate", Namespace: "default"},
		Type:           corev1.EventTypeWarning, Reason: "FailedCreate",
		Message: `pods "migrate-" is forbidden: serviceaccount "api-runner" not found`,
	}
	failed := batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"}
	err := WaitJob(context.Background(), context.Background(), fake.NewClientset(job(failed), pod, event), "default", "migrate")
	for _, want := range []string{
		"init container wait-for-db terminated exit=7",
		"restarts=2", "last terminated exit=1", "previous logs:",
		"Unschedulable: 0/1 nodes are available",
		`FailedCreate: pods "migrate-" is forbidden`,
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("evidence lacks %q:\n%v", want, err)
		}
	}
}

func TestPodEvidenceIncludesThePodsWarningEvents(t *testing.T) {
	d := deployment(1, 1, 1, appsv1.DeploymentStatus{UpdatedReplicas: 1, Replicas: 1})
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "d-1", Namespace: "default", Labels: map[string]string{"app": "d"}},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}
	event := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "d-1.1", Namespace: "default"},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "d-1", Namespace: "default"},
		Type:           corev1.EventTypeWarning, Reason: "FailedMount",
		Message: `MountVolume.SetUp failed for volume "config" : configmap "app-config-missing" not found`,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := WaitDeployment(ctx, context.Background(), fake.NewClientset(d, pod, event), "default", "d")
	if err == nil || !strings.Contains(err.Error(), `configmap "app-config-missing" not found`) {
		t.Fatalf("evidence lacks the Pod's FailedMount event:\n%v", err)
	}
}

func TestTailKeepsTheEndOfALongStream(t *testing.T) {
	long := strings.Repeat(strings.Repeat("x", 50_000)+"\n", 30) + "FATAL: the real cause\n"
	got := string(tail(strings.NewReader(long), logTailBytes))
	if len(got) != logTailBytes || !strings.HasSuffix(got, "FATAL: the real cause\n") {
		t.Fatalf("tail kept %d bytes ending %q", len(got), got[len(got)-25:])
	}
}

func TestDeploymentEvidenceDescribesRolloutAndPods(t *testing.T) {
	d := deployment(1, 1, 1, appsv1.DeploymentStatus{UpdatedReplicas: 1, Replicas: 1})
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "d-1", Namespace: "default", Labels: map[string]string{"app": "d"}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
			Name: "app", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		}}},
	}
	got := DeploymentEvidence(context.Background(), fake.NewClientset(d, pod), "default", "d")
	for _, want := range []string{"deployment default/d: 0 of 1 updated replicas available", "pod d-1 phase=Running", "container app running", "fake logs"} {
		if !strings.Contains(got, want) {
			t.Errorf("evidence lacks %q:\n%s", want, got)
		}
	}
	if got := DeploymentEvidence(context.Background(), fake.NewClientset(), "default", "typo"); !strings.Contains(got, "deployment default/typo: ") || !strings.Contains(got, "not found") {
		t.Errorf("missing Deployment evidence = %q", got)
	}
}
