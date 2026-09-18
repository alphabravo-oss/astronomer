package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
)

// --- Reconciler internals ---

// loggingOperationEnvelope is the payload format we persist on every
// logging_operations row. The reconciler uses it to find the target cluster
// and to render the ConfigMap body without needing to re-fetch the source
// row (which may have been deleted in the delete case).
type loggingOperationEnvelope struct {
	ClusterID     string          `json:"cluster_id"`
	TargetID      string          `json:"target_id"`
	TargetType    string          `json:"target_type"`
	Name          string          `json:"name"`
	OutputType    string          `json:"output_type,omitempty"`
	Enabled       bool            `json:"enabled"`
	IsSystem      bool            `json:"is_system,omitempty"`
	BearerToken   string          `json:"-"`
	Configuration json.RawMessage `json:"configuration,omitempty"`
	Namespaces    json.RawMessage `json:"namespaces,omitempty"`
	Labels        json.RawMessage `json:"labels,omitempty"`
	Filters       json.RawMessage `json:"filters,omitempty"`
}

func (h *LoggingHandler) enqueueOutputApply(ctx context.Context, output sqlc.LoggingOutput, userID pgtype.UUID) (sqlc.LoggingOperation, error) {
	op, err := createLoggingOutputApplyOperation(ctx, h.queries, output, userID)
	if err == nil {
		h.afterLoggingOperationCommit(op)
	}
	return op, err
}

func createLoggingOutputApplyOperation(ctx context.Context, q loggingOperationCreator, output sqlc.LoggingOutput, userID pgtype.UUID) (sqlc.LoggingOperation, error) {
	if !output.ClusterID.Valid {
		return sqlc.LoggingOperation{}, errors.New("logging output has no cluster_id")
	}
	env := loggingOperationEnvelope{
		ClusterID:     uuid.UUID(output.ClusterID.Bytes).String(),
		TargetID:      output.ID.String(),
		TargetType:    "output",
		Name:          output.Name,
		OutputType:    output.OutputType,
		Enabled:       output.Enabled,
		IsSystem:      output.IsSystem,
		Configuration: stripBearerFromLoggingConfiguration(output.Configuration),
	}
	return createLoggingOperation(ctx, q, "output", output.ID.String(), "apply", env, userID)
}

func createLoggingOutputDeleteOperation(ctx context.Context, q loggingOperationCreator, output sqlc.LoggingOutput, userID pgtype.UUID) (sqlc.LoggingOperation, error) {
	clusterID := ""
	if output.ClusterID.Valid {
		clusterID = uuid.UUID(output.ClusterID.Bytes).String()
	}
	env := loggingOperationEnvelope{
		ClusterID:  clusterID,
		TargetID:   output.ID.String(),
		TargetType: "output",
		Name:       output.Name,
		OutputType: output.OutputType,
	}
	return createLoggingOperation(ctx, q, "output", output.ID.String(), "delete", env, userID)
}

func createLoggingPipelineApplyOperation(ctx context.Context, q loggingOperationCreator, pipeline sqlc.LoggingPipeline, userID pgtype.UUID) (sqlc.LoggingOperation, error) {
	env := loggingOperationEnvelope{
		ClusterID:  pipeline.ClusterID.String(),
		TargetID:   pipeline.ID.String(),
		TargetType: "pipeline",
		Name:       pipeline.Name,
		Enabled:    pipeline.Enabled,
		Namespaces: pipeline.Namespaces,
		Labels:     pipeline.Labels,
		Filters:    pipeline.Filters,
	}
	return createLoggingOperation(ctx, q, "pipeline", pipeline.ID.String(), "apply", env, userID)
}

func createLoggingPipelineDeleteOperation(ctx context.Context, q loggingOperationCreator, pipeline sqlc.LoggingPipeline, userID pgtype.UUID) (sqlc.LoggingOperation, error) {
	env := loggingOperationEnvelope{
		ClusterID:  pipeline.ClusterID.String(),
		TargetID:   pipeline.ID.String(),
		TargetType: "pipeline",
		Name:       pipeline.Name,
	}
	return createLoggingOperation(ctx, q, "pipeline", pipeline.ID.String(), "delete", env, userID)
}

