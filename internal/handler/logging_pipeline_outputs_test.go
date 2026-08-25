package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestCreateLoggingPipelinePersistsAndReturnsSelectedOutputs(t *testing.T) {
	q := newLoggingFakeQuerier()
	clusterID := uuid.New()
	output, err := q.CreateLoggingOutput(context.Background(), sqlc.CreateLoggingOutputParams{
		Name: "primary", OutputType: "stdout", Configuration: json.RawMessage(`{}`),
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"name": "payments", "cluster_id": clusterID.String(),
		"namespaces": []string{"checkout"}, "output_ids": []string{output.ID.String()}, "enabled": true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/logging/pipelines?cluster_id="+clusterID.String(), bytes.NewReader(body))
	req.Header.Set("Idempotency-Key", "pipeline-create-1")
	rec := httptest.NewRecorder()
	NewLoggingHandler(q).CreatePipeline(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data loggingPipelineMutationReceipt `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Pipeline.OutputIDs) != 1 || envelope.Data.Pipeline.OutputIDs[0] != output.ID ||
		len(envelope.Data.Pipeline.OutputNames) != 1 || envelope.Data.Pipeline.OutputNames[0] != output.Name {
		t.Fatalf("response associations=%+v", envelope.Data.Pipeline)
	}
	operationID, _ := envelope.Data.Operation["id"].(string)
	if operationID == "" || rec.Header().Get("Location") != "/api/v1/logging/operations/"+operationID+"/" || rec.Header().Get("Retry-After") != "2" {
		t.Fatalf("operation receipt=%+v headers=%v", envelope.Data.Operation, rec.Header())
	}
	stored := q.pipelineOutputs[envelope.Data.Pipeline.ID]
	if len(stored) != 1 || stored[0] != output.ID {
		t.Fatalf("stored associations=%v", stored)
	}
}

func TestUpdateLoggingPipelineRejectsForeignClusterOutputWithoutMutation(t *testing.T) {
	q := newLoggingFakeQuerier()
	clusterID := uuid.New()
	foreignClusterID := uuid.New()
	pipeline, err := q.CreateLoggingPipeline(context.Background(), sqlc.CreateLoggingPipelineParams{
		Name: "payments", ClusterID: clusterID, Namespaces: json.RawMessage(`[]`),
		Labels: json.RawMessage(`{}`), Filters: json.RawMessage(`[]`), Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := q.CreateLoggingOutput(context.Background(), sqlc.CreateLoggingOutputParams{
		Name: "foreign", OutputType: "stdout", Configuration: json.RawMessage(`{}`),
		ClusterID: pgtype.UUID{Bytes: foreignClusterID, Valid: true}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"name": "changed", "output_ids": []string{foreign.ID.String()}, "enabled": true,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/logging/pipelines/"+pipeline.ID.String(), bytes.NewReader(body))
	req.Header.Set("Idempotency-Key", "pipeline-update-foreign-1")
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", pipeline.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()
	NewLoggingHandler(q).UpdatePipeline(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	stored, err := q.GetLoggingPipelineByID(context.Background(), pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != pipeline.Name {
		t.Fatalf("pipeline mutated before association validation: name=%q", stored.Name)
	}
}

func TestCreateLoggingPipelineRequiresIdempotencyKeyBeforeMutation(t *testing.T) {
	q := newLoggingFakeQuerier()
	clusterID := uuid.New()
	output, err := q.CreateLoggingOutput(context.Background(), sqlc.CreateLoggingOutputParams{
		Name: "primary", OutputType: "stdout", Configuration: json.RawMessage(`{}`),
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"name": "payments", "cluster_id": clusterID.String(),
		"output_ids": []string{output.ID.String()}, "enabled": true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/logging/pipelines?cluster_id="+clusterID.String(), bytes.NewReader(body))
	rec := httptest.NewRecorder()
	NewLoggingHandler(q).CreatePipeline(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(q.pipelines) != 0 {
		t.Fatalf("pipeline mutated without Idempotency-Key: %+v", q.pipelines)
	}
}

func TestLoggingPipelineReconciliationMutationsReturnAcceptedReceipts(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   func(uuid.UUID) string
		body   func(sqlc.LoggingOutput) []byte
		invoke func(*LoggingHandler, http.ResponseWriter, *http.Request)
	}{
		{
			name: "update", method: http.MethodPut,
			path: func(id uuid.UUID) string { return "/api/v1/logging/pipelines/" + id.String() },
			body: func(output sqlc.LoggingOutput) []byte {
				body, _ := json.Marshal(map[string]any{"name": "payments-v2", "output_ids": []string{output.ID.String()}, "enabled": true})
				return body
			},
			invoke: (*LoggingHandler).UpdatePipeline,
		},
		{
			name: "delete", method: http.MethodDelete,
			path:   func(id uuid.UUID) string { return "/api/v1/logging/pipelines/" + id.String() },
			body:   func(sqlc.LoggingOutput) []byte { return nil },
			invoke: (*LoggingHandler).DeletePipeline,
		},
		{
			name: "enable", method: http.MethodPost,
			path:   func(id uuid.UUID) string { return "/api/v1/logging/pipelines/" + id.String() + "/enable" },
			body:   func(sqlc.LoggingOutput) []byte { return nil },
			invoke: (*LoggingHandler).EnablePipeline,
		},
		{
			name: "disable", method: http.MethodPost,
			path:   func(id uuid.UUID) string { return "/api/v1/logging/pipelines/" + id.String() + "/disable" },
			body:   func(sqlc.LoggingOutput) []byte { return nil },
			invoke: (*LoggingHandler).DisablePipeline,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := newLoggingFakeQuerier()
			clusterID := uuid.New()
			output, err := q.CreateLoggingOutput(context.Background(), sqlc.CreateLoggingOutputParams{
				Name: "primary", OutputType: "stdout", Configuration: json.RawMessage(`{}`),
				ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true}, Enabled: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			pipeline, err := q.CreateLoggingPipeline(context.Background(), sqlc.CreateLoggingPipelineParams{
				Name: "payments", ClusterID: clusterID, Namespaces: json.RawMessage(`[]`),
				Labels: json.RawMessage(`{}`), Filters: json.RawMessage(`[]`), Enabled: tt.name != "enable",
			})
			if err != nil {
				t.Fatal(err)
			}
			q.pipelineOutputs[pipeline.ID] = []uuid.UUID{output.ID}

			req := httptest.NewRequest(tt.method, tt.path(pipeline.ID), bytes.NewReader(tt.body(output)))
			req.Header.Set("Idempotency-Key", "pipeline-"+tt.name+"-1")
			routeContext := chi.NewRouteContext()
			routeContext.URLParams.Add("id", pipeline.ID.String())
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
			rec := httptest.NewRecorder()
			tt.invoke(NewLoggingHandler(q), rec, req)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var envelope struct {
				Data loggingPipelineMutationReceipt `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			operationID, _ := envelope.Data.Operation["id"].(string)
			if envelope.Data.Pipeline.ID != pipeline.ID || operationID == "" {
				t.Fatalf("receipt=%+v", envelope.Data)
			}
			if rec.Header().Get("Location") != "/api/v1/logging/operations/"+operationID+"/" || rec.Header().Get("Retry-After") != "2" {
				t.Fatalf("headers=%v", rec.Header())
			}
		})
	}
}

