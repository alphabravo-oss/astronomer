package handler

import (
	"testing"
	"time"
)

func TestPodToMapIncludesOperationalContainerDetails(t *testing.T) {
	created := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	pod := podResource{}
	pod.Metadata.Name = "api-0"
	pod.Metadata.Namespace = "apps"
	pod.Metadata.CreationTimestamp = created
	pod.Spec.NodeName = "worker-a"
	pod.Spec.InitContainers = []podContainerSpec{{Name: "migrate", Image: "db-tools:v2"}}
	pod.Spec.Containers = []podContainerSpec{{Name: "api", Image: "api:v4"}}
	pod.Status.Phase = "Running"
	pod.Status.PodIP = "10.42.0.9"
	pod.Status.InitContainerStatuses = []podContainerStatus{{
		Name:         "migrate",
		Image:        "db-tools:v2",
		RestartCount: 1,
		State: podContainerState{Terminated: &podContainerStateDetail{
			Reason:     "Completed",
			ExitCode:   0,
			FinishedAt: "2026-09-18T12:01:00Z",
		}},
	}}
	pod.Status.ContainerStatuses = []podContainerStatus{{
		Name:         "api",
		Image:        "api:v4",
		ImageID:      "containerd://sha256:abc",
		Ready:        false,
		RestartCount: 3,
		State: podContainerState{Waiting: &podContainerStateDetail{
			Reason:  "CrashLoopBackOff",
			Message: "back-off restarting failed container",
		}},
		LastState: podContainerState{Terminated: &podContainerStateDetail{
			Reason:     "OOMKilled",
			ExitCode:   137,
			FinishedAt: "2026-09-18T12:03:00Z",
		}},
	}}

	got := podToMap("cluster-a", pod)
	if got["status"] != "CrashLoopBackOff" {
		t.Fatalf("status = %v, want CrashLoopBackOff", got["status"])
	}
	if got["ready"] != "0/1" {
		t.Fatalf("ready = %v, want 0/1", got["ready"])
	}
	if got["restarts"] != 4 {
		t.Fatalf("restarts = %v, want 4", got["restarts"])
	}
	if got["lastRestartAt"] != "2026-09-18T12:03:00Z" {
		t.Fatalf("lastRestartAt = %v", got["lastRestartAt"])
	}

	containers := got["containers"].([]map[string]any)
	if len(containers) != 2 || containers[1]["init"] != true {
		t.Fatalf("containers = %#v, want init and application details", containers)
	}
	application := containers[0]
	if application["reason"] != "CrashLoopBackOff" || application["imageId"] != "containerd://sha256:abc" {
		t.Fatalf("application container = %#v", application)
	}
	lastState := application["lastState"].(map[string]any)
	terminated := lastState["terminated"].(map[string]any)
	if terminated["reason"] != "OOMKilled" || terminated["exitCode"] != int32(137) {
		t.Fatalf("last terminated state = %#v", terminated)
	}
}

func TestPodDisplayStatusPrefersDeletion(t *testing.T) {
	now := time.Now()
	pod := podResource{}
	pod.Metadata.DeletionTimestamp = &now
	pod.Status.Phase = "Running"
	pod.Status.ContainerStatuses = []podContainerStatus{{
		State: podContainerState{Waiting: &podContainerStateDetail{Reason: "CrashLoopBackOff"}},
	}}
	if got := podDisplayStatus(pod); got != "Terminating" {
		t.Fatalf("status = %q, want Terminating", got)
	}
}