// SetEventBus wires the SSE bus for logging_operation.changed liveness
// events (P4.5). Optional: publishers are fire-and-forget and nil-safe.
func (h *LoggingHandler) SetEventBus(bus *events.Bus) {
	if h == nil {
		return
	}
	h.bus = bus
}

// publishLoggingOperationChanged emits the metadata-only
// logging_operation.changed event after a successful operation-row write.
// The cluster id comes from the persisted payload envelope (best-effort:
// legacy rows without one publish unscoped and are superuser-only via the
// SEC-R07 fail-closed drop).
func (h *LoggingHandler) publishLoggingOperationChanged(op sqlc.LoggingOperation) {
	if h == nil {
		return
	}
	var env loggingOperationEnvelope
	_ = json.Unmarshal(op.Payload, &env)
	events.PublishChanged(h.bus, "logging_operation", env.ClusterID, op.ID.String(), map[string]any{"status": op.Status})
}

func (h *LoggingHandler) afterLoggingOperationCommit(op sqlc.LoggingOperation) {
	if h == nil || op.ID == uuid.Nil {
		return
	}
	h.publishLoggingOperationChanged(op)
	h.TriggerReconcile()
}

type loggingOperationCreator interface {
	CreateLoggingOperation(context.Context, sqlc.CreateLoggingOperationParams) (sqlc.LoggingOperation, error)
}

type idempotentLoggingOperationCreator interface {
	CreateLoggingOperationIdempotent(context.Context, sqlc.CreateLoggingOperationIdempotentParams) (sqlc.LoggingOperation, error)
}

type dispositionLoggingOperationCreator interface {
	CreateLoggingOperationIdempotentWithDisposition(context.Context, sqlc.CreateLoggingOperationIdempotentWithDispositionParams) (sqlc.CreateLoggingOperationIdempotentWithDispositionRow, error)
}

func createLoggingOperation(ctx context.Context, q loggingOperationCreator, targetType, targetKey, operationType string, env loggingOperationEnvelope, userID pgtype.UUID) (sqlc.LoggingOperation, error) {
	payload, err := json.Marshal(env)
	if err != nil {
		return sqlc.LoggingOperation{}, err
	}
	params := sqlc.CreateLoggingOperationParams{
		TargetType:    targetType,
		TargetKey:     targetKey,
		OperationType: operationType,
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	}
	var op sqlc.LoggingOperation
	if idem, ok := operationIdempotencyFromContext(ctx); ok {
		if creator, ok := q.(dispositionLoggingOperationCreator); ok {
			result, createErr := creator.CreateLoggingOperationIdempotentWithDisposition(ctx, sqlc.CreateLoggingOperationIdempotentWithDispositionParams{
				Scope: idem.scope, IdempotencyKey: idem.key, TargetType: params.TargetType, TargetKey: params.TargetKey,
				OperationType: params.OperationType, Payload: params.Payload, Status: params.Status, CreatedByID: params.CreatedByID,
			})
			if createErr != nil {
				return sqlc.LoggingOperation{}, createErr
			}
			if !result.Inserted {
				return sqlc.LoggingOperation{}, errLoggingOperationIdempotencyConflict
			}
			op = result.LoggingOperation
		} else if creator, ok := q.(idempotentLoggingOperationCreator); ok {
			op, err = creator.CreateLoggingOperationIdempotent(ctx, sqlc.CreateLoggingOperationIdempotentParams{
				Scope:          idem.scope,
				IdempotencyKey: idem.key,
				TargetType:     params.TargetType,
				TargetKey:      params.TargetKey,
				OperationType:  params.OperationType,
				Payload:        params.Payload,
				Status:         params.Status,
				CreatedByID:    params.CreatedByID,
			})
			if err == nil && op.ID != uuid.Nil && (op.TargetType != params.TargetType || op.TargetKey != params.TargetKey || op.OperationType != params.OperationType || !jsonPayloadEqual(op.Payload, params.Payload)) {
				return sqlc.LoggingOperation{}, errLoggingOperationIdempotencyConflict
			}
		}
	}
	if op.ID == uuid.Nil && err == nil {
		op, err = q.CreateLoggingOperation(ctx, params)
	}
	return op, err
}

