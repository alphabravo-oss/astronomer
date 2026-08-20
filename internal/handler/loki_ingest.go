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

	// Mint the management-plane token before hashing so loki-auth admits
	// server/worker logs in the same tick as ingestPublic flipping on.
	if lokiRunning(meta) && host != "" && h.encryptor != nil {
		if local, ok := h.localManagementCluster(ctx); ok {
			if _, err := h.ensureManagementLokiToken(ctx, local.ID); err != nil && h.log != nil {
				h.log.Warn("management ingest token ensure failed", "error", err)
			}
		}
	}

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
		if err := h.stampSharedLokiIngestPublic(ctx, false); err != nil {
			return err
		}
		if err := h.ReconcileManagementLogging(ctx); err != nil && h.log != nil {
			h.log.Warn("management logging overlay failed", "error", err)
		}
		return nil
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

	tlsNS := h.lokiTLSNamespace(ns)
	cert := lokiIngestCertificate(tlsNS, host, h.grafanaExpose)
	if err := applyAPIResource(ctx, h.requester, clusterID, "cert-manager.io/v1", tlsNS, "certificates", lokiIngestTLSSecretName, cert); err != nil {
		return fmt.Errorf("reconcile loki ingest certificate: %w", err)
	}

	if h.lokiUseGateway() {
		if err := h.ensureGatewayIngestListener(ctx, clusterID, host, false); err != nil {
			return err
		}
		route := lokiIngestHTTPRoute(h.grafanaExpose.PlatformNamespace, h.grafanaExpose.GatewayName, ns, svcName, host)
		if err := applyAPIResource(ctx, h.requester, clusterID, "gateway.networking.k8s.io/v1", h.grafanaExpose.PlatformNamespace, "httproutes", svcName, route); err != nil {
			return fmt.Errorf("reconcile loki ingest httproute: %w", err)
		}
		grant := grafanaProxyReferenceGrant(ns, svcName, h.grafanaExpose.PlatformNamespace)
		if err := applyAPIResource(ctx, h.requester, clusterID, "gateway.networking.k8s.io/v1beta1", ns, "referencegrants", svcName+"-from-gateway", grant); err != nil {
			return fmt.Errorf("reconcile loki ingest referencegrant: %w", err)
		}
	} else {
		if err := h.ensureLokiNamespaceIssuer(ctx, clusterID, tlsNS); err != nil {
			return err
		}
		ing := lokiIngestIngress(tlsNS, svcName, host, h.lokiIngestClass(), h.grafanaExpose)
		if err := applyAPIResource(ctx, h.requester, clusterID, "networking.k8s.io/v1", tlsNS, "ingresses", svcName, ing); err != nil {
			return fmt.Errorf("reconcile loki ingest ingress: %w", err)
		}
	}
	if err := h.stampSharedLokiIngestPublic(ctx, true); err != nil {
		return err
	}
	if err := h.ReconcileManagementLogging(ctx); err != nil && h.log != nil {
		h.log.Warn("management logging overlay failed", "error", err)
	}
	return nil
}

func (h *MonitoringHandler) ensureLokiNamespaceIssuer(ctx context.Context, clusterID, destNS string) error {
	if h == nil || destNS == "" {
		return nil
	}
	name, kind := lokiTLSIssuer(h.grafanaExpose)
	if !strings.EqualFold(kind, "Issuer") {
		return nil
	}
	srcNS := strings.TrimSpace(h.grafanaExpose.PlatformNamespace)
	if srcNS == "" || srcNS == destNS {
		return nil
	}
	path := fmt.Sprintf("/apis/cert-manager.io/v1/namespaces/%s/issuers/%s", srcNS, name)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return fmt.Errorf("get platform tls issuer: %w", err)
	}
	if resp == nil || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if err := ensureSuccess(resp); err != nil {
		return err
	}
	var issuer map[string]any
	if err := parseJSONResponse(resp, &issuer); err != nil {
		return fmt.Errorf("decode platform tls issuer: %w", err)
	}
	spec, _ := issuer["spec"].(map[string]any)
	cloned := map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Issuer",
		"metadata":   map[string]any{"name": name, "namespace": destNS},
		"spec":       spec,
	}
	return applyAPIResource(ctx, h.requester, clusterID, "cert-manager.io/v1", destNS, "issuers", name, cloned)
}

