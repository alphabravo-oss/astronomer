package provider

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestOwnedPreparationLogRequiresActualRuntimeWindowAndOneRecord(t *testing.T) {
	request, _ := databaseScanReadFixture(t)
	payload, err := json.Marshal(request.prepared)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)
	finished := started.Add(time.Minute)
	valid := append([]byte(started.Add(time.Second).Format(time.RFC3339Nano)+" "), payload...)
	valid = append(valid, '\n')
	result, err := decodeOwnedPreparationLog(valid, started, finished)
	if err != nil || string(result) != string(payload) {
		t.Fatal("actual bounded record was not preserved")
	}
	clear(result)
	for name, line := range map[string][]byte{
		"before start":     append([]byte(started.Add(-time.Second).Format(time.RFC3339Nano)+" "), payload...),
		"after finish":     append([]byte(finished.Add(time.Second).Format(time.RFC3339Nano)+" "), payload...),
		"offset clock":     append([]byte(started.Add(time.Second).In(time.FixedZone("offset", 3600)).Format(time.RFC3339Nano)+" "), payload...),
		"no timestamp":     payload,
		"second line":      append(append([]byte(nil), valid...), valid...),
		"truncated record": valid[:len(valid)/2],
		"unknown field":    []byte(started.Format(time.RFC3339Nano) + ` {"unknown":true}`),
		"oversized":        []byte(strings.Repeat("x", 16513)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeOwnedPreparationLog(line, started, finished); err == nil {
				t.Fatal("unbounded or unowned preparation log accepted")
			}
		})
	}
}
func TestOwnedPreparationTerminationRequiresEveryInitializerAndJob(t *testing.T) {
	_, peer := databaseScanReadFixture(t)
	pod := peer.pod.DeepCopy()
	job := peer.job.DeepCopy()
	pod.Spec.Containers[0].Name = "prepare"
	pod.Status.ContainerStatuses[0].Name = "prepare"
	pod.Spec.InitContainers = pod.Spec.InitContainers[:2]
	pod.Status.InitContainerStatuses = pod.Status.InitContainerStatuses[:2]
	pod.Spec.InitContainers[1].RestartPolicy = nil
	if _, err := databasePreparationTerminated(job, pod); err != nil {
		t.Fatal("otherwise valid completed prep fixture rejected")
	}
	for _, test := range []struct {
		name   string
		change func(*batchv1.Job, *corev1.Pod)
	}{
		{"missing Job condition", func(j *batchv1.Job, _ *corev1.Pod) { j.Status.Conditions = nil }},
		{"Job failed", func(j *batchv1.Job, _ *corev1.Pod) { j.Status.Failed = 1 }},
		{"Pod pending", func(_ *batchv1.Job, p *corev1.Pod) { p.Status.Phase = corev1.PodRunning }},
		{"initializer missing", func(_ *batchv1.Job, p *corev1.Pod) { p.Status.InitContainerStatuses = nil }},
		{"initializer failed", func(_ *batchv1.Job, p *corev1.Pod) { p.Status.InitContainerStatuses[0].State.Terminated.ExitCode = 1 }},
		{"initializer restart", func(_ *batchv1.Job, p *corev1.Pod) { p.Status.InitContainerStatuses[0].RestartCount = 1 }},
		{"changed helper image", func(_ *batchv1.Job, p *corev1.Pod) { p.Status.ContainerStatuses[0].Image = "foreign" }},
		{"missing runtime imageID", func(_ *batchv1.Job, p *corev1.Pod) { p.Status.ContainerStatuses[0].ImageID = "" }},
		{"signal termination", func(_ *batchv1.Job, p *corev1.Pod) { p.Status.ContainerStatuses[0].State.Terminated.Signal = 15 }},
		{"future finish", func(_ *batchv1.Job, p *corev1.Pod) {
			p.Status.ContainerStatuses[0].State.Terminated.FinishedAt.Time = time.Now().Add(time.Hour)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			j, p := job.DeepCopy(), pod.DeepCopy()
			test.change(j, p)
			if _, err := databasePreparationTerminated(j, p); err == nil {
				t.Fatal("incomplete prep became valid staged DATA")
			}
		})
	}
	job.Status.Conditions = nil
	if _, err := databasePreparationTerminated(job, pod); !errors.Is(err, errDatabaseScanPending) {
		t.Fatal("unfinished prep must remain pending")
	}
}