// processPendingOperations walks the pending queue once, coalescing
// duplicate target operations and applying the latest one. Mirrors the
// catalog handler's loop.
func (h *LoggingHandler) processPendingOperations(ctx context.Context) {
	// Claim under the lock, dispatch outside — same pattern as
	// catalog/tools/monitoring. One slow cluster must not block other
	// clusters' logging-config rollouts.
	dispatchClaimed(ctx, h.helmConcurrency, h.claimPendingLoggingOperations(ctx))
}

// claimPendingLoggingOperations holds h.mu just long enough to
// supersede stale targets and mark this tick's claims "running". Each
// returned claimedOp captures the row + the type-specific
// execute/complete/fail callbacks so dispatchClaimed can drive it
// without holding the lock.
func (h *LoggingHandler) claimPendingLoggingOperations(ctx context.Context) []claimedOp {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops, err := h.queries.ListPendingLoggingOperations(ctx, 20)
	if err != nil {
		if h.log != nil {
			h.log.Warn("logging reconciler: list pending failed", "error", err)
		}
		return nil
	}
	return claimLatestOperations(ctx, ops, operationRunnerConfig[sqlc.LoggingOperation]{
		ID:        func(op sqlc.LoggingOperation) uuid.UUID { return op.ID },
		TargetKey: func(op sqlc.LoggingOperation) string { return op.TargetType + ":" + op.TargetKey },
		Status:    func(op sqlc.LoggingOperation) string { return op.Status },
		IsFreshRunning: func(op sqlc.LoggingOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) < time.Minute
		},
		Supersede: func(ctx context.Context, op sqlc.LoggingOperation) {
			h.recordEvent(ctx, op.ID, "info", "queue", "operation superseded by newer desired state", map[string]any{
				"targetType": op.TargetType,
				"targetKey":  op.TargetKey,
			})
			if superseded, serr := h.queries.MarkLoggingOperationSuperseded(ctx, sqlc.MarkLoggingOperationSupersededParams{
				ID:           op.ID,
				ErrorMessage: operationSupersededMessage,
			}); serr == nil {
				h.publishLoggingOperationChanged(superseded)
			}
		},
		MarkRunning: func(ctx context.Context, op sqlc.LoggingOperation) (sqlc.LoggingOperation, error) {
			running, err := h.queries.MarkLoggingOperationRunning(ctx, op.ID)
			if err != nil {
				return sqlc.LoggingOperation{}, err
			}
			h.publishLoggingOperationChanged(running)
			h.recordEvent(ctx, running.ID, "info", "queue", "operation execution started", map[string]any{
				"operationType": running.OperationType,
				"targetType":    running.TargetType,
				"targetKey":     running.TargetKey,
				"attemptCount":  running.AttemptCount,
			})
			return running, nil
		},
		Claimed: func(running sqlc.LoggingOperation) claimedOp {
			return claimedOp{
				ID: running.ID,
				Run: func(ctx context.Context) error {
					return h.executeOperation(ctx, running)
				},
				OnComplete: func(ctx context.Context) {
					h.recordEvent(ctx, running.ID, "info", "complete", "operation completed", map[string]any{})
					if completed, cerr := h.queries.MarkLoggingOperationCompleted(ctx, running.ID); cerr == nil {
						h.publishLoggingOperationChanged(completed)
					}
				},
				OnFailure: func(ctx context.Context, err error) {
					h.recordEvent(ctx, running.ID, "error", "complete", "operation failed", map[string]any{"error": err.Error()})
					if failed, ferr := h.queries.MarkLoggingOperationFailed(ctx, sqlc.MarkLoggingOperationFailedParams{
						ID:           running.ID,
						ErrorMessage: err.Error(),
					}); ferr == nil {
						h.publishLoggingOperationChanged(failed)
					}
					if h.log != nil {
						h.log.Warn("logging operation failed", "id", running.ID.String(), "error", err)
					}
				},
			}
		},
	})
}

