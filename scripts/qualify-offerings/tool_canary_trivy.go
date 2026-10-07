package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

func evaluateTrivyCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID, phase string, interval time.Duration) dimensionResult {
	started := time.Now().UTC()
	key := "qualification-" + execution.RunID + "-trivy-rescan-" + phase
	path := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/vulnerabilities/rescan/"
	response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, path, nil, key, http.StatusAccepted)
	if err != nil {
		return failedCanary(clusterID, fmt.Errorf("request Trivy rescan: %w", err))
	}
	data, err := objectAtPath(response.Body, "data")
	if err != nil {
		return failedCanary(clusterID, fmt.Errorf("read Trivy rescan receipt: %w", err))
	}
	opID := stringField(data, "operation_id")
	location := stringField(data, "operation_url")
	if opID == "" || location == "" || stringField(data, "cluster_id") != clusterID {
		return failedCanary(clusterID, fmt.Errorf("Trivy rescan receipt did not identify its operation and target cluster"))
	}
	if _, err := pollOperation(ctx, client, execution.Base, execution.Token, location, opID, interval); err != nil {
		return failedCanary(clusterID, fmt.Errorf("Trivy rescan operation: %w", err))
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		images, readErr := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet,
			"/api/v1/clusters/"+url.PathEscape(clusterID)+"/vulnerabilities/images/?limit=200", nil, "", http.StatusOK)
		if readErr != nil {
			return failedCanary(clusterID, fmt.Errorf("read Trivy reports: %w", readErr))
		}
		items, _ := valueAtPath(images.Body, "data")
		fresh := 0
		if rows, ok := items.([]any); ok {
			for _, item := range rows {
				row, ok := item.(map[string]any)
				if !ok || stringField(row, "cluster_id") != clusterID || stringField(row, "scanner") == "" {
					continue
				}
				scannedAt, parseErr := time.Parse(time.RFC3339Nano, stringField(row, "scanned_at"))
				if parseErr == nil && !scannedAt.Before(started.Add(-5*time.Second)) {
					fresh++
				}
			}
		}
		if fresh > 0 {
			raw, _ := json.Marshal(images.Body)
			now := time.Now().UTC()
			return dimensionResult{Name: "functional_canary", State: "PASS", Reason: fmt.Sprintf("Trivy rescan %s produced %d freshly ingested image reports", opID, fresh), ObservedAt: now, HTTPStatus: http.StatusOK, OperationID: opID, ArtifactSHA: digest(raw), IdempotencyKey: key, SampleAt: &now, TargetClusterID: clusterID}
		}
		select {
		case <-ctx.Done():
			return failedCanary(clusterID, fmt.Errorf("Trivy rescan %s produced no fresh ingested reports: %w", opID, ctx.Err()))
		case <-ticker.C:
		}
	}
}
