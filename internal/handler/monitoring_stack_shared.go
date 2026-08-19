package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// sharedStackLifecycle drives the six /settings/monitoring lifecycle endpoints
// — Preview, Install, Upgrade, Replace, Uninstall, Status — for ONE shared
// monitoring stack family. Shared families instantiated below: Thanos,
// Alertmanager, Grafana, and Loki.
//
// It exists for the authorization preamble, not for the line count. The first
// two families were written by copying one another, and the copy dropped
//
//	if !h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbRead)
//
// from BOTH Preview handlers, which served rendered Helm values to callers
// holding no monitoring grant until 3799088 restored them by hand. Restoring a
// line by hand fixes the instance, not the class: the next family, or the
// seventh endpoint on an existing one, is written the same way.
//
// Here the preamble is written exactly once per verb, inside this type, and it
// is the first statement of the only six bodies that exist. A family is a data
// literal — it supplies charts, payload decoding, persistence, enqueue, and an
// optional precheck hook, and has no way to express "and skip the check",
// because there is no body of its own to omit it from. A NEW FAMILY therefore
// gets all six gates free. TestSharedStackHandlersOnlyDelegateToTheLifecycleDriver
// counts 6 handlers per family (currently 24).
//
// A seventh ENDPOINT is the direction the original bug came from, and a comment
// promising that the next author will copy the preamble is worth nothing, so it
// is enforced instead: TestSharedStackLifecycleMethodsOpenWithTheAuthorizationGate
// parses this file and fails unless every route-shaped method on this type
// opens with the gate on rbac.ResourceMonitoring, and
// TestSharedStackHandlersOnlyDelegateToTheLifecycleDriver fails unless every
// exported handler here is a bare delegation to one of them. Both are in
// monitoring_stack_shared_gate_test.go and neither consults a list of endpoint
// names, so adding one cannot outrun them.
//
// Deliberately NOT generalised to the per-cluster stack
// (monitoring_stack_cluster.go): that family is gated by requirePermission
// middleware at routes_clusters.go rather than in the handler, persists a
// typed row rather than a JSON metadata bag, and has a materially different
// Uninstall. Folding it in would need an optional Resource field — an
// "authorization is somebody else's job" escape hatch on the very type whose
// job is to make that impossible. See the file comment there for the fence
// that covers it instead.
//
// Everything the shared families genuinely differ on is a field. There are no
// per-family conditionals in any of the six methods (precheck is a hook;
// Thanos/Alertmanager leave it nil).
type sharedStackLifecycle[Req any] struct {
	h *MonitoringHandler

	// auditPrefix is the audit action stem: "<auditPrefix>.install" and so on.
	// These names are a wire contract pinned by internal/audit — do not reword.
	auditPrefix string
	// noun names the family in operator-facing error and conflict text
	// ("Thanos", "Alertmanager").
	noun string
	// metadataKey is the monitoring_backends.auth_config key holding this
	// family's deployment metadata.
	metadataKey string
	// opTargetType is the monitoring_operations.target_type this family enqueues
	// under, used to surface the latest operation on the status response.
	opTargetType string
	// chartRepo/chartName are echoed by Preview so the operator can see what
	// would be installed.
	chartRepo string
	chartName string
	// defaultRelease is the release name assumed when neither the request nor
	// the persisted metadata names one.
	defaultRelease string

	// payload decodes and defaults the request, renders the Helm values, and
	// loads the backend row the metadata hangs off. The object-store secret is
	// nil for families that do not provision one.
	payload func(context.Context, *http.Request) (Req, map[string]any, *objectStoreSecretSpec, sqlc.MonitoringBackend, error)
	// replaceRequired reports whether the persisted metadata makes the request
	// a reinstall rather than an in-place upgrade.
	replaceRequired func(map[string]any, Req) (bool, []string)
	// persist stamps this family's metadata (and status) onto the backend row.
	persist func(context.Context, sqlc.MonitoringBackend, Req, string) error
	// enqueue creates the async operation that does the actual Helm work.
	enqueue func(context.Context, pgtype.UUID, string, Req, map[string]any, *objectStoreSecretSpec) (sqlc.MonitoringOperation, error)
	// target reads the three routing fields out of a request.
	target func(Req) (clusterID, namespace, releaseName string)
	// retarget rewrites those three fields, preserving whichever of the
	// family's remaining fields the Replace path is defined to carry over.
	// Called with the zero request by Uninstall, which carries nothing over.
	retarget func(Req, string, string, string) Req
	// statusFields projects the persisted metadata into the family's status
	// response. The driver adds the observed release, drift and pod count.
	statusFields func(map[string]any, sqlc.MonitoringBackend) map[string]any
	// precheck runs after authorize + payload, before persist.
	// ok=true → continue. ok=false → write status/code/msg and return.
	// Thanos/Alertmanager leave this nil (treated as ok). Preview does not
	// call it. Grafana install/replace 412 on leftover-floor fail; upgrade
	// skips the floor. Loki install/replace/mode-widen 412 on sizer fail;
	// in-place upgrade and sizer_ratchet skip the mode gate. Preview does
	// not call it.
	precheck func(ctx context.Context, req Req, op string) (status int, code, msg string, ok bool)
}

func (l sharedStackLifecycle[Req]) preview(w http.ResponseWriter, r *http.Request) {
	if !l.h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbRead) {
		return
	}
	req, values, _, backend, err := l.payload(r.Context(), r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	replaceRequired, reasons := l.replaceRequired(sharedStackMetadata(backend, l.metadataKey), req)
	clusterID, _, _ := l.target(req)
	RespondJSON(w, http.StatusOK, map[string]any{
		"clusterId": clusterID,
		"chart": map[string]any{
			"repoUrl":   l.chartRepo,
			"chartName": l.chartName,
		},
		"values":          sanitizeMonitoringValues(values),
		"desiredSpecHash": specHash(values),
		"requiresReplace": replaceRequired,
		"replaceReasons":  reasons,
	})
}

