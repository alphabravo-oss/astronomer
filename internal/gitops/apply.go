// Apply / reconcile a parsed ClusterRegistration into the database.
//
// This file is the shared helper the spec calls out: the sync worker and
// the handler /preview/ endpoint both call ApplyRegistration so the
// "what does GitOps actually do when it sees this YAML" logic lives in
// exactly one place.
//
// Apply behaviour:
//
//   - If no cluster row with metadata.name exists: CreateCluster (env =
//     spec.labels["env"] when present, region = spec.labels["region"]),
//     plus initial labels payload.
//   - If a row exists: UpdateCluster with the (re-marshalled) labels.
//   - Apply (or rebind) the cluster_template_application when spec.template
//     is set and resolvable.
//   - Surface the project name for the per-cluster project association
//     when spec.project is set (the result struct exposes both the doc and
//     the cluster so the worker can enqueue downstream tasks without
//     re-querying).
//
// The dry-run flow (handler /preview/) passes Dry=true. In that mode the
// function performs no DB writes — it returns the same Result shape the
// real apply would produce, with Created / Updated / TemplateBound / etc.
// flags reflecting what *would* happen.

package gitops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// rfc1123ClusterName mirrors the validClusterName check in the cluster
// handler. Duplicated here (rather than imported) because internal/gitops
// is intentionally handler-free — the handler imports gitops, not the
// reverse.
var rfc1123ClusterName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// ValidClusterName enforces the RFC-1123 label rules.
func ValidClusterName(s string) bool {
	return rfc1123ClusterName.MatchString(s)
}