// executeOperation renders the ConfigMap and applies (or deletes) it via the
// tunnel K8sRequester. System Loki ingest tokens are written to a member
// Secret and mounted into the baseline fluent-bit release; the ConfigMap
// references bearer_token_file. This code does NOT install Fluent Bit.
func (h *LoggingHandler) executeOperation(ctx context.Context, op sqlc.LoggingOperation) error {
	var env loggingOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	if env.ClusterID == "" {
		return errors.New("operation payload missing cluster_id")
	}
	if h.requester == nil {
		return errors.New("k8s requester not configured")
	}
	if err := h.attachLokiBearer(ctx, &env); err != nil {
		return err
	}
	configMapName := loggingConfigMapName(env.TargetType, env.TargetID)
	switch op.OperationType {
	case "apply":
		data, err := h.renderConfigMapData(env)
		if err != nil {
			return fmt.Errorf("render configmap: %w", err)
		}
		h.recordEvent(ctx, op.ID, "info", "apply", "applying logging configmap", map[string]any{
			"clusterId":     env.ClusterID,
			"namespace":     LoggingNamespace,
			"configMapName": configMapName,
		})
		// Ensure the namespace exists; Fluent Bit installer creates it
		// in steady state but on a fresh cluster the apply would 404.
		if err := ensureNamespace(ctx, h.requester, env.ClusterID, LoggingNamespace); err != nil {
			return fmt.Errorf("ensure namespace: %w", err)
		}
		if err := h.syncMemberLokiIngestSecret(ctx, env); err != nil {
			return err
		}
		if err := applyConfigMap(ctx, h.requester, env.ClusterID, LoggingNamespace, configMapName, data); err != nil {
			return err
		}
		// Refresh the single aggregate config ConfigMap that Fluent Bit actually
		// consumes (via existingConfigMap). The per-target ConfigMap above is
		// kept for provenance/debugging.
		return h.refreshAggregateFluentBitConfig(ctx, env.ClusterID)
	case "delete":
		h.recordEvent(ctx, op.ID, "info", "delete", "deleting logging configmap", map[string]any{
			"clusterId":     env.ClusterID,
			"namespace":     LoggingNamespace,
			"configMapName": configMapName,
		})
		if err := h.syncMemberLokiIngestSecret(ctx, loggingOperationEnvelope{
			ClusterID:  env.ClusterID,
			TargetType: env.TargetType,
			OutputType: env.OutputType,
			IsSystem:   env.IsSystem,
			Enabled:    false,
		}); err != nil {
			return err
		}
		if err := deleteConfigMap(ctx, h.requester, env.ClusterID, LoggingNamespace, configMapName); err != nil {
			return err
		}
		return h.refreshAggregateFluentBitConfig(ctx, env.ClusterID)
	default:
		return fmt.Errorf("unsupported logging operation type: %s", op.OperationType)
	}
}

// fluentBitValuePattern is the safe-character allowlist for values rendered
// into Fluent Bit config keys. Cluster names, label values, namespace names
// — anything that lands in a `Key Value` line — must match this. Anything
// that doesn't gets a `# warning` line instead of the real value, which
// keeps Fluent Bit's parser happy and prevents config injection from a
// malformed cluster name.
var fluentBitValuePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// renderConfigMapData turns the envelope into the ConfigMap data block.
//
// For outputs we render `output.conf` containing the Fluent Bit `[OUTPUT]`
// snippet plus a `meta.json` sidecar. For pipelines we render `pipeline.conf`
// with `[FILTER]` blocks and Match rules, plus `meta.json`. Each block lives
// in its own ConfigMap entry so a sidecar can compose them without parsing a
// concatenated megafile.
//
// Supported output_type values: elasticsearch, loki, s3, stdout. Anything
// else renders a `# unsupported output_type` comment so the operator sees
// the row was received but no live snippet was emitted.
func (h *LoggingHandler) renderConfigMapData(env loggingOperationEnvelope) (map[string]string, error) {
	generatedAt := time.Now().UTC().Format(time.RFC3339)
	meta := map[string]any{
		"id":           env.TargetID,
		"target_type":  env.TargetType,
		"name":         env.Name,
		"cluster_id":   env.ClusterID,
		"enabled":      env.Enabled,
		"generated_at": generatedAt,
	}
	if env.OutputType != "" {
		meta["output_type"] = env.OutputType
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, err
	}
	data := map[string]string{
		"meta.json": string(metaBytes),
	}

	switch env.TargetType {
	case "output":
		data["output.conf"] = renderOutputBlock(env)
	case "pipeline":
		data["pipeline.conf"] = renderPipelineBlock(env)
	default:
		// Unknown target_type — fall back to a comment so the ConfigMap is
		// still structurally present without misleading config text.
		data["unknown.conf"] = "# unsupported target_type " + env.TargetType + "\n"
	}
	return data, nil
}

