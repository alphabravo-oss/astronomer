package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

const systemLoggingOutputName = "Astronomer logs"

// systemLoggingOutputSpec is the safe (non-secret) payload for the per-cluster
// Astronomer Loki destination. Bearer tokens stay in loki_ingest_tokens.
type systemLoggingOutputSpec struct {
	ClusterID uuid.UUID
	Host      string
	Port      string
	Enabled   bool
	CreatedBy pgtype.UUID
}

func systemLoggingOutputConfiguration(clusterID uuid.UUID, host, port string) json.RawMessage {
	if strings.TrimSpace(port) == "" {
		port = "443"
	}
	raw, err := json.Marshal(map[string]any{
		"host":      host,
		"port":      port,
		"tls":       "on",
		"tenant_id": clusterID.String(),
		"labels":    "cluster=" + clusterID.String() + ",job=fluentbit",
	})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

// upsertSystemLoggingOutput creates or updates the single is_system row for
// a member cluster. configuration never contains a bearer token.
func (h *LoggingHandler) upsertSystemLoggingOutput(ctx context.Context, spec systemLoggingOutputSpec) (sqlc.LoggingOutput, error) {
	if h == nil || h.queries == nil {
		return sqlc.LoggingOutput{}, errors.New("logging store not configured")
	}
	return upsertSystemLoggingOutputWith(ctx, h.queries, spec)
}

type systemLoggingOutputWriter interface {
	GetSystemLoggingOutputByCluster(context.Context, pgtype.UUID) (sqlc.LoggingOutput, error)
	CreateLoggingOutput(context.Context, sqlc.CreateLoggingOutputParams) (sqlc.LoggingOutput, error)
	UpdateLoggingOutput(context.Context, sqlc.UpdateLoggingOutputParams) (sqlc.LoggingOutput, error)
}

func upsertSystemLoggingOutputWith(ctx context.Context, q systemLoggingOutputWriter, spec systemLoggingOutputSpec) (sqlc.LoggingOutput, error) {
	if q == nil {
		return sqlc.LoggingOutput{}, errors.New("logging store not configured")
	}
	if spec.ClusterID == uuid.Nil {
		return sqlc.LoggingOutput{}, errors.New("system logging output requires cluster_id")
	}
	cfg := systemLoggingOutputConfiguration(spec.ClusterID, spec.Host, spec.Port)
	cluster := pgtype.UUID{Bytes: spec.ClusterID, Valid: true}
	existing, err := q.GetSystemLoggingOutputByCluster(ctx, cluster)
	if err == nil {
		return q.UpdateLoggingOutput(ctx, sqlc.UpdateLoggingOutputParams{
			ID:            existing.ID,
			Name:          systemLoggingOutputName,
			OutputType:    "loki",
			Configuration: cfg,
			Enabled:       spec.Enabled,
		})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlc.LoggingOutput{}, err
	}
	created, err := q.CreateLoggingOutput(ctx, sqlc.CreateLoggingOutputParams{
		Name:          systemLoggingOutputName,
		OutputType:    "loki",
		Configuration: cfg,
		ClusterID:     cluster,
		Enabled:       spec.Enabled,
		CreatedByID:   spec.CreatedBy,
		IsSystem:      true,
	})
	if err == nil {
		return created, nil
	}
	if !isUniqueViolation(err) {
		return sqlc.LoggingOutput{}, err
	}
	existing, err = q.GetSystemLoggingOutputByCluster(ctx, cluster)
	if err != nil {
		return sqlc.LoggingOutput{}, err
	}
	return q.UpdateLoggingOutput(ctx, sqlc.UpdateLoggingOutputParams{
		ID:            existing.ID,
		Name:          systemLoggingOutputName,
		OutputType:    "loki",
		Configuration: cfg,
		Enabled:       spec.Enabled,
	})
}

// DisableSystemOutputsOnLokiUninstall flips every is_system row to enabled=false
// and re-renders member Fluent Bit ConfigMaps so they no longer emit [OUTPUT].
func (h *LoggingHandler) DisableSystemOutputsOnLokiUninstall(ctx context.Context) error {
	if h == nil || h.queries == nil {
		return nil
	}
	rows, err := h.queries.DisableSystemLoggingOutputs(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, opErr := h.enqueueOutputApply(ctx, row, pgtype.UUID{}); opErr != nil && h.log != nil {
			h.log.Warn("logging: failed to enqueue system output disable", "id", row.ID.String(), "error", opErr)
		}
	}
	return nil
}

func rejectSystemOutputMutation(w http.ResponseWriter, r *http.Request, output sqlc.LoggingOutput, action string) bool {
	if !output.IsSystem {
		return false
	}
	RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden,
		fmt.Sprintf("System logging destinations cannot be %s", action))
	return true
}

type loggingOutputResponse struct {
	sqlc.LoggingOutput
	Capabilities loggingOutputCapabilities `json:"capabilities"`
}