// SHA returns the canonical content-hash of a YAML payload. Stored on
// gitops_registered_clusters.last_yaml_sha so subsequent ticks no-op when
// the file hasn't changed.
func SHA(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// ErrInvalidClusterName is returned when metadata.name doesn't pass
// ValidClusterName.
var ErrInvalidClusterName = errors.New("gitops: metadata.name is not a valid RFC-1123 cluster name")

// ApplyQuerier is the database surface ApplyRegistration needs. It's a
// strict subset of *sqlc.Queries so the handler tier passes the same
// concrete value the rest of the platform uses, and tests pass a fake.
type ApplyQuerier interface {
	GetClusterByName(ctx context.Context, name string) (sqlc.Cluster, error)
	CreateCluster(ctx context.Context, arg sqlc.CreateClusterParams) (sqlc.Cluster, error)
	UpdateCluster(ctx context.Context, arg sqlc.UpdateClusterParams) (sqlc.Cluster, error)

	GetClusterTemplateByName(ctx context.Context, name string) (sqlc.ClusterTemplate, error)
	UpsertClusterTemplateApplication(ctx context.Context, arg sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error)

	UpsertGitOpsRegisteredCluster(ctx context.Context, arg sqlc.UpsertGitOpsRegisteredClusterParams) (sqlc.GitopsRegisteredCluster, error)
}

// ApplyInput is the (parsed-doc + source) bundle the worker hands to
// Apply on every tick. ContentSHA is the canonical sha256 of the raw
// file bytes; it's stored on gitops_registered_clusters.last_yaml_sha so
// the next tick no-ops when nothing has changed.
type ApplyInput struct {
	Doc        ClusterRegistration
	SourceID   uuid.UUID
	ContentSHA string
	ActorID    pgtype.UUID // created_by on the clusters row when a new row is inserted
	Dry        bool        // when true, no DB writes; Result still describes intended effect
}

// Result is the structured output of Apply. The sync worker reads it to
// drive metrics + audit; /preview/ serialises it directly into the
// dry-run JSON response.
type Result struct {
	ClusterName    string   `json:"cluster_name"`
	ClusterID      string   `json:"cluster_id,omitempty"`
	RepoPath       string   `json:"repo_path"`
	Created        bool     `json:"created"`        // a new clusters row was/would be inserted
	Updated        bool     `json:"updated"`        // an existing row was/would be UpdateCluster'd
	NoOp           bool     `json:"no_op"`          // last_yaml_sha matched; nothing applied
	TemplateBound  bool     `json:"template_bound"` // cluster_template_application was/would be upserted
	Registries     []string `json:"registries"`     // names from spec.registries
	ToolPresets    []string `json:"tool_presets"`   // names from spec.toolPresets
	Project        string   `json:"project,omitempty"`
	RestoredActive bool     `json:"restored_active"` // a previously tombstoned row was/would be revived
}

// Apply reconciles a single parsed ClusterRegistration into the
// database. Returns the Result the worker / preview surface consume.
//
// Apply does NOT enqueue downstream tasks (cluster_template:apply,
// cluster_registry:apply). The worker layer wires those after Apply
// returns successfully, because the queue handle isn't part of the
// shared helper's contract (the preview/dry-run path must not enqueue).
func Apply(ctx context.Context, q ApplyQuerier, in ApplyInput) (Result, error) {
	if in.Doc.Metadata.Name == "" {
		return Result{}, ErrMissingName
	}
	if !ValidClusterName(in.Doc.Metadata.Name) {
		return Result{}, fmt.Errorf("%w: %q", ErrInvalidClusterName, in.Doc.Metadata.Name)
	}

	result := Result{
		ClusterName: in.Doc.Metadata.Name,
		RepoPath:    in.Doc.RepoPath,
		Registries:  append([]string(nil), in.Doc.Spec.Registries...),
		ToolPresets: append([]string(nil), in.Doc.Spec.ToolPresets...),
		Project:     in.Doc.Spec.Project,
	}
	if result.Registries == nil {
		result.Registries = []string{}
	}
	if result.ToolPresets == nil {
		result.ToolPresets = []string{}
	}

	// Marshal labels once — used by both Create + Update.
	labelsJSON, err := json.Marshal(in.Doc.Spec.Labels)
	if err != nil {
		return Result{}, fmt.Errorf("marshal labels: %w", err)
	}
	if len(labelsJSON) == 0 || string(labelsJSON) == "null" {
		labelsJSON = json.RawMessage(`{}`)
	}

	existing, getErr := q.GetClusterByName(ctx, in.Doc.Metadata.Name)
	if getErr != nil && !errors.Is(getErr, pgx.ErrNoRows) {
		return Result{}, fmt.Errorf("lookup cluster %q: %w", in.Doc.Metadata.Name, getErr)
	}
	clusterMissing := errors.Is(getErr, pgx.ErrNoRows)

	var cluster sqlc.Cluster
	switch {
	case clusterMissing && !in.Dry:
		// Insert. environment / region default to labels["env"] /
		// labels["region"] when present so the spec.labels carry over to
		// the columns the rest of the platform reads.
		cluster, err = q.CreateCluster(ctx, sqlc.CreateClusterParams{
			Name:         in.Doc.Metadata.Name,
			DisplayName:  in.Doc.Metadata.Name,
			Description:  "",
			Environment:  pickLabel(in.Doc.Spec.Labels, "env", "environment", "tier"),
			Region:       pickLabel(in.Doc.Spec.Labels, "region"),
			Provider:     pickLabel(in.Doc.Spec.Labels, "provider"),
			Distribution: pickLabel(in.Doc.Spec.Labels, "distribution"),
			CreatedByID:  in.ActorID,
		})
		if err != nil {
			return Result{}, fmt.Errorf("create cluster %q: %w", in.Doc.Metadata.Name, err)
		}
		// Stamp labels onto the row via UpdateCluster — CreateCluster
		// doesn't take a labels arg.
		cluster, err = q.UpdateCluster(ctx, sqlc.UpdateClusterParams{
			ID:          cluster.ID,
			DisplayName: cluster.DisplayName,
			Description: cluster.Description,
			Environment: cluster.Environment,
			Region:      cluster.Region,
			Labels:      labelsJSON,
			Annotations: json.RawMessage(`{}`),
		})
		if err != nil {
			return Result{}, fmt.Errorf("stamp labels on cluster %q: %w", in.Doc.Metadata.Name, err)
		}
		result.Created = true
		result.ClusterID = cluster.ID.String()

	case clusterMissing && in.Dry:
		// Dry-run with no existing row → mark as Created and pretend.
		result.Created = true

	case !clusterMissing && !in.Dry:
		cluster, err = q.UpdateCluster(ctx, sqlc.UpdateClusterParams{
			ID:          existing.ID,
			DisplayName: existing.DisplayName,
			Description: existing.Description,
			Environment: pickLabelOr(in.Doc.Spec.Labels, existing.Environment, "env", "environment", "tier"),
			Region:      pickLabelOr(in.Doc.Spec.Labels, existing.Region, "region"),
			Labels:      labelsJSON,
			Annotations: orRaw(existing.Annotations, json.RawMessage(`{}`)),
		})
		if err != nil {
			return Result{}, fmt.Errorf("update cluster %q: %w", in.Doc.Metadata.Name, err)
		}
		result.Updated = true
		result.ClusterID = cluster.ID.String()

	case !clusterMissing && in.Dry:
		result.Updated = true
		result.ClusterID = existing.ID.String()
		cluster = existing
	}

	// Template bind --------------------------------------------------
	if in.Doc.Spec.Template != "" {
		if in.Dry {
			result.TemplateBound = true
		} else {
			tmpl, tErr := q.GetClusterTemplateByName(ctx, in.Doc.Spec.Template)
			if tErr != nil {
				if errors.Is(tErr, pgx.ErrNoRows) {
					return Result{}, fmt.Errorf("template %q referenced by %q does not exist", in.Doc.Spec.Template, in.Doc.Metadata.Name)
				}
				return Result{}, fmt.Errorf("lookup template %q: %w", in.Doc.Spec.Template, tErr)
			}
			if _, err := q.UpsertClusterTemplateApplication(ctx, sqlc.UpsertClusterTemplateApplicationParams{
				ClusterID:    cluster.ID,
				TemplateID:   tmpl.ID,
				SpecSnapshot: orRaw(tmpl.Spec, json.RawMessage(`{}`)),
			}); err != nil {
				return Result{}, fmt.Errorf("upsert template application: %w", err)
			}
			result.TemplateBound = true
		}
	}

	// Stamp the gitops_registered_clusters link row so subsequent ticks
	// converge to no-op when the YAML hasn't changed. Dry-run skips
	// this — that's how /preview/ stays read-only.
	if !in.Dry && cluster.ID != uuid.Nil {
		linkRow, err := q.UpsertGitOpsRegisteredCluster(ctx, sqlc.UpsertGitOpsRegisteredClusterParams{
			ClusterID:   cluster.ID,
			SourceID:    in.SourceID,
			RepoPath:    in.Doc.RepoPath,
			LastYamlSha: in.ContentSHA,
		})
		if err != nil {
			return Result{}, fmt.Errorf("upsert registered cluster link: %w", err)
		}
		_ = linkRow
	}

	return result, nil
}

// pickLabel returns the first non-empty value from the spec labels for
// any of the provided keys.
func pickLabel(labels map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := labels[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

// pickLabelOr falls back to the provided default when no label matches.
func pickLabelOr(labels map[string]string, fallback string, keys ...string) string {
	if v := pickLabel(labels, keys...); v != "" {
		return v
	}
	return fallback
}

// orRaw substitutes a default when the provided JSON is empty / null.
func orRaw(raw json.RawMessage, def json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return def
	}
	return raw
}