// renderOutputBlock renders the Fluent Bit `[OUTPUT]` snippet for one
// logging_outputs row. Unknown output_type values produce a comment line
// instead of a block so the reconciler keeps the ConfigMap up to date but
// Fluent Bit doesn't reject the file.
func renderOutputBlock(env loggingOperationEnvelope) string {
	cfg := decodeConfiguration(env.Configuration)
	var b strings.Builder
	b.WriteString("# rendered by astronomer-go logging controller\n")
	b.WriteString("# output: " + safeComment(env.Name) + " (" + env.OutputType + ")\n")
	if !env.Enabled {
		b.WriteString("# note: output is currently disabled\n")
		return b.String()
	}
	switch env.OutputType {
	case "elasticsearch", "opensearch":
		host, port := outputHostPort(cfg, "url", "9200")
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "es")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		writeKV(&b, "Host", host)
		writeKV(&b, "Port", port)
		writeKV(&b, "Index", configString(cfg, "index", "astronomer"))
		if v := configString(cfg, "username", configString(cfg, "http_user", "")); v != "" {
			writeKV(&b, "HTTP_User", v)
		}
		if v := configString(cfg, "password", configString(cfg, "http_passwd", "")); v != "" {
			writeKV(&b, "HTTP_Passwd", v)
		}
		tls := configString(cfg, "tls", "")
		if tls == "" && strings.HasPrefix(strings.ToLower(configString(cfg, "url", "")), "https://") {
			tls = "on"
		}
		if tls != "" {
			writeKV(&b, "tls", tls)
		}
	case "loki":
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "loki")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		writeKV(&b, "Host", configString(cfg, "host", ""))
		portDefault := "3100"
		if env.IsSystem {
			portDefault = "443"
		}
		writeKV(&b, "Port", configString(cfg, "port", portDefault))
		tls := configString(cfg, "tls", "")
		if env.IsSystem && tls == "" {
			tls = "on"
		}
		if tls != "" {
			writeKV(&b, "tls", tls)
		}
		tlsVerify := configString(cfg, "tls.verify", "")
		if tlsVerify == "" && strings.EqualFold(tls, "on") {
			tlsVerify = "on"
		}
		if tlsVerify != "" {
			writeKV(&b, "tls.verify", tlsVerify)
		}
		if env.IsSystem {
			writeKV(&b, "bearer_token_file", fluentBitIngestTokenFile)
		} else if env.BearerToken != "" {
			writeKV(&b, "bearer_token", env.BearerToken)
		}
		labels := configString(cfg, "labels", "")
		if env.IsSystem && env.ClusterID != "" {
			labels = systemLokiLabels(env.ClusterID)
		}
		if labels != "" {
			writeKV(&b, "Labels", labels)
		}
		if env.IsSystem {
			// Keep identifiers needed for a precise drill-down without turning
			// image digests and Pod UIDs into Loki stream-cardinality dimensions.
			// The complete Kubernetes labels and ownerReferences maps remain in
			// the JSON log record produced by the kubernetes filter.
			writeKV(&b, "structured_metadata", "pod_uid=$kubernetes['pod_id'],container_image=$kubernetes['container_image']")
		}
		tenant := configString(cfg, "tenant_id", "")
		if env.IsSystem {
			tenant = env.ClusterID
		}
		if tenant != "" {
			writeKV(&b, "tenant_id", tenant)
		}
	case "s3":
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "s3")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		writeKV(&b, "bucket", configString(cfg, "bucket", ""))
		writeKV(&b, "region", configString(cfg, "region", "us-east-1"))
		if v := configString(cfg, "total_file_size", ""); v != "" {
			writeKV(&b, "total_file_size", v)
		}
		if v := configString(cfg, "upload_timeout", ""); v != "" {
			writeKV(&b, "upload_timeout", v)
		}
		if v := configString(cfg, "s3_key_format", ""); v != "" {
			writeKV(&b, "s3_key_format", v)
		}
	case "stdout":
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "stdout")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		if v := configString(cfg, "format", ""); v != "" {
			writeKV(&b, "Format", v)
		}
	case "splunk":
		host, port := outputHostPort(cfg, "hec_url", "8088")
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "splunk")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		writeKV(&b, "Host", host)
		writeKV(&b, "Port", port)
		if v := configString(cfg, "token", configString(cfg, "splunk_token", "")); v != "" {
			writeKV(&b, "Splunk_Token", v)
		}
		writeKV(&b, "Splunk_Send_Raw", "Off")
		writeKV(&b, "TLS", "On")
	case "datadog":
		site := configString(cfg, "site", "datadoghq.com")
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "datadog")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		writeKV(&b, "Host", "http-intake.logs."+site)
		writeKV(&b, "TLS", "on")
		writeKV(&b, "compress", "gzip")
		if v := configString(cfg, "api_key", ""); v != "" {
			writeKV(&b, "apikey", v)
		}
		if v := configString(cfg, "service", ""); v != "" {
			writeKV(&b, "dd_service", v)
		}
		if v := configString(cfg, "source", ""); v != "" {
			writeKV(&b, "dd_source", v)
		}
		if v := configString(cfg, "tags", ""); v != "" {
			writeKV(&b, "dd_tags", v)
		}
	case "cloudwatch":
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "cloudwatch_logs")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		writeKV(&b, "region", configString(cfg, "region", "us-east-1"))
		writeKV(&b, "log_group_name", configString(cfg, "log_group", "/astronomer/cluster-logs"))
		writeKV(&b, "log_stream_prefix", configString(cfg, "log_stream_prefix", "fluentbit-"))
		writeKV(&b, "auto_create_group", "On")
		if configString(cfg, "access_key", "") != "" {
			// The cloudwatch_logs plugin reads AWS credentials from the Fluent
			// Bit pod's identity (IRSA / instance role), not inline params.
			b.WriteString("# note: access_key/secret_key are supplied via the pod's AWS identity (IRSA/instance role)\n")
		}
	case "syslog":
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "syslog")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		writeKV(&b, "Host", configString(cfg, "host", ""))
		writeKV(&b, "Port", configString(cfg, "port", "514"))
		writeKV(&b, "Mode", configString(cfg, "protocol", "tcp"))
		writeKV(&b, "Syslog_Format", configString(cfg, "format", "rfc5424"))
		writeKV(&b, "Syslog_Maxsize", "2048")
	default:
		b.WriteString("# unsupported output_type " + env.OutputType + "; no [OUTPUT] emitted\n")
	}
	return b.String()
}