func loggingOutputDTO(o sqlc.LoggingOutput) loggingOutputResponse {
	o.Configuration = redactLoggingOutputConfiguration(o)
	return loggingOutputResponse{
		LoggingOutput: o,
		Capabilities:  loggingCapabilitiesFor(o),
	}
}

func loggingOutputDTOs(outputs []sqlc.LoggingOutput) []loggingOutputResponse {
	out := make([]loggingOutputResponse, len(outputs))
	for i, o := range outputs {
		out[i] = loggingOutputDTO(o)
	}
	return out
}

type loggingPipelineResponse struct {
	sqlc.LoggingPipeline
	OutputIDs   []uuid.UUID `json:"output_ids"`
	OutputNames []string    `json:"output_names"`
}

func loggingPipelineDTO(pipeline sqlc.LoggingPipeline, outputIDs []uuid.UUID, outputNames []string) loggingPipelineResponse {
	if outputIDs == nil {
		outputIDs = []uuid.UUID{}
	}
	if outputNames == nil {
		outputNames = []string{}
	}
	return loggingPipelineResponse{
		LoggingPipeline: pipeline,
		OutputIDs:       outputIDs,
		OutputNames:     outputNames,
	}
}

// loggingPipelineDTOs resolves associations in one query so the fleet and
// cluster list APIs do not regress into an N+1 read pattern.
func (h *LoggingHandler) loggingPipelineDTOs(ctx context.Context, pipelines []sqlc.LoggingPipeline) ([]loggingPipelineResponse, error) {
	responses := make([]loggingPipelineResponse, len(pipelines))
	if len(pipelines) == 0 {
		return responses, nil
	}
	ids := make([]uuid.UUID, len(pipelines))
	for i, pipeline := range pipelines {
		ids[i] = pipeline.ID
		responses[i] = loggingPipelineDTO(pipeline, nil, nil)
	}
	details, err := h.queries.ListLoggingPipelineOutputDetails(ctx, ids)
	if err != nil {
		return nil, err
	}
	index := make(map[uuid.UUID]int, len(pipelines))
	for i, id := range ids {
		index[id] = i
	}
	for _, detail := range details {
		i, ok := index[detail.LoggingPipelineID]
		if !ok {
			continue
		}
		responses[i].OutputIDs = append(responses[i].OutputIDs, detail.LoggingOutputID)
		responses[i].OutputNames = append(responses[i].OutputNames, detail.LoggingOutputName)
	}
	return responses, nil
}

var systemLoggingOutputConfigKeys = []string{"host", "port", "tls", "tenant_id", "labels"}

var loggingSecretConfigKeys = map[string]struct{}{
	"access_key": {}, "access_key_id": {}, "api_key": {}, "application_key": {},
	"bearer_token": {}, "bearerToken": {}, "credentials": {}, "http_passwd": {},
	"password": {}, "private_key": {}, "query_token": {}, "secret": {},
	"secret_access_key": {}, "secret_key": {}, "token": {},
}

func redactLoggingOutputConfiguration(o sqlc.LoggingOutput) json.RawMessage {
	cfg := decodeConfiguration(o.Configuration)
	redactLoggingSecrets(cfg)
	if o.IsSystem {
		safe := make(map[string]any, len(systemLoggingOutputConfigKeys))
		for _, k := range systemLoggingOutputConfigKeys {
			if v, ok := cfg[k]; ok {
				safe[k] = v
			}
		}
		cfg = safe
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func redactLoggingSecrets(value map[string]any) {
	for key, item := range value {
		if _, sensitive := loggingSecretConfigKeys[key]; sensitive {
			delete(value, key)
			continue
		}
		switch nested := item.(type) {
		case map[string]any:
			redactLoggingSecrets(nested)
		case []any:
			for _, child := range nested {
				if object, ok := child.(map[string]any); ok {
					redactLoggingSecrets(object)
				}
			}
		}
	}
}

func stripBearerFromLoggingConfiguration(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	cfg := decodeConfiguration(raw)
	delete(cfg, "bearer_token")
	delete(cfg, "bearerToken")
	out, err := json.Marshal(cfg)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return out
}

func (h *LoggingHandler) attachLokiBearer(ctx context.Context, env *loggingOperationEnvelope) error {
	if env == nil || !env.IsSystem || !strings.EqualFold(env.OutputType, "loki") || !env.Enabled {
		return nil
	}
	if h.encryptor == nil {
		return errors.New("encryption is not configured")
	}
	clusterID, err := uuid.Parse(env.ClusterID)
	if err != nil {
		return fmt.Errorf("system loki output cluster_id: %w", err)
	}
	tok, err := h.queries.GetLokiIngestTokenByCluster(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("load ingest token: %w", err)
	}
	plain, err := h.encryptor.Decrypt(tok.TokenEncrypted)
	if err != nil {
		return fmt.Errorf("decrypt ingest token: %w", err)
	}
	env.BearerToken = plain
	return nil
}