func (l sharedStackLifecycle[Req]) install(w http.ResponseWriter, r *http.Request) {
	if !l.h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbUpdate) {
		return
	}
	req, values, secretSpec, backend, err := l.payload(r.Context(), r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	if !l.runPrecheck(w, r, req, "install") {
		return
	}
	if err := l.persist(r.Context(), backend, req, "installing"); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, l.persistFailure())
		return
	}
	op, err := l.enqueue(withOperationIdempotency(r, "monitoring"), currentUserUUID(r), "install", req, values, secretSpec)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to create monitoring operation")
		return
	}
	clusterID, namespace, releaseName := l.target(req)
	l.recordLifecycleAudit(r, "install", backend, clusterID, namespace, releaseName, op)
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (l sharedStackLifecycle[Req]) upgrade(w http.ResponseWriter, r *http.Request) {
	if !l.h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbUpdate) {
		return
	}
	req, values, secretSpec, backend, err := l.payload(r.Context(), r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	if !l.runPrecheck(w, r, req, "upgrade") {
		return
	}
	if replaceRequired, reasons := l.replaceRequired(sharedStackMetadata(backend, l.metadataKey), req); replaceRequired {
		RespondJSON(w, http.StatusConflict, map[string]any{
			"error":           "replace_required",
			"message":         "Requested " + l.noun + " changes require reinstall rather than in-place upgrade",
			"requiresReplace": true,
			"replaceReasons":  reasons,
		})
		return
	}
	if err := l.persist(r.Context(), backend, req, "updating"); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, l.persistFailure())
		return
	}
	op, err := l.enqueue(withOperationIdempotency(r, "monitoring"), currentUserUUID(r), "upgrade", req, values, secretSpec)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to create monitoring operation")
		return
	}
	clusterID, namespace, releaseName := l.target(req)
	l.recordLifecycleAudit(r, "upgrade", backend, clusterID, namespace, releaseName, op)
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (l sharedStackLifecycle[Req]) replace(w http.ResponseWriter, r *http.Request) {
	if !l.h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbUpdate) {
		return
	}
	req, values, secretSpec, backend, err := l.payload(r.Context(), r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	if !l.runPrecheck(w, r, req, "replace") {
		return
	}
	metadata := sharedStackMetadata(backend, l.metadataKey)
	reqCluster, reqNamespace, reqRelease := l.target(req)
	clusterID := defaultString(reqCluster, stringFromMap(metadata, "managementClusterId"))
	namespace := defaultString(reqNamespace, defaultString(stringFromMap(metadata, "namespace"), "monitoring"))
	releaseName := defaultString(reqRelease, defaultString(stringFromMap(metadata, "releaseName"), l.defaultRelease))
	if clusterID == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "managementClusterId is required")
		return
	}
	// Persisted from the REQUEST, while the operation is enqueued from the
	// metadata-defaulted target below. That asymmetry predates this driver and
	// is preserved verbatim: a Replace with an empty body stamps empty metadata
	// but still uninstalls/reinstalls the release the metadata named.
	if err := l.persist(r.Context(), backend, req, "reinstalled"); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, l.persistFailure())
		return
	}
	op, err := l.enqueue(withOperationIdempotency(r, "monitoring"), currentUserUUID(r), "replace", l.retarget(req, clusterID, namespace, releaseName), values, secretSpec)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to create monitoring operation")
		return
	}
	l.recordLifecycleAudit(r, "replace", backend, clusterID, namespace, releaseName, op)
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (l sharedStackLifecycle[Req]) uninstall(w http.ResponseWriter, r *http.Request) {
	if !l.h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbUpdate) {
		return
	}
	if l.h.helm == nil || l.h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.HelmError, "monitoring deployment is not configured")
		return
	}
	backend, err := l.h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.MonitoringError, "Default monitoring backend is not configured")
		return
	}
	metadata := sharedStackMetadata(backend, l.metadataKey)
	clusterID := r.URL.Query().Get("clusterId")
	if clusterID == "" {
		clusterID = stringFromMap(metadata, "managementClusterId")
	}
	if clusterID == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "clusterId is required")
		return
	}
	namespace := defaultString(stringFromMap(metadata, "namespace"), "monitoring")
	releaseName := defaultString(stringFromMap(metadata, "releaseName"), l.defaultRelease)
	// Uninstall carries nothing over from the request body: the zero request
	// retargeted at the release the metadata names.
	var zero Req
	target := l.retarget(zero, clusterID, namespace, releaseName)
	if err := l.persist(r.Context(), backend, target, "uninstalled"); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, l.persistFailure())
		return
	}
	op, err := l.enqueue(withOperationIdempotency(r, "monitoring"), currentUserUUID(r), "uninstall", target, nil, nil)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to create monitoring operation")
		return
	}
	l.recordLifecycleAudit(r, "uninstall", backend, clusterID, namespace, releaseName, op)
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (l sharedStackLifecycle[Req]) status(w http.ResponseWriter, r *http.Request) {
	if !l.h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbRead) {
		return
	}
	if l.h.queries == nil {
		RespondJSON(w, http.StatusOK, map[string]any{"status": "not_configured"})
		return
	}
	backend, err := l.h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil {
		RespondJSON(w, http.StatusOK, map[string]any{"status": "not_configured"})
		return
	}
	metadata := sharedStackMetadata(backend, l.metadataKey)
	status := l.statusFields(metadata, backend)
	if observed, drifted, reasons := l.h.observeRelease(r.Context(), stringFromMap(metadata, "managementClusterId"), releaseRef{
		Namespace:   defaultString(stringFromMap(metadata, "namespace"), "monitoring"),
		ReleaseName: defaultString(stringFromMap(metadata, "releaseName"), l.defaultRelease),
	}); observed != nil {
		status["observedRelease"] = observed
		status["drifted"] = drifted
		status["driftReasons"] = reasons
		if drifted && status["status"] == "healthy" {
			status["status"] = "drifted"
		}
	}
	if op, ok := l.h.latestMonitoringOperation(r.Context(), l.opTargetType, "shared"); ok {
		status["operation"] = op
	}
	if l.h.requester != nil {
		clusterID := stringFromMap(metadata, "managementClusterId")
		namespace := defaultString(stringFromMap(metadata, "namespace"), "monitoring")
		releaseName := defaultString(stringFromMap(metadata, "releaseName"), l.defaultRelease)
		if clusterID != "" {
			path := fmt.Sprintf("/api/v1/namespaces/%s/pods?labelSelector=%s", namespace, url.QueryEscape("app.kubernetes.io/instance="+releaseName))
			resp, doErr := l.h.requester.Do(r.Context(), clusterID, http.MethodGet, path, nil, requestHeaders(""))
			if doErr == nil && ensureSuccess(resp) == nil {
				var payload map[string]any
				if parseErr := parseJSONResponse(resp, &payload); parseErr == nil {
					status["pods"] = len(objectItems(payload))
				}
			}
		}
	}
	RespondJSON(w, http.StatusOK, status)
}