// outputHostPort extracts host + port for an output, accepting either a full
// URL field (e.g. "https://host:8088") or separate host/port config keys.
func outputHostPort(cfg map[string]any, urlKey, defaultPort string) (host, port string) {
	port = defaultPort
	if raw := configString(cfg, urlKey, ""); raw != "" {
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
			host = u.Hostname()
			if u.Port() != "" {
				port = u.Port()
			}
			return host, port
		}
	}
	host = configString(cfg, "host", "")
	if p := configString(cfg, "port", ""); p != "" {
		port = p
	}
	return host, port
}

// renderPipelineBlock renders Match rules for the pipeline's namespaces and
// `[FILTER]` blocks for any modify labels / declared filters. Pipelines
// don't emit [OUTPUT] blocks themselves — those come from the linked
// logging_outputs rows, rendered into separate ConfigMaps.
func renderPipelineBlock(env loggingOperationEnvelope) string {
	var b strings.Builder
	b.WriteString("# rendered by astronomer-go logging controller\n")
	b.WriteString("# pipeline: " + safeComment(env.Name) + "\n")
	if !env.Enabled {
		b.WriteString("# note: pipeline is currently disabled\n")
	}

	namespaces := decodeStringList(env.Namespaces)
	patterns := pipelineMatchPatterns(namespaces)
	if len(namespaces) == 0 {
		b.WriteString("# no namespaces declared; matches all kube.* records\n")
	}
	for _, ns := range namespaces {
		if !fluentBitValuePattern.MatchString(ns) {
			b.WriteString("# warning: skipped invalid namespace " + safeComment(ns) + "\n")
			continue
		}
		b.WriteString("# match kube." + ns + ".*\n")
	}

	labels := decodeStringMap(env.Labels)
	if len(labels) > 0 {
		// Sort keys so renders are deterministic — important for unit tests
		// and for avoiding spurious diffs in ConfigMap apply traffic.
		keys := make([]string, 0, len(labels))
		for k := range labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, matchPattern := range patterns {
			b.WriteString("[FILTER]\n")
			writeKV(&b, "Name", "modify")
			writeKV(&b, "Match", matchPattern)
			for _, k := range keys {
				v := labels[k]
				if !fluentBitValuePattern.MatchString(k) || !fluentBitValuePattern.MatchString(v) {
					b.WriteString("# warning: skipped invalid label " + safeComment(k) + "\n")
					continue
				}
				b.WriteString("    Add         " + k + " " + v + "\n")
			}
		}
	}

	for _, f := range decodeFilters(env.Filters) {
		if !fluentBitValuePattern.MatchString(f.Type) {
			b.WriteString("# warning: skipped filter with invalid type " + safeComment(f.Type) + "\n")
			continue
		}
		// Stable iteration order across params for the same reasons as above.
		keys := make([]string, 0, len(f.Params))
		for k := range f.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, matchPattern := range patterns {
			b.WriteString("[FILTER]\n")
			writeKV(&b, "Name", f.Type)
			writeKV(&b, "Match", matchPattern)
			for _, k := range keys {
				v := f.Params[k]
				if !fluentBitValuePattern.MatchString(k) || !fluentBitValuePattern.MatchString(v) {
					b.WriteString("# warning: skipped invalid param " + safeComment(k) + "\n")
					continue
				}
				writeKV(&b, k, v)
			}
		}
	}

	// The rewrite creates a pipeline-specific tag. Output blocks match that
	// exact tag, making the persisted pipeline-output association operational
	// rather than presentation-only. KEEP=true intentionally permits one input
	// record to fan out through multiple overlapping pipelines.
	if env.Enabled {
		if pipelineID, err := uuid.Parse(env.TargetID); err == nil {
			emitterBase := "astronomer_pipeline_" + strings.ReplaceAll(pipelineID.String(), "-", "")
			for i, matchPattern := range patterns {
				b.WriteString("[FILTER]\n")
				writeKV(&b, "Name", "rewrite_tag")
				writeKV(&b, "Match", matchPattern)
				writeKV(&b, "Rule", "$TAG ^.+$ "+pipelineRouteTag(pipelineID)+" true")
				writeKV(&b, "Emitter_Name", fmt.Sprintf("%s_%d", emitterBase, i))
			}
		}
	}
	return b.String()
}

