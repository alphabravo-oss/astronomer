package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
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
	// persistWith stamps this family's metadata (and status) through the writer
	// supplied by the lifecycle transaction.
	persistWith func(context.Context, monitoringSharedMutationWriter, sqlc.MonitoringBackend, Req, string) error
	// enqueueWith creates the async operation through that same writer. Keeping
	// both callbacks transaction-bound prevents desired metadata without an
	// operation, or an operation without its mandatory audit intent.
	enqueueWith func(context.Context, monitoringSharedMutationWriter, pgtype.UUID, string, Req, map[string]any, *objectStoreSecretSpec) (sqlc.MonitoringOperation, error)
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
	// afterCommit is for non-transactional wakeups and external reconciliation.
	// It is never called until metadata, operation, and audit intent commit.
	afterCommit func(context.Context, string)
}

type monitoringSharedMutationWriter interface {
	monitoringOperationCreator
	GetDefaultMonitoringBackend(context.Context) (sqlc.MonitoringBackend, error)
	UpsertDefaultMonitoringBackend(context.Context, sqlc.UpsertDefaultMonitoringBackendParams) (sqlc.MonitoringBackend, error)
}

var (
	errSharedStackMetadataPersistence = errors.New("shared monitoring metadata persistence failed")
	errSharedStackOperationCreation   = errors.New("shared monitoring operation creation failed")
)

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
	if !RequireOperationIdempotencyKey(w, r) {
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
	clusterID, namespace, releaseName := l.target(req)
	op, err := l.stageMutation(r, backend, req, req, "installing", "install", values, secretSpec, clusterID, namespace, releaseName)
	if err != nil {
		l.respondStageMutationError(w, r, err)
		return
	}
	RespondAcceptedOperation(w, "/api/v1/settings/monitoring/operations/"+op.ID.String()+"/", monitoringOperationResponse(op))
}

func (l sharedStackLifecycle[Req]) upgrade(w http.ResponseWriter, r *http.Request) {
	if !l.h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbUpdate) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
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
	clusterID, namespace, releaseName := l.target(req)
	op, err := l.stageMutation(r, backend, req, req, "updating", "upgrade", values, secretSpec, clusterID, namespace, releaseName)
	if err != nil {
		l.respondStageMutationError(w, r, err)
		return
	}
	RespondAcceptedOperation(w, "/api/v1/settings/monitoring/operations/"+op.ID.String()+"/", monitoringOperationResponse(op))
}

func (l sharedStackLifecycle[Req]) replace(w http.ResponseWriter, r *http.Request) {
	if !l.h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbUpdate) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
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
	// Persist and enqueue the same metadata-defaulted target. A replace with an
	// omitted target must not stamp empty desired state while operating on the
	// previously configured release.
	target := l.retarget(req, clusterID, namespace, releaseName)
	op, err := l.stageMutation(r, backend, target, target, "reinstalled", "replace", values, secretSpec, clusterID, namespace, releaseName)
	if err != nil {
		l.respondStageMutationError(w, r, err)
		return
	}
	RespondAcceptedOperation(w, "/api/v1/settings/monitoring/operations/"+op.ID.String()+"/", monitoringOperationResponse(op))
}

func (l sharedStackLifecycle[Req]) uninstall(w http.ResponseWriter, r *http.Request) {
	if !l.h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbUpdate) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
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
	op, err := l.stageMutation(r, backend, target, target, "uninstalled", "uninstall", nil, nil, clusterID, namespace, releaseName)
	if err != nil {
		l.respondStageMutationError(w, r, err)
		return
	}
	RespondAcceptedOperation(w, "/api/v1/settings/monitoring/operations/"+op.ID.String()+"/", monitoringOperationResponse(op))
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

