package handler

import (
	"encoding/json"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

// ClusterTemplateResponse is the wire shape returned by the list/get/
// create/update endpoints.
type ClusterTemplateResponse struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Spec        json.RawMessage `json:"spec"`
	CreatedBy   string          `json:"created_by,omitempty"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

type ClusterTemplateBoundClusterResponse struct {
	ClusterID     string `json:"cluster_id"`
	ClusterName   string `json:"cluster_name"`
	Status        string `json:"status"`
	LastAppliedAt string `json:"last_applied_at,omitempty"`
	Message       string `json:"message,omitempty"`
}

func templateToResponse(t sqlc.ClusterTemplate) ClusterTemplateResponse {
	resp := ClusterTemplateResponse{
		ID:          t.ID.String(),
		Name:        t.Name,
		Description: t.Description,
		Spec:        t.Spec,
		CreatedAt:   t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   t.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if t.CreatedBy.Valid {
		resp.CreatedBy = uuid.UUID(t.CreatedBy.Bytes).String()
	}
	return resp
}

// ClusterTemplateApplicationResponse is the wire shape for the
// /clusters/{id}/template/ GET status endpoint.
type ClusterTemplateApplicationResponse struct {
	ClusterID    string          `json:"cluster_id"`
	TemplateID   string          `json:"template_id"`
	TemplateName string          `json:"template_name,omitempty"`
	Status       string          `json:"status"`
	SpecSnapshot json.RawMessage `json:"spec_snapshot"`
	LastError    string          `json:"last_error,omitempty"`
	AppliedAt    string          `json:"applied_at,omitempty"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
	// Drift is filled in only by the drift-check task; the GET endpoint
	// reports the cached value. Empty string means "not yet evaluated".
	// Possible values: "synced" | "drift" | "".
	Drift string `json:"drift,omitempty"`
}

func applicationToResponse(a sqlc.ClusterTemplateApplication, templateName string) ClusterTemplateApplicationResponse {
	resp := ClusterTemplateApplicationResponse{
		ClusterID:    a.ClusterID.String(),
		TemplateID:   a.TemplateID.String(),
		TemplateName: templateName,
		Status:       a.Status,
		SpecSnapshot: a.SpecSnapshot,
		LastError:    a.LastError,
		CreatedAt:    a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    a.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if a.AppliedAt.Valid {
		resp.AppliedAt = a.AppliedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	return resp
}