func pipelineMatchPatterns(namespaces []string) []string {
	patterns := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		if fluentBitValuePattern.MatchString(ns) {
			patterns = append(patterns, "kube."+ns+".*")
		}
	}
	if len(patterns) == 0 {
		return []string{"kube.*"}
	}
	return patterns
}

func pipelineRouteTag(pipelineID uuid.UUID) string {
	return "astronomer.pipeline." + pipelineID.String()
}

func withLoggingOutputMatch(configuration json.RawMessage, match string) json.RawMessage {
	cfg := decodeConfiguration(configuration)
	cfg["match"] = match
	rendered, err := json.Marshal(cfg)
	if err != nil {
		return configuration
	}
	return rendered
}

// loggingFilterSpec mirrors the per-filter shape we accept inside a
// pipeline's filters JSON column: {type: "...", params: {k: v, ...}}.
type loggingFilterSpec struct {
	Type   string            `json:"type"`
	Params map[string]string `json:"params"`
}

func decodeFilters(raw json.RawMessage) []loggingFilterSpec {
	if len(raw) == 0 {
		return nil
	}
	var arr []loggingFilterSpec
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	// Tolerate the legacy `{}` default that older rows persist; treat it as
	// "no filters" rather than a parse error.
	return nil
}

func decodeStringList(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil
	}
	return arr
}

