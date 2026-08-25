package tasks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const NodeOperationType = "node:operation"

const (
	nodeOperationTimeout = 15 * time.Minute
	nodeOperationLease   = 17 * time.Minute
)

type NodeOperationPayload struct {
	OperationID string `json:"operation_id"`
	Generation  int64  `json:"generation"`
}

func NewNodeOperationTask(operationID uuid.UUID, generation int64) (*asynq.Task, error) {
	if operationID == uuid.Nil || generation <= 0 {
		return nil, errors.New("node operation requires operation_id and generation")
	}
	payload, err := json.Marshal(NodeOperationPayload{OperationID: operationID.String(), Generation: generation})
	if err != nil {
		return nil, errors.New("marshal node operation task")
	}
	return asynq.NewTask(NodeOperationType, payload, asynq.MaxRetry(8), asynq.Timeout(nodeOperationTimeout)), nil
}

type nodeOperationTaskQuerier interface {
	ClaimNodeOperationGeneration(context.Context, sqlc.ClaimNodeOperationGenerationParams) (sqlc.NodeOperation, error)
	GetNodeOperation(context.Context, uuid.UUID) (sqlc.NodeOperation, error)
	UpdateNodeOperationProgress(context.Context, sqlc.UpdateNodeOperationProgressParams) (sqlc.NodeOperation, error)
	MarkNodeOperationSucceeded(context.Context, sqlc.MarkNodeOperationSucceededParams) (sqlc.NodeOperation, error)
	MarkNodeOperationBlocked(context.Context, sqlc.MarkNodeOperationBlockedParams) (sqlc.NodeOperation, error)
	MarkNodeOperationRetrying(context.Context, sqlc.MarkNodeOperationRetryingParams) (sqlc.NodeOperation, error)
	MarkNodeOperationFailed(context.Context, sqlc.MarkNodeOperationFailedParams) (sqlc.NodeOperation, error)
}

type nodeOperationParameters struct {
	Key                string `json:"key,omitempty"`
	Value              string `json:"value,omitempty"`
	Effect             string `json:"effect,omitempty"`
	IgnoreDaemonSets   bool   `json:"ignore_daemonsets,omitempty"`
	DeleteEmptyDirData bool   `json:"delete_empty_dir_data,omitempty"`
	GracePeriodSeconds *int64 `json:"grace_period_seconds,omitempty"`
	Force              bool   `json:"force,omitempty"`
}

type nodeOperationPod struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Reason    string `json:"reason,omitempty"`
}

type nodeOperationProgress struct {
	Cordoned bool               `json:"cordoned,omitempty"`
	Evicted  []nodeOperationPod `json:"evicted,omitempty"`
	Skipped  []nodeOperationPod `json:"skipped,omitempty"`
	Failed   []nodeOperationPod `json:"failed,omitempty"`
	Blockers []string           `json:"blockers,omitempty"`
}

type nodeOperationResource struct {
	Metadata struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Spec struct {
		Taints []map[string]any `json:"taints"`
	} `json:"spec"`
}

type nodeOperationPodList struct {
	Items []nodeOperationPodResource `json:"items"`
}