// stageMutation is the commit boundary for every shared-stack lifecycle
// mutation. Missing transaction wiring fails closed before any state changes.
func (l sharedStackLifecycle[Req]) stageMutation(r *http.Request, backend sqlc.MonitoringBackend, persistReq, operationReq Req, desiredStatus, verb string, values map[string]any, secretSpec *objectStoreSecretSpec, clusterID, namespace, releaseName string) (sqlc.MonitoringOperation, error) {
	ctx := withOperationIdempotency(r, "monitoring")
	mutate := func(q MonitoringMutationTx) (sqlc.MonitoringOperation, error) {
		if err := l.persistWith(r.Context(), q, backend, persistReq, desiredStatus); err != nil {
			return sqlc.MonitoringOperation{}, fmt.Errorf("%w: %w", errSharedStackMetadataPersistence, err)
		}
		op, err := l.enqueueWith(ctx, q, currentUserUUID(r), verb, operationReq, values, secretSpec)
		if err != nil {
			return sqlc.MonitoringOperation{}, fmt.Errorf("%w: %w", errSharedStackOperationCreation, err)
		}
		return op, nil
	}

	op, err := executeMutation(r, l.h.runTx, mutate, func(op sqlc.MonitoringOperation) mutationAuditEvent {
		return mutationAuditEvent{action: l.auditPrefix + "." + verb, resourceType: "monitoring_backend", resourceID: backend.ID.String(), resourceName: backend.BackendType, status: http.StatusAccepted, detail: map[string]any{
			"managementClusterId": clusterID,
			"namespace":           namespace,
			"releaseName":         releaseName,
			"operationId":         op.ID.String(),
		}}
	})
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}

	l.h.TriggerReconcile()
	if l.afterCommit != nil {
		l.afterCommit(r.Context(), desiredStatus)
	}
	return op, nil
}

func (l sharedStackLifecycle[Req]) respondStageMutationError(w http.ResponseWriter, r *http.Request, err error) {
	message := "Failed to create monitoring operation"
	if errors.Is(err, errSharedStackMetadataPersistence) {
		message = l.persistFailure()
	}
	respondMonitoringMutationError(w, r, err, http.StatusInternalServerError, apierror.MonitoringError, message)
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
		persistWith:     h.updateSharedThanosMetadataWith,
		enqueueWith:     h.enqueueSharedThanosOperationWith,
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
		persistWith:     h.updateSharedAlertmanagerMetadataWith,
		enqueueWith: func(ctx context.Context, q monitoringSharedMutationWriter, userID pgtype.UUID, opType string, req SharedAlertmanagerRequest, values map[string]any, _ *objectStoreSecretSpec) (sqlc.MonitoringOperation, error) {
			return h.enqueueSharedAlertmanagerOperationWith(ctx, q, userID, opType, req, values)
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
		persistWith:     h.updateSharedGrafanaMetadataWith,
		enqueueWith: func(ctx context.Context, q monitoringSharedMutationWriter, userID pgtype.UUID, opType string, req SharedGrafanaRequest, values map[string]any, _ *objectStoreSecretSpec) (sqlc.MonitoringOperation, error) {
			return h.enqueueSharedGrafanaOperationWith(ctx, q, userID, opType, req, values)
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
				"logDatasourceUrl":      stringFromMap(metadata, "logDatasourceUrl"),
				"proxyPath":             sharedGrafanaProxyPath,
				"authMode":              sharedGrafanaAuthModeProxy,
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
		persistWith:     h.updateSharedLokiMetadataWith,
		enqueueWith: func(ctx context.Context, q monitoringSharedMutationWriter, userID pgtype.UUID, opType string, req SharedLokiRequest, values map[string]any, _ *objectStoreSecretSpec) (sqlc.MonitoringOperation, error) {
			return h.enqueueSharedLokiOperationWith(ctx, q, userID, opType, req, values)
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
				"ingestPublic":            boolFromAny(metadata["ingestPublic"]),
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
		precheck:    h.sharedLokiPrecheck,
		afterCommit: h.afterSharedLokiMetadataCommit,
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