func decodeStringMap(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	// Decode into RawMessage first so we can stringify non-string values
	// rather than dropping them silently — operators sometimes pass numeric
	// label values from the UI.
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil
	}
	out := make(map[string]string, len(generic))
	for k, v := range generic {
		switch t := v.(type) {
		case string:
			out[k] = t
		case bool:
			if t {
				out[k] = "true"
			} else {
				out[k] = "false"
			}
		case float64:
			out[k] = fmt.Sprintf("%v", t)
		}
	}
	return out
}

func decodeConfiguration(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	return m
}

func configString(cfg map[string]any, key, fallback string) string {
	if v, ok := cfg[key]; ok {
		switch t := v.(type) {
		case string:
			if t != "" {
				return t
			}
		case float64:
			return fmt.Sprintf("%v", t)
		case bool:
			if t {
				return "true"
			}
			return "false"
		}
	}
	return fallback
}

// writeKV renders one `    Key Value` line in Fluent Bit's classic config
// format. The leading four-space indent matches Fluent Bit's documented
// style; the renderer itself uses tabs (per CLAUDE.md house style) but the
// emitted config is what Fluent Bit will parse, so we keep its conventions.
func writeKV(b *strings.Builder, key, value string) {
	b.WriteString("    ")
	b.WriteString(key)
	b.WriteString(" ")
	b.WriteString(value)
	b.WriteString("\n")
}

// safeComment strips newlines from a string so it can't break out of a
// `# comment` line into the surrounding config.
func safeComment(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

func loggingConfigMapName(targetType, targetID string) string {
	// Kubernetes ConfigMap names must be DNS-1123 subdomain compliant. UUID
	// + "logging-output-" / "logging-pipeline-" prefix gives us 36 + 16 < 63
	// chars and stays lowercase.
	prefix := "logging-" + targetType + "-"
	return prefix + strings.ToLower(targetID)
}

func deleteConfigMap(ctx context.Context, requester K8sRequester, clusterID, namespace, name string) error {
	return deleteCoreV1Resource(ctx, requester, clusterID, namespace, "configmaps", name)
}

func deleteSecret(ctx context.Context, requester K8sRequester, clusterID, namespace, name string) error {
	return deleteCoreV1Resource(ctx, requester, clusterID, namespace, "secrets", name)
}

func deleteCoreV1Resource(ctx context.Context, requester K8sRequester, clusterID, namespace, plural, name string) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/%s/%s", namespace, plural, name)
	resp, err := requester.Do(ctx, clusterID, http.MethodDelete, path, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return ensureSuccess(resp)
}

func ensureNamespace(ctx context.Context, requester K8sRequester, clusterID, name string) error {
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]any{"name": name},
	})
	if err != nil {
		return err
	}
	resp, err := requester.Do(ctx, clusterID, http.MethodPost, "/api/v1/namespaces", body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		// Already exists — that's fine.
		return nil
	}
	return ensureSuccess(resp)
}

func (h *LoggingHandler) recordEvent(ctx context.Context, operationID uuid.UUID, level, stage, message string, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, _ = h.queries.CreateLoggingOperationEvent(ctx, sqlc.CreateLoggingOperationEventParams{
		OperationID: operationID,
		Level:       level,
		Stage:       stage,
		Message:     message,
		Detail:      raw,
	})
}