func (l sharedStackLifecycle[Req]) persistFailure() string {
	return "Failed to persist shared " + l.noun + " metadata"
}

func (l sharedStackLifecycle[Req]) runPrecheck(w http.ResponseWriter, r *http.Request, req Req, op string) bool {
	if l.precheck == nil {
		return true
	}
	status, code, msg, ok := l.precheck(r.Context(), req, op)
	if ok {
		return true
	}
	if status == 0 {
		status = http.StatusPreconditionFailed
	}
	if code == "" {
		code = apierror.SizerFailed
	}
	RespondRequestError(w, r, status, code, msg)
	return false
}

// recordLifecycleAudit emits the family's audit row. Action name and detail
// keys are a wire contract (internal/audit pins the vocabulary, and the
// coverage contract requires every mutating handler in this file to reach
// here) — the only thing this collapse changed is where the call is written.
func (l sharedStackLifecycle[Req]) recordLifecycleAudit(r *http.Request, verb string, backend sqlc.MonitoringBackend, clusterID, namespace, releaseName string, op sqlc.MonitoringOperation) {
	recordAudit(r, l.h.queries, l.auditPrefix+"."+verb, "monitoring_backend", backend.ID.String(), backend.BackendType, map[string]any{
		"managementClusterId": clusterID,
		"namespace":           namespace,
		"releaseName":         releaseName,
		"operationId":         op.ID.String(),
	})
}

func (h *MonitoringHandler) sharedThanosLifecycle() sharedStackLifecycle[SharedThanosStackRequest] {
	return sharedStackLifecycle[SharedThanosStackRequest]{
		h:              h,
		auditPrefix:    "monitoring.shared_thanos",
		noun:           "Thanos",
		metadataKey:    "sharedThanos",
		opTargetType:   "shared_thanos",
		chartRepo:      "https://stevehipwell.github.io/helm-charts/",
		chartName:      "thanos",
		defaultRelease: "thanos",
		payload: func(ctx context.Context, r *http.Request) (SharedThanosStackRequest, map[string]any, *objectStoreSecretSpec, sqlc.MonitoringBackend, error) {
			req, values, secretSpec, backend, err := h.sharedThanosPayload(ctx, r)
			if err != nil {
				return SharedThanosStackRequest{}, nil, nil, sqlc.MonitoringBackend{}, err
			}
			return req, values, &secretSpec, backend, nil
		},
		replaceRequired: sharedThanosReplaceRequired,
		persist:         h.updateSharedThanosMetadata,
		enqueue:         h.enqueueSharedThanosOperation,
		target: func(req SharedThanosStackRequest) (string, string, string) {
			return req.ManagementClusterID, req.Namespace, req.ReleaseName
		},
		retarget: func(req SharedThanosStackRequest, clusterID, namespace, releaseName string) SharedThanosStackRequest {
			return SharedThanosStackRequest{
				ManagementClusterID:     clusterID,
				Namespace:               namespace,
				ReleaseName:             releaseName,
				ChartVersion:            req.ChartVersion,
				StorageConfigID:         req.StorageConfigID,
				ObjectStorageSecretName: req.ObjectStorageSecretName,
				QueryReplicas:           req.QueryReplicas,
				StoreGatewayReplicas:    req.StoreGatewayReplicas,
				CompactorReplicas:       req.CompactorReplicas,
			}
		},
		statusFields: func(metadata map[string]any, backend sqlc.MonitoringBackend) map[string]any {
			return map[string]any{
				"status":                  defaultString(stringFromMap(metadata, "status"), "not_configured"),
				"managementClusterId":     stringFromMap(metadata, "managementClusterId"),
				"namespace":               stringFromMap(metadata, "namespace"),
				"releaseName":             stringFromMap(metadata, "releaseName"),
				"storageConfigId":         stringFromMap(metadata, "storageConfigId"),
				"objectStorageSecretName": stringFromMap(metadata, "objectStorageSecretName"),
				"chartVersion":            stringFromMap(metadata, "chartVersion"),
				"queryReplicas":           metadata["queryReplicas"],
				"storeGatewayReplicas":    metadata["storeGatewayReplicas"],
				"compactorReplicas":       metadata["compactorReplicas"],
				"desiredSpecHash":         stringFromMap(metadata, "lastAppliedSpecHash"),
				"managedAssetHashes":      mapFromMapValue(metadata["managedAssetHashes"]),
				"alertingAssetHashes":     mapFromMapValue(mapFromMapValue(decodeJSONMap(backend.AuthConfig)["sharedAlertingAssets"])["hashes"]),
			}
		},
	}
}