type nodeOperationPodResource struct {
	Metadata struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace"`
		Annotations       map[string]string `json:"annotations"`
		DeletionTimestamp string            `json:"deletionTimestamp"`
		OwnerReferences   []struct {
			Kind string `json:"kind"`
		} `json:"ownerReferences"`
	} `json:"metadata"`
	Spec struct {
		Volumes []struct {
			EmptyDir map[string]any `json:"emptyDir,omitempty"`
		} `json:"volumes"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

func HandleNodeOperation(ctx context.Context, task *asynq.Task) error {
	if task == nil {
		return asynq.SkipRetry
	}
	var payload NodeOperationPayload
	if json.Unmarshal(task.Payload(), &payload) != nil {
		return fmt.Errorf("%w: invalid node operation payload", asynq.SkipRetry)
	}
	id, err := uuid.Parse(payload.OperationID)
	if err != nil || payload.Generation <= 0 {
		return fmt.Errorf("%w: invalid node operation identity", asynq.SkipRetry)
	}
	deps := runtimeDependencies(ctx)
	queries, ok := deps.Queries.(nodeOperationTaskQuerier)
	if !ok || queries == nil || deps.K8s == nil || deps.ResourceDecryptor == nil {
		return errors.New("node operation runtime is not configured")
	}
	now := time.Now().UTC()
	operation, err := queries.ClaimNodeOperationGeneration(ctx, sqlc.ClaimNodeOperationGenerationParams{
		ID: id, Generation: payload.Generation, Now: now,
		LockedUntil: pgtype.Timestamptz{Time: now.Add(nodeOperationLease), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := queries.GetNodeOperation(ctx, id)
		if errors.Is(loadErr, pgx.ErrNoRows) {
			return nil
		}
		if loadErr != nil {
			return errors.New("load node operation failed")
		}
		if existing.ObservedGeneration >= payload.Generation || existing.Status == "succeeded" || existing.Status == "blocked" {
			return nil
		}
		if existing.Generation != payload.Generation {
			return fmt.Errorf("%w: stale node operation generation", asynq.SkipRetry)
		}
		return errors.New("node operation lease is held")
	}
	if err != nil {
		return errors.New("claim node operation failed")
	}
	plain, err := deps.ResourceDecryptor.DecryptBytes(operation.ParametersEncrypted)
	if err != nil {
		return failNodeOperation(ctx, queries, operation, nodeOperationProgress{}, "decrypt_failed", true)
	}
	defer zeroBytes(plain)
	var parameters nodeOperationParameters
	if json.Unmarshal(plain, &parameters) != nil {
		return failNodeOperation(ctx, queries, operation, nodeOperationProgress{}, "invalid_parameters", true)
	}
	var progress nodeOperationProgress
	_ = json.Unmarshal(operation.Progress, &progress)
	if operation.Action == "drain" {
		return executeNodeDrain(ctx, deps.K8s, queries, operation, parameters, progress)
	}
	if err := executeNodePatch(ctx, deps.K8s, operation, parameters); err != nil {
		return failNodeOperation(ctx, queries, operation, progress, err.code, err.terminal)
	}
	encoded := encodeNodeProgress(progress)
	_, err = queries.MarkNodeOperationSucceeded(ctx, sqlc.MarkNodeOperationSucceededParams{
		ID: operation.ID, Generation: operation.Generation, Progress: encoded,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errors.New("persist node operation success failed")
	}
	return nil
}

type nodeEffectError struct {
	code     string
	terminal bool
}

func executeNodePatch(ctx context.Context, requester K8sRequester, operation sqlc.NodeOperation, parameters nodeOperationParameters) *nodeEffectError {
	path := "/api/v1/nodes/" + url.PathEscape(operation.NodeName)
	var patch any
	switch operation.Action {
	case "cordon":
		patch = map[string]any{"spec": map[string]any{"unschedulable": true}}
	case "uncordon":
		patch = map[string]any{"spec": map[string]any{"unschedulable": false}}
	case "set_label":
		if parameters.Key == "" {
			return &nodeEffectError{code: "invalid_parameters", terminal: true}
		}
		patch = map[string]any{"metadata": map[string]any{"labels": map[string]any{parameters.Key: parameters.Value}}}
	case "remove_label":
		if parameters.Key == "" {
			return &nodeEffectError{code: "invalid_parameters", terminal: true}
		}
		patch = map[string]any{"metadata": map[string]any{"labels": map[string]any{parameters.Key: nil}}}
	case "set_annotation":
		if parameters.Key == "" {
			return &nodeEffectError{code: "invalid_parameters", terminal: true}
		}
		patch = map[string]any{"metadata": map[string]any{"annotations": map[string]any{parameters.Key: parameters.Value}}}
	case "remove_annotation":
		if parameters.Key == "" {
			return &nodeEffectError{code: "invalid_parameters", terminal: true}
		}
		patch = map[string]any{"metadata": map[string]any{"annotations": map[string]any{parameters.Key: nil}}}
	case "add_taint", "remove_taint":
		return executeNodeTaintPatch(ctx, requester, operation, parameters)
	default:
		return &nodeEffectError{code: "invalid_action", terminal: true}
	}
	return sendNodePatch(ctx, requester, operation.ClusterID.String(), path, patch)
}

func executeNodeTaintPatch(ctx context.Context, requester K8sRequester, operation sqlc.NodeOperation, parameters nodeOperationParameters) *nodeEffectError {
	if parameters.Key == "" || (parameters.Effect != "" && !validNodeTaintEffect(parameters.Effect)) {
		return &nodeEffectError{code: "invalid_parameters", terminal: true}
	}
	path := "/api/v1/nodes/" + url.PathEscape(operation.NodeName)
	response, effectErr := requester.Do(ctx, operation.ClusterID.String(), http.MethodGet, path, nil, map[string]string{"Accept": "application/json"})
	if effectErr != nil || response == nil {
		return &nodeEffectError{code: "tunnel_unreachable"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return classifyNodeHTTPStatus(response.StatusCode)
	}
	var node nodeOperationResource
	if decodeNodeResponse(response, &node) != nil {
		return &nodeEffectError{code: "invalid_kubernetes_response"}
	}
	if node.Metadata.ResourceVersion == "" {
		return &nodeEffectError{code: "invalid_kubernetes_response"}
	}
	next := make([]map[string]any, 0, len(node.Spec.Taints)+1)
	for _, taint := range node.Spec.Taints {
		key, _ := taint["key"].(string)
		effect, _ := taint["effect"].(string)
		matches := key == parameters.Key && (parameters.Effect == "" || effect == parameters.Effect)
		if matches {
			continue
		}
		next = append(next, taint)
	}
	if operation.Action == "add_taint" {
		next = append(next, map[string]any{"key": parameters.Key, "value": parameters.Value, "effect": parameters.Effect})
	}
	var taints any = next
	if len(next) == 0 {
		taints = nil
	}
	return sendNodePatch(ctx, requester, operation.ClusterID.String(), path, map[string]any{
		"metadata": map[string]any{"resourceVersion": node.Metadata.ResourceVersion},
		"spec":     map[string]any{"taints": taints},
	})
}

func sendNodePatch(ctx context.Context, requester K8sRequester, clusterID, path string, patch any) *nodeEffectError {
	body, err := json.Marshal(patch)
	if err != nil {
		return &nodeEffectError{code: "invalid_parameters", terminal: true}
	}
	defer zeroBytes(body)
	response, effectErr := requester.Do(ctx, clusterID, http.MethodPatch, path, body, map[string]string{
		"Accept": "application/json", "Content-Type": "application/merge-patch+json",
	})
	if effectErr != nil || response == nil {
		return &nodeEffectError{code: "tunnel_unreachable"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return classifyNodeHTTPStatus(response.StatusCode)
	}
	return nil
}

func executeNodeDrain(ctx context.Context, requester K8sRequester, queries nodeOperationTaskQuerier, operation sqlc.NodeOperation, parameters nodeOperationParameters, progress nodeOperationProgress) error {
	if effectErr := sendNodePatch(ctx, requester, operation.ClusterID.String(), "/api/v1/nodes/"+url.PathEscape(operation.NodeName), map[string]any{"spec": map[string]any{"unschedulable": true}}); effectErr != nil {
		return failNodeOperation(ctx, queries, operation, progress, effectErr.code, effectErr.terminal)
	}
	progress.Cordoned = true
	if err := persistNodeProgress(ctx, queries, operation, progress); err != nil {
		return err
	}
	response, effectErr := requester.Do(ctx, operation.ClusterID.String(), http.MethodGet,
		"/api/v1/pods?fieldSelector=spec.nodeName="+url.QueryEscape(operation.NodeName), nil,
		map[string]string{"Accept": "application/json"})
	if effectErr != nil || response == nil {
		return failNodeOperation(ctx, queries, operation, progress, "tunnel_unreachable", false)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		classified := classifyNodeHTTPStatus(response.StatusCode)
		return failNodeOperation(ctx, queries, operation, progress, classified.code, classified.terminal)
	}
	var pods nodeOperationPodList
	if decodeNodeResponse(response, &pods) != nil {
		return failNodeOperation(ctx, queries, operation, progress, "invalid_kubernetes_response", false)
	}
	currentPods := make(map[string]struct{}, len(pods.Items))
	for _, pod := range pods.Items {
		currentPods[pod.Metadata.Namespace+"/"+pod.Metadata.Name] = struct{}{}
	}
	for _, failed := range append([]nodeOperationPod(nil), progress.Failed...) {
		if _, stillPresent := currentPods[nodePodKey(failed)]; !stillPresent {
			progress.Failed = removeNodePod(progress.Failed, failed)
			failed.Reason = "no_longer_present"
			progress.Skipped = upsertNodePod(progress.Skipped, failed)
		}
	}
	progress.Blockers = nil
	candidates := make([]nodeOperationPodResource, 0, len(pods.Items))
	evictionPending := false
	for _, pod := range pods.Items {
		ref := nodeOperationPod{Namespace: pod.Metadata.Namespace, Name: pod.Metadata.Name}
		if containsNodePod(progress.Evicted, ref) {
			// A successful Eviction response only acknowledges the request. The
			// drain is complete once a fresh node pod list no longer contains it.
			evictionPending = true
			continue
		}
		switch {
		case ref.Name == "":
		case pod.Metadata.DeletionTimestamp != "":
			ref.Reason = "already_terminating"
			progress.Skipped = upsertNodePod(progress.Skipped, ref)
		case pod.Status.Phase == "Succeeded" || pod.Status.Phase == "Failed":
			ref.Reason = "terminal"
			progress.Skipped = upsertNodePod(progress.Skipped, ref)
		case pod.Metadata.Annotations["kubernetes.io/config.mirror"] != "":
			ref.Reason = "mirror_pod"
			progress.Skipped = upsertNodePod(progress.Skipped, ref)
		case nodePodOwnedByDaemonSet(pod) && parameters.IgnoreDaemonSets:
			ref.Reason = "daemonset"
			progress.Skipped = upsertNodePod(progress.Skipped, ref)
		case nodePodOwnedByDaemonSet(pod):
			progress.Blockers = append(progress.Blockers, nodePodKey(ref)+":daemonset")
		case len(pod.Metadata.OwnerReferences) == 0 && !parameters.Force:
			progress.Blockers = append(progress.Blockers, nodePodKey(ref)+":unmanaged")
		case nodePodHasEmptyDir(pod) && !parameters.DeleteEmptyDirData:
			progress.Blockers = append(progress.Blockers, nodePodKey(ref)+":empty_dir")
		default:
			candidates = append(candidates, pod)
		}
	}
	if len(progress.Blockers) > 0 {
		encoded := encodeNodeProgress(progress)
		_, err := queries.MarkNodeOperationBlocked(ctx, sqlc.MarkNodeOperationBlockedParams{
			ID: operation.ID, Generation: operation.Generation, Progress: encoded, ErrorCode: "drain_blocked",
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return errors.New("persist blocked node drain failed")
		}
		return nil
	}
	for _, pod := range candidates {
		ref := nodeOperationPod{Namespace: pod.Metadata.Namespace, Name: pod.Metadata.Name}
		if containsNodePod(progress.Evicted, ref) {
			continue
		}
		body := map[string]any{
			"apiVersion": "policy/v1", "kind": "Eviction",
			"metadata": map[string]any{"name": ref.Name, "namespace": ref.Namespace},
		}
		if parameters.GracePeriodSeconds != nil {
			body["deleteOptions"] = map[string]any{"gracePeriodSeconds": *parameters.GracePeriodSeconds}
		}
		encodedBody, _ := json.Marshal(body)
		eviction, requestErr := requester.Do(ctx, operation.ClusterID.String(), http.MethodPost,
			"/api/v1/namespaces/"+url.PathEscape(ref.Namespace)+"/pods/"+url.PathEscape(ref.Name)+"/eviction",
			encodedBody, map[string]string{"Accept": "application/json", "Content-Type": "application/json"})
		zeroBytes(encodedBody)
		if requestErr != nil || eviction == nil {
			ref.Reason = "tunnel_unreachable"
			progress.Failed = upsertNodePod(progress.Failed, ref)
			_ = persistNodeProgress(ctx, queries, operation, progress)
			return failNodeOperation(ctx, queries, operation, progress, "tunnel_unreachable", false)
		}
		if eviction.StatusCode >= 200 && eviction.StatusCode < 300 || eviction.StatusCode == http.StatusNotFound {
			progress.Evicted = upsertNodePod(progress.Evicted, ref)
			progress.Failed = removeNodePod(progress.Failed, ref)
			if eviction.StatusCode != http.StatusNotFound {
				evictionPending = true
			}
			if err := persistNodeProgress(ctx, queries, operation, progress); err != nil {
				return err
			}
			continue
		}
		classified := classifyNodeHTTPStatus(eviction.StatusCode)
		if eviction.StatusCode == http.StatusTooManyRequests {
			classified.code = "pdb_blocked"
			classified.terminal = false
		}
		ref.Reason = classified.code
		progress.Failed = upsertNodePod(progress.Failed, ref)
		_ = persistNodeProgress(ctx, queries, operation, progress)
		return failNodeOperation(ctx, queries, operation, progress, classified.code, classified.terminal)
	}
	if evictionPending {
		return failNodeOperation(ctx, queries, operation, progress, "eviction_in_progress", false)
	}
	_, err := queries.MarkNodeOperationSucceeded(ctx, sqlc.MarkNodeOperationSucceededParams{
		ID: operation.ID, Generation: operation.Generation, Progress: encodeNodeProgress(progress),
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errors.New("persist node drain success failed")
	}
	return nil
}

func failNodeOperation(ctx context.Context, queries nodeOperationTaskQuerier, operation sqlc.NodeOperation, progress nodeOperationProgress, code string, terminal bool) error {
	code = sanitizeNodeErrorCode(code)
	if !terminal {
		retried, retryOK := asynq.GetRetryCount(ctx)
		maximum, maximumOK := asynq.GetMaxRetry(ctx)
		terminal = retryOK && maximumOK && retried >= maximum
	}
	var err error
	if terminal {
		_, err = queries.MarkNodeOperationFailed(ctx, sqlc.MarkNodeOperationFailedParams{
			ID: operation.ID, Generation: operation.Generation, Progress: encodeNodeProgress(progress), ErrorCode: code,
		})
	} else {
		_, err = queries.MarkNodeOperationRetrying(ctx, sqlc.MarkNodeOperationRetryingParams{
			ID: operation.ID, Generation: operation.Generation, Progress: encodeNodeProgress(progress), ErrorCode: code,
		})
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errors.New("persist node operation failure failed")
	}
	result := errors.New("node operation failed: " + code)
	if terminal {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, result)
	}
	return result
}

func persistNodeProgress(ctx context.Context, queries nodeOperationTaskQuerier, operation sqlc.NodeOperation, progress nodeOperationProgress) error {
	_, err := queries.UpdateNodeOperationProgress(ctx, sqlc.UpdateNodeOperationProgressParams{
		ID: operation.ID, Generation: operation.Generation, Progress: encodeNodeProgress(progress),
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errors.New("persist node operation progress failed")
	}
	return nil
}

func classifyNodeHTTPStatus(status int) *nodeEffectError {
	switch status {
	case http.StatusNotFound:
		return &nodeEffectError{code: "node_not_found", terminal: true}
	case http.StatusForbidden, http.StatusUnauthorized:
		return &nodeEffectError{code: "kubernetes_forbidden", terminal: true}
	case http.StatusUnprocessableEntity, http.StatusBadRequest:
		return &nodeEffectError{code: "kubernetes_rejected", terminal: true}
	case http.StatusTooManyRequests:
		return &nodeEffectError{code: "kubernetes_rate_limited"}
	case http.StatusConflict:
		return &nodeEffectError{code: "kubernetes_conflict"}
	default:
		return &nodeEffectError{code: "kubernetes_unavailable", terminal: status < 500}
	}
}

func sanitizeNodeErrorCode(code string) string {
	switch code {
	case "decrypt_failed", "invalid_parameters", "invalid_action", "tunnel_unreachable",
		"invalid_kubernetes_response", "node_not_found", "kubernetes_forbidden",
		"kubernetes_rejected", "kubernetes_rate_limited", "kubernetes_unavailable",
		"kubernetes_conflict", "pdb_blocked", "drain_blocked", "eviction_in_progress":
		return code
	default:
		return "internal_error"
	}
}

func decodeNodeResponse(response *protocol.K8sResponsePayload, out any) error {
	if response == nil || response.Body == "" {
		return errors.New("empty response")
	}
	body, err := base64.StdEncoding.DecodeString(response.Body)
	if err != nil {
		return err
	}
	defer zeroBytes(body)
	return json.Unmarshal(body, out)
}

func validNodeTaintEffect(effect string) bool {
	return effect == "NoSchedule" || effect == "PreferNoSchedule" || effect == "NoExecute"
}

func nodePodOwnedByDaemonSet(pod nodeOperationPodResource) bool {
	for _, owner := range pod.Metadata.OwnerReferences {
		if owner.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func nodePodHasEmptyDir(pod nodeOperationPodResource) bool {
	for _, volume := range pod.Spec.Volumes {
		if volume.EmptyDir != nil {
			return true
		}
	}
	return false
}

func nodePodKey(pod nodeOperationPod) string { return pod.Namespace + "/" + pod.Name }

func containsNodePod(items []nodeOperationPod, candidate nodeOperationPod) bool {
	key := nodePodKey(candidate)
	for _, item := range items {
		if nodePodKey(item) == key {
			return true
		}
	}
	return false
}

func upsertNodePod(items []nodeOperationPod, candidate nodeOperationPod) []nodeOperationPod {
	return append(removeNodePod(items, candidate), candidate)
}

func removeNodePod(items []nodeOperationPod, candidate nodeOperationPod) []nodeOperationPod {
	key := nodePodKey(candidate)
	out := items[:0]
	for _, item := range items {
		if nodePodKey(item) != key {
			out = append(out, item)
		}
	}
	return out
}

func encodeNodeProgress(progress nodeOperationProgress) json.RawMessage {
	encoded, err := json.Marshal(progress)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}
