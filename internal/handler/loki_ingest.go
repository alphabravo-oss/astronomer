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
// management cluster. Public ingest objects (Ingress or HTTPRoute, plus a
// cert-manager Certificate) are reconcile-owned so Helm extraObjects never
// fight rotate/uninstall.
func (h *MonitoringHandler) ReconcileLokiIngest(ctx context.Context) error {
	if h == nil || h.queries == nil || h.requester == nil {
		return nil
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return nil
	}
	meta := sharedStackMetadata(backend, "sharedLoki")
	clusterID := stringFromMap(meta, "managementClusterId")
	if clusterID == "" {
		return nil
	}
	ns := defaultString(stringFromMap(meta, "namespace"), "monitoring")
	release := defaultString(stringFromMap(meta, "releaseName"), sharedLokiDefaultRelease)
	host := strings.TrimSpace(stringFromMap(meta, "ingestHostname"))
	status := stringFromMap(meta, "status")
	svcName := release + "-auth"

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

	if !lokiIngestShouldBePublic(status, len(hashes) > 0, host) {
		if err := h.deleteLokiPublicIngest(ctx, clusterID, ns, svcName); err != nil {
			return err
		}
		return h.stampSharedLokiIngestPublic(ctx, false)
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

	certNS := ns
	if h.lokiUseGateway() {
		certNS = h.grafanaExpose.PlatformNamespace
	}
	cert := lokiIngestCertificate(certNS, host, h.grafanaExpose)
	if err := applyAPIResource(ctx, h.requester, clusterID, "cert-manager.io/v1", certNS, "certificates", lokiIngestTLSSecretName, cert); err != nil {
		return fmt.Errorf("reconcile loki ingest certificate: %w", err)
	}

	if h.lokiUseGateway() {
		route := lokiIngestHTTPRoute(h.grafanaExpose.PlatformNamespace, h.grafanaExpose.GatewayName, ns, svcName, host)
		if err := applyAPIResource(ctx, h.requester, clusterID, "gateway.networking.k8s.io/v1", h.grafanaExpose.PlatformNamespace, "httproutes", svcName, route); err != nil {
			return fmt.Errorf("reconcile loki ingest httproute: %w", err)
		}
		grant := grafanaProxyReferenceGrant(ns, svcName, h.grafanaExpose.PlatformNamespace)
		if err := applyAPIResource(ctx, h.requester, clusterID, "gateway.networking.k8s.io/v1beta1", ns, "referencegrants", svcName+"-from-gateway", grant); err != nil {
			return fmt.Errorf("reconcile loki ingest referencegrant: %w", err)
		}
	} else {
		ing := lokiIngestIngress(ns, svcName, host, h.lokiIngestClass(), h.grafanaExpose)
		if err := applyAPIResource(ctx, h.requester, clusterID, "networking.k8s.io/v1", ns, "ingresses", svcName, ing); err != nil {
			return fmt.Errorf("reconcile loki ingest ingress: %w", err)
		}
	}
	return h.stampSharedLokiIngestPublic(ctx, true)
}

func (h *MonitoringHandler) lokiUseGateway() bool {
	if h == nil {
		return false
	}
	e := h.grafanaExpose
	return e.GatewayClass != "" && e.PlatformNamespace != "" && e.GatewayName != ""
}

func (h *MonitoringHandler) deleteLokiPublicIngest(ctx context.Context, clusterID, ns, svcName string) error {
	if err := deleteAPIResource(ctx, h.requester, clusterID, "networking.k8s.io/v1", ns, "ingresses", svcName); err != nil {
		return err
	}
	if err := deleteAPIResource(ctx, h.requester, clusterID, "cert-manager.io/v1", ns, "certificates", lokiIngestTLSSecretName); err != nil {
		return err
	}
	if plat := strings.TrimSpace(h.grafanaExpose.PlatformNamespace); plat != "" {
		if err := deleteAPIResource(ctx, h.requester, clusterID, "gateway.networking.k8s.io/v1", plat, "httproutes", svcName); err != nil {
			return err
		}
		if err := deleteAPIResource(ctx, h.requester, clusterID, "cert-manager.io/v1", plat, "certificates", lokiIngestTLSSecretName); err != nil {
			return err
		}
	}
	return deleteAPIResource(ctx, h.requester, clusterID, "gateway.networking.k8s.io/v1beta1", ns, "referencegrants", svcName+"-from-gateway")
}

func (h *MonitoringHandler) buildLokiQueryACL(ctx context.Context) lokiauth.QueryACL {
	acl := lokiauth.QueryACL{Users: map[string][]string{}}
	src, ok := h.queries.(interface {
		ListLokiQueryACLAdminCandidates(context.Context) ([]sqlc.ListLokiQueryACLAdminCandidatesRow, error)
		ListLokiQueryACLUserCandidates(context.Context) ([]sqlc.ListLokiQueryACLUserCandidatesRow, error)
	})
	if !ok {
		return acl
	}
	admins, _ := src.ListLokiQueryACLAdminCandidates(ctx)
	users, _ := src.ListLokiQueryACLUserCandidates(ctx)
	return buildLokiQueryACLFromCandidates(admins, users)
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

func applyAPIResource(ctx context.Context, requester K8sRequester, clusterID, groupVersion, namespace, plural, name string, obj map[string]any) error {
	body, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	patchPath := fmt.Sprintf("/apis/%s/namespaces/%s/%s/%s", groupVersion, namespace, plural, name)
	resp, err := requester.Do(ctx, clusterID, http.MethodPatch, patchPath, body, requestHeaders("application/merge-patch+json"))
	if err == nil && resp != nil && resp.StatusCode != http.StatusNotFound {
		return ensureSuccess(resp)
	}
	createPath := fmt.Sprintf("/apis/%s/namespaces/%s/%s", groupVersion, namespace, plural)
	resp, err = requester.Do(ctx, clusterID, http.MethodPost, createPath, body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	return ensureSuccess(resp)
}

func deleteAPIResource(ctx context.Context, requester K8sRequester, clusterID, groupVersion, namespace, plural, name string) error {
	if requester == nil || namespace == "" || name == "" {
		return nil
	}
	path := fmt.Sprintf("/apis/%s/namespaces/%s/%s/%s", groupVersion, namespace, plural, name)
	resp, err := requester.Do(ctx, clusterID, http.MethodDelete, path, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	if resp == nil || resp.StatusCode == http.StatusNotFound {
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
