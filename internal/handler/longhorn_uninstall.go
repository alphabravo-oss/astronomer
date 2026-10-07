package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

type longhornDeletionPreparation string

const (
	longhornDeletionEnabled   longhornDeletionPreparation = "enabled"
	longhornDeletionCreated   longhornDeletionPreparation = "created"
	longhornDeletionCRDAbsent longhornDeletionPreparation = "crd_absent"
)

// prepareLonghornDeletion enables the chart's mandatory uninstall guard through
// the adopted-cluster agent. Both the Tools and Catalog Apps entrypoints use
// this path so neither requires an operator to repair the cluster with kubectl.
func prepareLonghornDeletion(ctx context.Context, k8s K8sRequester, clusterID string, confirmed, allowMissing bool) (longhornDeletionPreparation, error) {
	if !confirmed {
		return "", errors.New("Longhorn uninstall is missing explicit persistent-data deletion confirmation")
	}
	if k8s == nil {
		return "", errors.New("kubernetes requester not configured for Longhorn uninstall")
	}
	const (
		path       = "/apis/longhorn.io/v1beta2/namespaces/longhorn-system/settings/deleting-confirmation-flag"
		collection = "/apis/longhorn.io/v1beta2/namespaces/longhorn-system/settings"
	)
	resp, err := k8s.Do(ctx, clusterID, http.MethodPatch, path, []byte(`{"value":"true"}`), requestHeaders("application/merge-patch+json"))
	if err != nil {
		return "", fmt.Errorf("enable Longhorn deletion confirmation: %w", err)
	}
	if resp != nil && resp.StatusCode == http.StatusNotFound && allowMissing {
		body := []byte(`{"apiVersion":"longhorn.io/v1beta2","kind":"Setting","metadata":{"name":"deleting-confirmation-flag","namespace":"longhorn-system"},"value":"true"}`)
		created, createErr := k8s.Do(ctx, clusterID, http.MethodPost, collection, body, requestHeaders("application/json"))
		if createErr != nil {
			return "", fmt.Errorf("create Longhorn deletion confirmation during failed-release cleanup: %w", createErr)
		}
		if created != nil && created.StatusCode == http.StatusNotFound {
			return longhornDeletionCRDAbsent, nil
		}
		if created != nil && created.StatusCode == http.StatusConflict {
			created, createErr = k8s.Do(ctx, clusterID, http.MethodPatch, path, []byte(`{"value":"true"}`), requestHeaders("application/merge-patch+json"))
			if createErr != nil {
				return "", fmt.Errorf("enable concurrently-created Longhorn deletion confirmation: %w", createErr)
			}
		}
		if err := ensureSuccess(created); err != nil {
			return "", fmt.Errorf("create Longhorn deletion confirmation during failed-release cleanup: %w", err)
		}
		return longhornDeletionCreated, nil
	}
	if err := ensureSuccess(resp); err != nil {
		return "", fmt.Errorf("enable Longhorn deletion confirmation: %w", err)
	}
	return longhornDeletionEnabled, nil
}