func TestListLoggingPipelinesReturnsOutputAssociations(t *testing.T) {
	q := newLoggingFakeQuerier()
	clusterID := uuid.New()
	output, _ := q.CreateLoggingOutput(context.Background(), sqlc.CreateLoggingOutputParams{
		Name: "archive", OutputType: "s3", Configuration: json.RawMessage(`{}`),
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true}, Enabled: true,
	})
	pipeline, _ := q.CreateLoggingPipeline(context.Background(), sqlc.CreateLoggingPipelineParams{
		Name: "audit", ClusterID: clusterID, Namespaces: json.RawMessage(`[]`),
		Labels: json.RawMessage(`{}`), Filters: json.RawMessage(`[]`), Enabled: true,
	})
	q.pipelineOutputs[pipeline.ID] = []uuid.UUID{output.ID}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/logging/pipelines?cluster_id="+clusterID.String(), nil)
	rec := httptest.NewRecorder()
	NewLoggingHandler(q).ListPipelines(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data []loggingPipelineResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) != 1 || len(envelope.Data[0].OutputIDs) != 1 || envelope.Data[0].OutputIDs[0] != output.ID {
		t.Fatalf("list response=%+v", envelope.Data)
	}
}

func TestDeleteLoggingOutputReturnsConflictWhileSelectedByPipeline(t *testing.T) {
	q := newLoggingFakeQuerier()
	clusterID := uuid.New()
	output, _ := q.CreateLoggingOutput(context.Background(), sqlc.CreateLoggingOutputParams{
		Name: "protected", OutputType: "stdout", Configuration: json.RawMessage(`{}`),
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true}, Enabled: true,
	})
	pipeline, _ := q.CreateLoggingPipeline(context.Background(), sqlc.CreateLoggingPipelineParams{
		Name: "route", ClusterID: clusterID, Namespaces: json.RawMessage(`[]`),
		Labels: json.RawMessage(`{}`), Filters: json.RawMessage(`[]`), Enabled: true,
	})
	q.pipelineOutputs[pipeline.ID] = []uuid.UUID{output.ID}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/logging/outputs/"+output.ID.String(), nil)
	req.Header.Set("Idempotency-Key", "output-delete-selected-1")
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", output.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()
	NewLoggingHandler(q).DeleteOutput(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := q.GetLoggingOutputByID(context.Background(), output.ID); err != nil {
		t.Fatal("referenced output was deleted")
	}
}