func (h *MonitoringHandler) sharedAlertmanagerLifecycle() sharedStackLifecycle[SharedAlertmanagerRequest] {
	return sharedStackLifecycle[SharedAlertmanagerRequest]{
		h:              h,
		auditPrefix:    "monitoring.shared_alertmanager",
		noun:           "Alertmanager",
		metadataKey:    "sharedAlertmanager",
		opTargetType:   "shared_alertmanager",
		chartRepo:      "https://prometheus-community.github.io/helm-charts",
		chartName:      "alertmanager",
		defaultRelease: "astronomer-alertmanager",
		payload: func(ctx context.Context, r *http.Request) (SharedAlertmanagerRequest, map[string]any, *objectStoreSecretSpec, sqlc.MonitoringBackend, error) {
			// This family provisions no object-store secret; the operation
			// envelope carries a nil secretSpec, as it always has.
			req, values, backend, err := h.sharedAlertmanagerPayload(ctx, r)
			return req, values, nil, backend, err
		},
		replaceRequired: sharedAlertmanagerReplaceRequired,
		persist:         h.updateSharedAlertmanagerMetadata,
		enqueue: func(ctx context.Context, userID pgtype.UUID, opType string, req SharedAlertmanagerRequest, values map[string]any, _ *objectStoreSecretSpec) (sqlc.MonitoringOperation, error) {
			return h.enqueueSharedAlertmanagerOperation(ctx, userID, opType, req, values)
		},
		target: func(req SharedAlertmanagerRequest) (string, string, string) {
			return req.ManagementClusterID, req.Namespace, req.ReleaseName
		},
		retarget: func(req SharedAlertmanagerRequest, clusterID, namespace, releaseName string) SharedAlertmanagerRequest {
			return SharedAlertmanagerRequest{
				ManagementClusterID: clusterID,
				Namespace:           namespace,
				ReleaseName:         releaseName,
				ChartVersion:        req.ChartVersion,
				Replicas:            req.Replicas,
				StorageClass:        req.StorageClass,
				StorageSize:         req.StorageSize,
			}
		},
		statusFields: func(metadata map[string]any, backend sqlc.MonitoringBackend) map[string]any {
			return map[string]any{
				"status":              defaultString(stringFromMap(metadata, "status"), "not_configured"),
				"managementClusterId": stringFromMap(metadata, "managementClusterId"),
				"namespace":           stringFromMap(metadata, "namespace"),
				"releaseName":         stringFromMap(metadata, "releaseName"),
				"chartVersion":        stringFromMap(metadata, "chartVersion"),
				"replicas":            metadata["replicas"],
				"storageClass":        stringFromMap(metadata, "storageClass"),
				"storageSize":         stringFromMap(metadata, "storageSize"),
				"desiredSpecHash":     stringFromMap(metadata, "lastAppliedSpecHash"),
				"managedAssetHashes":  mapFromMapValue(metadata["managedAssetHashes"]),
				"alertingAssetHashes": mapFromMapValue(mapFromMapValue(decodeJSONMap(backend.AuthConfig)["sharedAlertingAssets"])["hashes"]),
			}
		},
	}
}

func (h *MonitoringHandler) sharedGrafanaLifecycle() sharedStackLifecycle[SharedGrafanaRequest] {
	return sharedStackLifecycle[SharedGrafanaRequest]{
		h:              h,
		auditPrefix:    "monitoring.shared_grafana",
		noun:           "Grafana",
		metadataKey:    "sharedGrafana",
		opTargetType:   "shared_grafana",
		chartRepo:      sharedGrafanaChartRepo,
		chartName:      sharedGrafanaChartName,
		defaultRelease: sharedGrafanaDefaultRelease,
		payload: func(ctx context.Context, r *http.Request) (SharedGrafanaRequest, map[string]any, *objectStoreSecretSpec, sqlc.MonitoringBackend, error) {
			req, values, backend, err := h.sharedGrafanaPayload(ctx, r)
			return req, values, nil, backend, err
		},
		replaceRequired: sharedGrafanaReplaceRequired,
		persist:         h.updateSharedGrafanaMetadata,
		enqueue: func(ctx context.Context, userID pgtype.UUID, opType string, req SharedGrafanaRequest, values map[string]any, _ *objectStoreSecretSpec) (sqlc.MonitoringOperation, error) {
			return h.enqueueSharedGrafanaOperation(ctx, userID, opType, req, values)
		},
		target: func(req SharedGrafanaRequest) (string, string, string) {
			return req.ManagementClusterID, req.Namespace, req.ReleaseName
		},
		retarget: func(req SharedGrafanaRequest, clusterID, namespace, releaseName string) SharedGrafanaRequest {
			return SharedGrafanaRequest{
				ManagementClusterID:   clusterID,
				Namespace:             namespace,
				ReleaseName:           releaseName,
				ChartVersion:          req.ChartVersion,
				Replicas:              req.Replicas,
				StorageClass:          req.StorageClass,
				StorageSize:           req.StorageSize,
				IngressHost:           req.IngressHost,
				LogDatasourceURL:      req.LogDatasourceURL,
				AutoRollbackOnFailure: req.AutoRollbackOnFailure,
			}
		},
		statusFields: func(metadata map[string]any, backend sqlc.MonitoringBackend) map[string]any {
			return map[string]any{
				"status":                sharedGrafanaProjectedStatus(metadata, backend),
				"managementClusterId":   stringFromMap(metadata, "managementClusterId"),
				"namespace":             stringFromMap(metadata, "namespace"),
				"releaseName":           stringFromMap(metadata, "releaseName"),
				"chartVersion":          stringFromMap(metadata, "chartVersion"),
				"replicas":              metadata["replicas"],
				"storageClass":          stringFromMap(metadata, "storageClass"),
				"storageSize":           stringFromMap(metadata, "storageSize"),
				"ingressHost":           stringFromMap(metadata, "ingressHost"),
				"logDatasourceUrl":      stringFromMap(metadata, "logDatasourceUrl"),
				"grafanaHost":           stringFromMap(metadata, "grafanaHost"),
				"authMode":              defaultString(stringFromMap(metadata, "authMode"), sharedGrafanaAuthModeClusterIP),
				"autoRollbackOnFailure": boolFromAny(metadata["autoRollbackOnFailure"]),
				"desiredSpecHash":       stringFromMap(metadata, "lastAppliedSpecHash"),
				"managedAssetHashes":    mapFromMapValue(metadata["managedAssetHashes"]),
			}
		},
		precheck: h.sharedGrafanaPrecheck,
	}
}