func (h *MonitoringHandler) lokiTLSNamespace(lokiNS string) string {
	if h != nil && h.lokiUseGateway() {
		if plat := strings.TrimSpace(h.grafanaExpose.PlatformNamespace); plat != "" {
			return plat
		}
	}
	return lokiNS
}

func (h *MonitoringHandler) lokiUseGateway() bool {
	if h == nil {
		return false
	}
	e := h.grafanaExpose
	return e.GatewayClass != "" && e.PlatformNamespace != "" && e.GatewayName != ""
}

func (h *MonitoringHandler) deleteLokiPublicIngest(ctx context.Context, clusterID, ns, svcName string) error {
	tlsNS := h.lokiTLSNamespace(ns)
	if err := deleteAPIResource(ctx, h.requester, clusterID, "networking.k8s.io/v1", tlsNS, "ingresses", svcName); err != nil {
		return err
	}
	if err := deleteAPIResource(ctx, h.requester, clusterID, "cert-manager.io/v1", tlsNS, "certificates", lokiIngestTLSSecretName); err != nil {
		return err
	}
	if plat := strings.TrimSpace(h.grafanaExpose.PlatformNamespace); plat != "" {
		if err := h.ensureGatewayIngestListener(ctx, clusterID, "", true); err != nil && h.log != nil {
			h.log.Warn("remove loki ingest gateway listener", "error", err)
		}
		if err := deleteAPIResource(ctx, h.requester, clusterID, "gateway.networking.k8s.io/v1", plat, "httproutes", svcName); err != nil {
			return err
		}
		if err := deleteAPIResource(ctx, h.requester, clusterID, "cert-manager.io/v1", plat, "certificates", lokiIngestTLSSecretName); err != nil {
			return err
		}
	}
	return deleteAPIResource(ctx, h.requester, clusterID, "gateway.networking.k8s.io/v1beta1", ns, "referencegrants", svcName+"-from-gateway")
}

func (h *MonitoringHandler) ensureGatewayIngestListener(ctx context.Context, clusterID, host string, remove bool) error {
	if h == nil || !h.lokiUseGateway() {
		return nil
	}
	ns := h.grafanaExpose.PlatformNamespace
	name := h.grafanaExpose.GatewayName
	path := fmt.Sprintf("/apis/gateway.networking.k8s.io/v1/namespaces/%s/gateways/%s", ns, name)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return fmt.Errorf("get platform gateway: %w", err)
	}
	if resp == nil || resp.StatusCode == http.StatusNotFound {
		if remove {
			return nil
		}
		return fmt.Errorf("platform gateway %s/%s not found", ns, name)
	}
	if err := ensureSuccess(resp); err != nil {
		return err
	}
	var gw map[string]any
	if err := parseJSONResponse(resp, &gw); err != nil {
		return fmt.Errorf("decode platform gateway: %w", err)
	}
	spec, _ := gw["spec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
		gw["spec"] = spec
	}
	listeners, _ := spec["listeners"].([]any)
	next := make([]any, 0, len(listeners)+1)
	for _, raw := range listeners {
		m, _ := raw.(map[string]any)
		if m != nil && fmt.Sprint(m["name"]) == lokiIngestGatewayListener {
			continue
		}
		next = append(next, raw)
	}
	if !remove && host != "" {
		next = append(next, map[string]any{
			"name":     lokiIngestGatewayListener,
			"hostname": host,
			"port":     443,
			"protocol": "HTTPS",
			"tls": map[string]any{
				"mode": "Terminate",
				"certificateRefs": []any{
					map[string]any{"kind": "Secret", "name": lokiIngestTLSSecretName},
				},
			},
			"allowedRoutes": map[string]any{
				"namespaces": map[string]any{"from": "Same"},
			},
		})
	}
	spec["listeners"] = next
	body, err := json.Marshal(gw)
	if err != nil {
		return err
	}
	put, err := h.requester.Do(ctx, clusterID, http.MethodPut, path, body, requestHeaders("application/json"))
	if err != nil {
		return fmt.Errorf("update platform gateway listeners: %w", err)
	}
	return ensureSuccess(put)
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
