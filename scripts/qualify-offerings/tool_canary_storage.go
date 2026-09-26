package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func evaluateLonghornCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID, phase string, interval time.Duration) (result dimensionResult) {
	namespace := memberNamespace(execution.Config, clusterID)
	if namespace == "" {
		return failedCanary(clusterID, fmt.Errorf("no qualification namespace is configured for the Longhorn target"))
	}
	suffix := digest([]byte(execution.RunID + "-longhorn-" + phase))[:10]
	pvcName, writerName, readerName := "astr-qual-lh-"+suffix, "astr-qual-lh-write-"+suffix, "astr-qual-lh-read-"+suffix
	marker := "astronomer-longhorn-" + suffix
	k8sBase := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/k8s/api/v1/namespaces/" + url.PathEscape(namespace)
	pvcPath := k8sBase + "/persistentvolumeclaims/" + pvcName
	podPath := func(name string) string { return k8sBase + "/pods/" + name }
	pvcCreated, writerCreated, readerCreated := false, false, false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cleanupErrors := []string{}
		for _, item := range []struct {
			created bool
			path    string
		}{{readerCreated, podPath(readerName)}, {writerCreated, podPath(writerName)}, {pvcCreated, pvcPath}} {
			if !item.created {
				continue
			}
			if _, err := requestAPI(cleanupCtx, client, execution.Base, execution.Token, http.MethodDelete, item.path, nil, "", http.StatusOK, http.StatusAccepted, http.StatusNotFound); err != nil {
				cleanupErrors = append(cleanupErrors, err.Error())
				continue
			}
			if err := waitK8sDeleted(cleanupCtx, client, execution, item.path, time.Second); err != nil {
				cleanupErrors = append(cleanupErrors, err.Error())
			}
		}
		if len(cleanupErrors) > 0 {
			result = failedCanary(clusterID, fmt.Errorf("Longhorn canary cleanup failed: %s", strings.Join(cleanupErrors, "; ")))
		}
	}()
	pvc := map[string]any{
		"apiVersion": "v1", "kind": "PersistentVolumeClaim",
		"metadata": map[string]any{"name": pvcName, "namespace": namespace, "labels": map[string]any{"app.kubernetes.io/managed-by": "astronomer-qualification"}},
		"spec":     map[string]any{"accessModes": []string{"ReadWriteOnce"}, "storageClassName": "longhorn", "resources": map[string]any{"requests": map[string]any{"storage": "64Mi"}}},
	}
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, k8sBase+"/persistentvolumeclaims", pvc, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create Longhorn PVC: %w", err))
	}
	pvcCreated = true
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if err := waitPVCBound(ctx, client, execution, pvcPath, interval); err != nil {
		return failedCanary(clusterID, err)
	}
	writer := longhornCanaryPod(namespace, writerName, pvcName, []string{"sh", "-c", "printf '%s\\n' \"$MARKER\" > /data/value && sync && cat /data/value"}, marker)
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, k8sBase+"/pods", writer, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create Longhorn writer pod: %w", err))
	}
	writerCreated = true
	if err := waitPodSucceeded(ctx, client, execution, podPath(writerName), interval); err != nil {
		return failedCanary(clusterID, fmt.Errorf("Longhorn writer pod: %w", err))
	}
	writerLog, err := requestRawAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, podPath(writerName)+"/log", http.StatusOK)
	if err != nil || !strings.Contains(string(writerLog.Body), marker) {
		return failedCanary(clusterID, fmt.Errorf("Longhorn writer did not persist its marker"))
	}
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodDelete, podPath(writerName), nil, "", http.StatusOK, http.StatusAccepted); err != nil {
		return failedCanary(clusterID, fmt.Errorf("delete Longhorn writer pod: %w", err))
	}
	if err := waitK8sDeleted(ctx, client, execution, podPath(writerName), interval); err != nil {
		return failedCanary(clusterID, err)
	}
	writerCreated = false
	reader := longhornCanaryPod(namespace, readerName, pvcName, []string{"sh", "-c", "grep -Fx \"$MARKER\" /data/value && echo persisted"}, marker)
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, k8sBase+"/pods", reader, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create Longhorn reader pod: %w", err))
	}
	readerCreated = true
	if err := waitPodSucceeded(ctx, client, execution, podPath(readerName), interval); err != nil {
		return failedCanary(clusterID, fmt.Errorf("Longhorn reader pod: %w", err))
	}
	readerLog, err := requestRawAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, podPath(readerName)+"/log", http.StatusOK)
	if err != nil || !strings.Contains(string(readerLog.Body), marker) || !strings.Contains(string(readerLog.Body), "persisted") {
		return failedCanary(clusterID, fmt.Errorf("Longhorn reader did not recover the persisted marker"))
	}
	raw := append(append([]byte{}, writerLog.Body...), readerLog.Body...)
	now := time.Now().UTC()
	return dimensionResult{Name: "functional_canary", State: "PASS", Reason: fmt.Sprintf("Longhorn dynamically provisioned PVC %s/%s and preserved a marker across pod recreation", namespace, pvcName), ObservedAt: now, HTTPStatus: http.StatusOK, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
}

func longhornCanaryPod(namespace, name, pvc string, command []string, marker string) map[string]any {
	return map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name, "namespace": namespace, "labels": map[string]any{"app.kubernetes.io/managed-by": "astronomer-qualification"}},
		"spec": map[string]any{
			"restartPolicy": "Never",
			"containers":    []any{map[string]any{"name": "canary", "image": "busybox:1.36", "command": command, "env": []any{map[string]any{"name": "MARKER", "value": marker}}, "volumeMounts": []any{map[string]any{"name": "data", "mountPath": "/data"}}}},
			"volumes":       []any{map[string]any{"name": "data", "persistentVolumeClaim": map[string]any{"claimName": pvc}}},
		},
	}
}

func waitPVCBound(ctx context.Context, client *http.Client, execution executionContext, path string, interval time.Duration) error {
	return pollK8sObject(ctx, client, execution, path, interval, func(object map[string]any) (bool, error) {
		status, _ := object["status"].(map[string]any)
		phase := stringField(status, "phase")
		if phase == "Bound" {
			return true, nil
		}
		if phase == "Lost" {
			return false, fmt.Errorf("PVC entered Lost phase")
		}
		return false, nil
	})
}

func waitPodSucceeded(ctx context.Context, client *http.Client, execution executionContext, path string, interval time.Duration) error {
	return pollK8sObject(ctx, client, execution, path, interval, func(object map[string]any) (bool, error) {
		status, _ := object["status"].(map[string]any)
		switch stringField(status, "phase") {
		case "Succeeded":
			return true, nil
		case "Failed":
			return false, fmt.Errorf("pod entered Failed phase")
		default:
			return false, nil
		}
	})
}

func pollK8sObject(ctx context.Context, client *http.Client, execution executionContext, path string, interval time.Duration, ready func(map[string]any) (bool, error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, path, nil, "", http.StatusOK)
		if err != nil {
			return err
		}
		object, _ := response.Body.(map[string]any)
		done, readyErr := ready(object)
		if readyErr != nil || done {
			return readyErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitK8sDeleted(ctx context.Context, client *http.Client, execution executionContext, path string, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		response, err := requestRawAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, path, http.StatusOK, http.StatusNotFound)
		if err != nil {
			return err
		}
		if response.Status == http.StatusNotFound {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