func (h *MonitoringHandler) sharedLokiLifecycle() sharedStackLifecycle[SharedLokiRequest] {
	return sharedStackLifecycle[SharedLokiRequest]{
		h:              h,
		auditPrefix:    "monitoring.shared_loki",
		noun:           "Loki",
		metadataKey:    "sharedLoki",
		opTargetType:   "shared_loki",
		chartRepo:      sharedLokiChartRepo,
		chartName:      sharedLokiChartName,
		defaultRelease: sharedLokiDefaultRelease,
		payload: func(ctx context.Context, r *http.Request) (SharedLokiRequest, map[string]any, *objectStoreSecretSpec, sqlc.MonitoringBackend, error) {
			req, values, backend, err := h.sharedLokiPayload(ctx, r)
			return req, values, nil, backend, err
		},
		replaceRequired: sharedLokiReplaceRequired,
		persist:         h.updateSharedLokiMetadata,
		enqueue: func(ctx context.Context, userID pgtype.UUID, opType string, req SharedLokiRequest, values map[string]any, _ *objectStoreSecretSpec) (sqlc.MonitoringOperation, error) {
			return h.enqueueSharedLokiOperation(ctx, userID, opType, req, values)
		},
		target: func(req SharedLokiRequest) (string, string, string) {
			return req.ManagementClusterID, req.Namespace, req.ReleaseName
		},
		retarget: func(req SharedLokiRequest, clusterID, namespace, releaseName string) SharedLokiRequest {
			return SharedLokiRequest{
				ManagementClusterID:     clusterID,
				Namespace:               namespace,
				ReleaseName:             releaseName,
				ChartVersion:            req.ChartVersion,
				StorageConfigID:         req.StorageConfigID,
				ObjectStorageSecretName: req.ObjectStorageSecretName,
				IngestHostname:          req.IngestHostname,
				StorageClass:            req.StorageClass,
				WalStorageSize:          req.WalStorageSize,
				Mode:                    req.Mode,
				Retention:               req.Retention,
				SkipDiskCheck:           req.SkipDiskCheck,
				AutoRollbackOnFailure:   req.AutoRollbackOnFailure,
			}
		},
		statusFields: func(metadata map[string]any, _ sqlc.MonitoringBackend) map[string]any {
			release := defaultString(stringFromMap(metadata, "releaseName"), sharedLokiDefaultRelease)
			ns := defaultString(stringFromMap(metadata, "namespace"), "monitoring")
			queryURL, authURL := lokiDerivedURLs(release, ns)
			return map[string]any{
				"status":                  defaultString(stringFromMap(metadata, "status"), "not_configured"),
				"managementClusterId":     stringFromMap(metadata, "managementClusterId"),
				"namespace":               ns,
				"releaseName":             release,
				"chartVersion":            stringFromMap(metadata, "chartVersion"),
				"storageConfigId":         stringFromMap(metadata, "storageConfigId"),
				"objectStorageSecretName": stringFromMap(metadata, "objectStorageSecretName"),
				"ingestHostname":          stringFromMap(metadata, "ingestHostname"),
				"ingestPublic":            false,
				"storageClass":            stringFromMap(metadata, "storageClass"),
				"walStorageSize":          stringFromMap(metadata, "walStorageSize"),
				"mode":                    stringFromMap(metadata, "mode"),
				"retention":               stringFromMap(metadata, "retention"),
				"skipDiskCheck":           boolFromAny(metadata["skipDiskCheck"]),
				"autoRollbackOnFailure":   boolFromAny(metadata["autoRollbackOnFailure"]),
				"computedLokiPrefix":      stringFromMap(metadata, "computedLokiPrefix"),
				"lastSizerVerdict":        metadata["lastSizerVerdict"],
				"queryUrl":                defaultString(stringFromMap(metadata, "queryUrl"), queryURL),
				"authUrl":                 defaultString(stringFromMap(metadata, "authUrl"), authURL),
				"desiredSpecHash":         stringFromMap(metadata, "lastAppliedSpecHash"),
			}
		},
		precheck: h.sharedLokiPrecheck,
	}
}

// The twenty-four exported entry points. Each is a route target and nothing else —
// there is no body here to leave a check out of.

func (h *MonitoringHandler) PreviewSharedThanosStack(w http.ResponseWriter, r *http.Request) {
	h.sharedThanosLifecycle().preview(w, r)
}

func (h *MonitoringHandler) InstallSharedThanosStack(w http.ResponseWriter, r *http.Request) {
	h.sharedThanosLifecycle().install(w, r)
}

func (h *MonitoringHandler) UpgradeSharedThanosStack(w http.ResponseWriter, r *http.Request) {
	h.sharedThanosLifecycle().upgrade(w, r)
}

func (h *MonitoringHandler) ReplaceSharedThanosStack(w http.ResponseWriter, r *http.Request) {
	h.sharedThanosLifecycle().replace(w, r)
}

func (h *MonitoringHandler) UninstallSharedThanosStack(w http.ResponseWriter, r *http.Request) {
	h.sharedThanosLifecycle().uninstall(w, r)
}

func (h *MonitoringHandler) GetSharedThanosStatus(w http.ResponseWriter, r *http.Request) {
	h.sharedThanosLifecycle().status(w, r)
}

func (h *MonitoringHandler) PreviewSharedAlertmanager(w http.ResponseWriter, r *http.Request) {
	h.sharedAlertmanagerLifecycle().preview(w, r)
}

func (h *MonitoringHandler) InstallSharedAlertmanager(w http.ResponseWriter, r *http.Request) {
	h.sharedAlertmanagerLifecycle().install(w, r)
}

func (h *MonitoringHandler) UpgradeSharedAlertmanager(w http.ResponseWriter, r *http.Request) {
	h.sharedAlertmanagerLifecycle().upgrade(w, r)
}

func (h *MonitoringHandler) ReplaceSharedAlertmanager(w http.ResponseWriter, r *http.Request) {
	h.sharedAlertmanagerLifecycle().replace(w, r)
}

func (h *MonitoringHandler) UninstallSharedAlertmanager(w http.ResponseWriter, r *http.Request) {
	h.sharedAlertmanagerLifecycle().uninstall(w, r)
}

func (h *MonitoringHandler) GetSharedAlertmanagerStatus(w http.ResponseWriter, r *http.Request) {
	h.sharedAlertmanagerLifecycle().status(w, r)
}

func (h *MonitoringHandler) PreviewSharedGrafanaStack(w http.ResponseWriter, r *http.Request) {
	h.sharedGrafanaLifecycle().preview(w, r)
}

func (h *MonitoringHandler) InstallSharedGrafanaStack(w http.ResponseWriter, r *http.Request) {
	h.sharedGrafanaLifecycle().install(w, r)
}

func (h *MonitoringHandler) UpgradeSharedGrafanaStack(w http.ResponseWriter, r *http.Request) {
	h.sharedGrafanaLifecycle().upgrade(w, r)
}

