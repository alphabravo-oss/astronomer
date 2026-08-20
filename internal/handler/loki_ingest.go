package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/lokiauth"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
)

// ReconcileLokiIngest projects hash-only tokens and the query ACL onto the
// management cluster, then opens Ingress on the explicit ingestHostname once
// any token exists. No plaintext leaves Postgres.
func (h *MonitoringHandler) ReconcileLokiIngest(ctx context.Context) error {
	if h == nil || h.queries == nil || h.requester == nil {
		return nil
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return nil
	}
	meta := sharedStackMetadata(backend, "sharedLoki")
	status := stringFromMap(meta, "status")
	if status == "" || status == "not_configured" || status == "uninstalled" {
		return nil
	}
	clusterID := stringFromMap(meta, "managementClusterId")
	if clusterID == "" {
		return nil
	}
	ns := defaultString(stringFromMap(meta, "namespace"), "monitoring")
	release := defaultString(stringFromMap(meta, "releaseName"), sharedLokiDefaultRelease)
	host := strings.TrimSpace(stringFromMap(meta, "ingestHostname"))

	hashes := map[string]string{}
	if lister, ok := h.queries.(lokiTokenHashLister); ok {
		rows, listErr := lister.ListLokiIngestTokenHashes(ctx)
		if listErr != nil {
			return listErr
		}
		for _, row := range rows {
			if row.TokenHash == "" {
				continue
			}
			hashes[row.ClusterID.String()] = row.TokenHash
		}
	}
	hashJSON, err := json.Marshal(hashes)
	if err != nil {
		return err
	}
	if err := applyAlertSecret(ctx, h.requester, clusterID, ns, lokiTokenHashSecretName, map[string]string{
		"hashes": string(hashJSON),
	}); err != nil {
		return fmt.Errorf("reconcile loki token hashes: %w", err)
	}

	acl := h.buildLokiQueryACL(ctx)
	aclJSON, err := json.Marshal(acl)
	if err != nil {
		return err
	}
	if err := applyConfigMap(ctx, h.requester, clusterID, ns, lokiQueryACLConfigMapName, map[string]string{
		"acl": string(aclJSON),
	}); err != nil {
		return fmt.Errorf("reconcile loki query acl: %w", err)
	}

	ingestPublic := len(hashes) > 0 && host != ""
	if ingestPublic {
		ing := lokiIngestIngress(ns, release+"-auth", host, h.lokiIngestClass())
		body, marshalErr := json.Marshal(ing)
		if marshalErr != nil {
			return marshalErr
		}
		if err := applyNetworkingNamedResource(ctx, h.requester, clusterID, ns, "ingresses", release+"-auth", body); err != nil {
			return fmt.Errorf("reconcile loki ingest ingress: %w", err)
		}
	}
	return h.stampSharedLokiIngestPublic(ctx, ingestPublic)
}

func (h *MonitoringHandler) buildLokiQueryACL(ctx context.Context) lokiauth.QueryACL {
	acl := lokiauth.QueryACL{Users: map[string][]string{}}
	src, ok := h.queries.(interface {
		ListLokiQueryACLAdmins(context.Context) ([]string, error)
		ListLokiQueryACLUserClusters(context.Context) ([]sqlc.ListLokiQueryACLUserClustersRow, error)
	})
	if !ok {
		return acl
	}
	if admins, err := src.ListLokiQueryACLAdmins(ctx); err == nil {
		acl.Admins = admins
	}
	if rows, err := src.ListLokiQueryACLUserClusters(ctx); err == nil {
		for _, row := range rows {
			email := strings.ToLower(strings.TrimSpace(row.Email))
			if email == "" {
				continue
			}
			acl.Users[email] = append(acl.Users[email], row.ClusterID.String())
		}
	}
	return acl
}

func (h *MonitoringHandler) stampSharedLokiIngestPublic(ctx context.Context, ingestPublic bool) error {
	if h == nil || h.queries == nil {
		return nil
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return err
	}
	authCfg, err := resolveMonitoringBackendAuthConfig(backend, h.monitoringDecryptor())
	if err != nil {
		return err
	}
	meta := mapFromMapValue(authCfg["sharedLoki"])
	if meta == nil {
		meta = map[string]any{}
	}
	meta["ingestPublic"] = ingestPublic
	authCfg["sharedLoki"] = meta
	params := sqlc.UpsertDefaultMonitoringBackendParams{
		BackendType:        backend.BackendType,
		QueryUrl:           backend.QueryUrl,
		AlertmanagerUrl:    backend.AlertmanagerUrl,
		TenantID:           backend.TenantID,
		AuthType:           backend.AuthType,
		DefaultStepSeconds: backend.DefaultStepSeconds,
		TimeoutSeconds:     backend.TimeoutSeconds,
		CreatedByID:        backend.CreatedByID,
	}
	if err := imonitoring.SealInto(&params, authCfg, h.monitoringSealer()); err != nil {
		return err
	}
	_, err = h.queries.UpsertDefaultMonitoringBackend(ctx, params)
	return err
}

func applyNetworkingNamedResource(ctx context.Context, requester K8sRequester, clusterID, namespace, plural, name string, body []byte) error {
	patchPath := fmt.Sprintf("/apis/networking.k8s.io/v1/namespaces/%s/%s/%s", namespace, plural, name)
	resp, err := requester.Do(ctx, clusterID, http.MethodPatch, patchPath, body, requestHeaders("application/merge-patch+json"))
	if err == nil && resp != nil && resp.StatusCode != http.StatusNotFound {
		return ensureSuccess(resp)
	}
	createPath := fmt.Sprintf("/apis/networking.k8s.io/v1/namespaces/%s/%s", namespace, plural)
	resp, err = requester.Do(ctx, clusterID, http.MethodPost, createPath, body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	return ensureSuccess(resp)
}

func (h *MonitoringHandler) runLokiIngestReconciler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	_ = h.ReconcileLokiIngest(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := h.ReconcileLokiIngest(ctx); err != nil && h.log != nil {
				h.log.Warn("loki ingest reconcile failed", "error", err)
			}
		}
	}
}