func (h *MonitoringHandler) ReplaceSharedGrafanaStack(w http.ResponseWriter, r *http.Request) {
	h.sharedGrafanaLifecycle().replace(w, r)
}

func (h *MonitoringHandler) UninstallSharedGrafanaStack(w http.ResponseWriter, r *http.Request) {
	h.sharedGrafanaLifecycle().uninstall(w, r)
}

func (h *MonitoringHandler) GetSharedGrafanaStatus(w http.ResponseWriter, r *http.Request) {
	h.sharedGrafanaLifecycle().status(w, r)
}

func (h *MonitoringHandler) PreviewSharedLokiStack(w http.ResponseWriter, r *http.Request) {
	h.sharedLokiLifecycle().preview(w, r)
}

func (h *MonitoringHandler) InstallSharedLokiStack(w http.ResponseWriter, r *http.Request) {
	h.sharedLokiLifecycle().install(w, r)
}

func (h *MonitoringHandler) UpgradeSharedLokiStack(w http.ResponseWriter, r *http.Request) {
	h.sharedLokiLifecycle().upgrade(w, r)
}

func (h *MonitoringHandler) ReplaceSharedLokiStack(w http.ResponseWriter, r *http.Request) {
	h.sharedLokiLifecycle().replace(w, r)
}

func (h *MonitoringHandler) UninstallSharedLokiStack(w http.ResponseWriter, r *http.Request) {
	h.sharedLokiLifecycle().uninstall(w, r)
}

func (h *MonitoringHandler) GetSharedLokiStatus(w http.ResponseWriter, r *http.Request) {
	h.sharedLokiLifecycle().status(w, r)
}

func (h *MonitoringHandler) sharedThanosPayload(ctx context.Context, r *http.Request) (SharedThanosStackRequest, map[string]any, objectStoreSecretSpec, sqlc.MonitoringBackend, error) {
	if h.queries == nil {
		return SharedThanosStackRequest{}, nil, objectStoreSecretSpec{}, sqlc.MonitoringBackend{}, fmt.Errorf("monitoring store not configured")
	}
	if h.helm == nil {
		return SharedThanosStackRequest{}, nil, objectStoreSecretSpec{}, sqlc.MonitoringBackend{}, fmt.Errorf("helm requester not configured")
	}

	var req SharedThanosStackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		return SharedThanosStackRequest{}, nil, objectStoreSecretSpec{}, sqlc.MonitoringBackend{}, fmt.Errorf("invalid JSON body")
	}
	if req.ManagementClusterID == "" {
		req.ManagementClusterID = r.URL.Query().Get("clusterId")
	}
	if req.ManagementClusterID == "" {
		return SharedThanosStackRequest{}, nil, objectStoreSecretSpec{}, sqlc.MonitoringBackend{}, fmt.Errorf("managementClusterId is required")
	}
	if req.Namespace == "" {
		req.Namespace = "monitoring"
	}
	if req.ReleaseName == "" {
		req.ReleaseName = "thanos"
	}
	if req.ChartVersion == "" {
		req.ChartVersion = "1.23.0"
	}
	if req.QueryReplicas <= 0 {
		req.QueryReplicas = 2
	}
	if req.StoreGatewayReplicas <= 0 {
		req.StoreGatewayReplicas = 1
	}
	if req.CompactorReplicas <= 0 {
		req.CompactorReplicas = 1
	}
	if req.StorageConfigID == "" {
		return SharedThanosStackRequest{}, nil, objectStoreSecretSpec{}, sqlc.MonitoringBackend{}, fmt.Errorf("storageConfigId is required")
	}

	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return SharedThanosStackRequest{}, nil, objectStoreSecretSpec{}, sqlc.MonitoringBackend{}, fmt.Errorf("default monitoring backend is not configured")
	}
	// nil authorizer, deliberately: sharedStackLifecycle's preamble has already
	// required a FLEET-WIDE monitoring grant (authorizeGlobalAction against
	// uuid.Nil) to reach this payload builder, which is a strictly higher bar
	// than clusterStorageConfigAuthorizer's most permissive clause, and the
	// secret lands on the management cluster rather than a tenant's.
	secretSpec, err := h.objectStoreSecretSpec(ctx, req.StorageConfigID, req.ObjectStorageSecretName, req.ReleaseName+"-objstore", nil)
	if err != nil {
		return SharedThanosStackRequest{}, nil, objectStoreSecretSpec{}, sqlc.MonitoringBackend{}, err
	}
	req.ObjectStorageSecretName = secretSpec.Name

	values := map[string]any{
		"objstoreConfig": map[string]any{
			"create": false,
			"name":   secretSpec.Name,
			"key":    secretSpec.Key,
		},
		"query": map[string]any{
			"enabled":            true,
			"replicas":           req.QueryReplicas,
			"enableDnsDiscovery": false,
		},
		"queryFrontend": map[string]any{
			"enabled": true,
		},
		"bucketWeb": map[string]any{
			"enabled": true,
		},
		"compact": map[string]any{
			"enabled": true,
			"persistence": map[string]any{
				"enabled": true,
				"size":    "20Gi",
			},
		},
		"storeGateway": map[string]any{
			"enabled":  true,
			"replicas": req.StoreGatewayReplicas,
			"persistence": map[string]any{
				"enabled": true,
				"size":    "20Gi",
			},
		},
		"rule": map[string]any{
			"enabled":  true,
			"replicas": 1,
			"rules": map[string]any{
				"create": false,
				"name":   "astronomer-ruler-rules",
			},
		},
		"receive": map[string]any{
			"enabled": false,
		},
		"metrics": map[string]any{
			"enabled": true,
		},
	}
	if backend.AlertmanagerUrl != "" {
		values["rule"].(map[string]any)["alertmanagersConfig"] = map[string]any{
			"create": false,
			"name":   "astronomer-thanos-rule-alertmanagers",
			"key":    "config",
		}
	}
	return req, values, secretSpec, backend, nil
}

// sharedStackMetadata reads one family's deployment metadata out of the shared
// monitoring_backends.auth_config bag. A missing or malformed entry is an empty
// map, never nil-panicking downstream.
func sharedStackMetadata(backend sqlc.MonitoringBackend, key string) map[string]any {
	authCfg := decodeJSONMap(backend.AuthConfig)
	raw, ok := authCfg[key]
	if !ok {
		return map[string]any{}
	}
	metadata, ok := raw.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return metadata
}

// sharedThanosMetadata is sharedStackMetadata bound to the Thanos family, kept
// for the one caller outside this file (alerting.go, resolving the ruler's
// target cluster). The lifecycle driver reads the key off its own config.
func sharedThanosMetadata(backend sqlc.MonitoringBackend) map[string]any {
	return sharedStackMetadata(backend, "sharedThanos")
}

func (h *MonitoringHandler) updateSharedThanosMetadata(ctx context.Context, backend sqlc.MonitoringBackend, req SharedThanosStackRequest, status string) error {
	if h.queries == nil {
		return nil
	}
	appliedSpecHash := specHash(map[string]any{
		"managementClusterId":     req.ManagementClusterID,
		"namespace":               defaultString(req.Namespace, "monitoring"),
		"releaseName":             defaultString(req.ReleaseName, "thanos"),
		"storageConfigId":         req.StorageConfigID,
		"objectStorageSecretName": req.ObjectStorageSecretName,
		"chartVersion":            req.ChartVersion,
		"queryReplicas":           req.QueryReplicas,
		"storeGatewayReplicas":    req.StoreGatewayReplicas,
		"compactorReplicas":       req.CompactorReplicas,
		"autoRollbackOnFailure":   boolPtrValue(req.AutoRollbackOnFailure),
	})
	// RMW site (migration 146): this mutates a NON-secret key
	// (sharedThanos deployment metadata) and must not lose the credential
	// stored alongside it. Resolve first; a failure aborts the write.
	authCfg, err := resolveMonitoringBackendAuthConfig(backend, h.monitoringDecryptor())
	if err != nil {
		return fmt.Errorf("resolve monitoring backend auth_config: %w", err)
	}
	authCfg["sharedThanos"] = map[string]any{
		"managementClusterId":     req.ManagementClusterID,
		"namespace":               defaultString(req.Namespace, "monitoring"),
		"releaseName":             defaultString(req.ReleaseName, "thanos"),
		"storageConfigId":         req.StorageConfigID,
		"objectStorageSecretName": req.ObjectStorageSecretName,
		"status":                  status,
		"chartVersion":            req.ChartVersion,
		"queryReplicas":           req.QueryReplicas,
		"storeGatewayReplicas":    req.StoreGatewayReplicas,
		"compactorReplicas":       req.CompactorReplicas,
		"lastAppliedSpecHash":     appliedSpecHash,
		"managedAssetHashes": map[string]any{
			"objstoreSecret": specHash(map[string]any{
				"name": req.ObjectStorageSecretName,
				"id":   req.StorageConfigID,
			}),
		},
		"updatedAt": time.Now().UTC().Format(time.RFC3339),
	}
	params := sqlc.UpsertDefaultMonitoringBackendParams{
		BackendType:        backend.BackendType,
		QueryUrl:           defaultSharedThanosQueryURL(backend.QueryUrl, req),
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

func (h *MonitoringHandler) updateSharedAlertmanagerMetadata(ctx context.Context, backend sqlc.MonitoringBackend, req SharedAlertmanagerRequest, status string) error {
	if h.queries == nil {
		return nil
	}
	appliedSpecHash := specHash(map[string]any{
		"managementClusterId":   req.ManagementClusterID,
		"namespace":             defaultString(req.Namespace, "monitoring"),
		"releaseName":           defaultString(req.ReleaseName, "astronomer-alertmanager"),
		"chartVersion":          req.ChartVersion,
		"replicas":              req.Replicas,
		"storageClass":          req.StorageClass,
		"storageSize":           req.StorageSize,
		"autoRollbackOnFailure": boolPtrValue(req.AutoRollbackOnFailure),
	})
	// RMW site (migration 146): same rule as updateSharedThanosMetadata — a
	// non-secret metadata stamp must not be able to delete the credential.
	authCfg, err := resolveMonitoringBackendAuthConfig(backend, h.monitoringDecryptor())
	if err != nil {
		return fmt.Errorf("resolve monitoring backend auth_config: %w", err)
	}
	authCfg["sharedAlertmanager"] = map[string]any{
		"managementClusterId": req.ManagementClusterID,
		"namespace":           defaultString(req.Namespace, "monitoring"),
		"releaseName":         defaultString(req.ReleaseName, "astronomer-alertmanager"),
		"status":              status,
		"chartVersion":        req.ChartVersion,
		"replicas":            req.Replicas,
		"storageClass":        req.StorageClass,
		"storageSize":         req.StorageSize,
		"lastAppliedSpecHash": appliedSpecHash,
		"updatedAt":           time.Now().UTC().Format(time.RFC3339),
	}
	params := sqlc.UpsertDefaultMonitoringBackendParams{
		BackendType:        backend.BackendType,
		QueryUrl:           backend.QueryUrl,
		AlertmanagerUrl:    defaultSharedAlertmanagerURL(backend.AlertmanagerUrl, req),
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

func (h *MonitoringHandler) sharedAlertmanagerPayload(ctx context.Context, r *http.Request) (SharedAlertmanagerRequest, map[string]any, sqlc.MonitoringBackend, error) {
	if h.queries == nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("monitoring store not configured")
	}
	if h.helm == nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("helm requester not configured")
	}

	var req SharedAlertmanagerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("invalid JSON body")
	}
	if req.ManagementClusterID == "" {
		req.ManagementClusterID = r.URL.Query().Get("clusterId")
	}
	if req.ManagementClusterID == "" {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("managementClusterId is required")
	}
	if req.Namespace == "" {
		req.Namespace = "monitoring"
	}
	if req.ReleaseName == "" {
		req.ReleaseName = "astronomer-alertmanager"
	}
	if req.ChartVersion == "" {
		req.ChartVersion = "1.18.0"
	}
	if req.Replicas <= 0 {
		req.Replicas = 1
	}
	if req.StorageSize == "" {
		req.StorageSize = "2Gi"
	}

	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("default monitoring backend is not configured")
	}
	channels, err := h.queries.ListNotificationChannels(ctx, sqlc.ListNotificationChannelsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, err
	}
	rules, err := h.queries.ListAlertRules(ctx, sqlc.ListAlertRulesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, err
	}
	routing, err := h.renderSharedAlertmanagerConfig(ctx, channels, rules)
	if err != nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, err
	}
	var config map[string]any
	if err := yaml.Unmarshal([]byte(routing), &config); err != nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("failed to parse alertmanager config")
	}

	persistence := map[string]any{"enabled": true, "size": req.StorageSize}
	if req.StorageClass != "" {
		persistence["storageClass"] = req.StorageClass
	}
	values := map[string]any{
		"replicaCount": req.Replicas,
		"persistence":  persistence,
		"config":       config,
		"configmapReload": map[string]any{
			"enabled": true,
		},
	}
	return req, values, backend, nil
}

func (h *MonitoringHandler) renderSharedAlertmanagerConfig(ctx context.Context, channels []sqlc.NotificationChannel, rules []sqlc.AlertRule) (string, error) {
	// Load every rule<->channel link for the rule set in ONE query and build a
	// channel_id -> set(rule_id) map, instead of the old N+1 that ran
	// ListChannelsForAlertRule for every rule on every shared Alertmanager
	// Preview/Install/Upgrade/Replace (O(channels x rules) round-trips). Mirrors
	// AlertingHandler.renderAlertmanagerConfig.
	channelRuleSet := map[uuid.UUID]map[uuid.UUID]bool{}
	if len(rules) > 0 {
		ruleIDs := make([]uuid.UUID, 0, len(rules))
		for _, rule := range rules {
			ruleIDs = append(ruleIDs, rule.ID)
		}
		links, err := h.queries.ListAlertRuleChannelsByRules(ctx, ruleIDs)
		if err != nil {
			return "", err
		}
		for _, link := range links {
			set := channelRuleSet[link.NotificationChannelID]
			if set == nil {
				set = map[uuid.UUID]bool{}
				channelRuleSet[link.NotificationChannelID] = set
			}
			set[link.AlertRuleID] = true
		}
	}

	receivers := []map[string]any{{"name": "null"}}
	routes := []map[string]any{}
	for _, channel := range channels {
		if !channel.Enabled {
			continue
		}
		receiverName := "channel-" + channel.ID.String()
		receiver := map[string]any{"name": receiverName}
		cfg := decodeJSONMap(channel.Configuration)
		switch strings.ToLower(channel.ChannelType) {
		case "slack", "webhook":
			if webhook, ok := firstConfigString(cfg, "url", "webhook_url"); ok {
				receiver["webhook_configs"] = []map[string]any{{"url": webhook, "send_resolved": true}}
			}
		case "email":
			if email, ok := firstConfigString(cfg, "email", "address"); ok {
				receiver["email_configs"] = []map[string]any{{"to": email, "send_resolved": true}}
			}
		default:
			continue
		}
		receivers = append(receivers, receiver)
		for _, rule := range rulesForChannel(rules, channelRuleSet[channel.ID]) {
			routes = append(routes, map[string]any{
				"receiver": receiverName,
				"matchers": []string{fmt.Sprintf(`astronomer_rule_id="%s"`, rule.ID.String())},
				"continue": true,
			})
		}
	}
	payload := map[string]any{
		"global": map[string]any{
			"resolve_timeout": "5m",
		},
		"route": map[string]any{
			"receiver": "null",
			"group_by": []string{"alertname", "astronomer_rule_id", "cluster"},
			// Defaults match platform_settings alertmanager.* (DIR-08); monitoring
			// stack render does not currently thread SettingsCache, so keep the
			// same registry defaults here for parity with AlertingHandler.
			"group_wait":      "30s",
			"group_interval":  "5m",
			"repeat_interval": "3h",
			"routes":          routes,
		},
		"receivers": receivers,
	}
	raw, err := yaml.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func defaultSharedThanosQueryURL(current string, req SharedThanosStackRequest) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return fmt.Sprintf("http://%s-query-frontend.%s.svc.cluster.local:9090", defaultString(req.ReleaseName, "thanos"), defaultString(req.Namespace, "monitoring"))
}

func defaultSharedAlertmanagerURL(current string, req SharedAlertmanagerRequest) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:9093", defaultString(req.ReleaseName, "astronomer-alertmanager"), defaultString(req.Namespace, "monitoring"))
}

func sharedAlertmanagerReplaceRequired(metadata map[string]any, req SharedAlertmanagerRequest) (bool, []string) {
	if len(metadata) == 0 || stringFromMap(metadata, "status") == "not_configured" || stringFromMap(metadata, "status") == "uninstalled" {
		return false, nil
	}
	reasons := []string{}
	if current := stringFromMap(metadata, "namespace"); current != "" && current != req.Namespace {
		reasons = append(reasons, "namespace change")
	}
	if current := stringFromMap(metadata, "releaseName"); current != "" && current != req.ReleaseName {
		reasons = append(reasons, "release name change")
	}
	if current := stringFromMap(metadata, "storageClass"); current != req.StorageClass {
		reasons = append(reasons, "storage class change")
	}
	if current := stringFromMap(metadata, "storageSize"); current != "" && current != req.StorageSize {
		reasons = append(reasons, "storage size change")
	}
	return len(reasons) > 0, reasons
}

func sharedThanosReplaceRequired(metadata map[string]any, req SharedThanosStackRequest) (bool, []string) {
	if len(metadata) == 0 || stringFromMap(metadata, "status") == "not_configured" || stringFromMap(metadata, "status") == "uninstalled" {
		return false, nil
	}
	reasons := []string{}
	if current := stringFromMap(metadata, "namespace"); current != "" && current != req.Namespace {
		reasons = append(reasons, "namespace change")
	}
	if current := stringFromMap(metadata, "releaseName"); current != "" && current != req.ReleaseName {
		reasons = append(reasons, "release name change")
	}
	if current := stringFromMap(metadata, "storageConfigId"); current != req.StorageConfigID {
		reasons = append(reasons, "object storage configuration change")
	}
	if current := stringFromMap(metadata, "objectStorageSecretName"); current != "" && current != req.ObjectStorageSecretName {
		reasons = append(reasons, "object storage secret change")
	}
	return len(reasons) > 0, reasons
}
