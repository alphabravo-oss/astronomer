package tasks

import (
	"context"
	"fmt"
	"strings"

	"github.com/hibiken/asynq"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/alphabravocompany/astronomer-go/internal/crd"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const (
	CRDOwnershipDriftCheckType = "crd:ownership_drift_check"
	ConditionCRDOwnership      = "CRDOwnershipSynced"
)

type CRDOwnershipDriftQuerier interface {
	ListCRDOwnedClusters(ctx context.Context, limit int32) ([]sqlc.FleetOwnership, error)
	UpsertClusterCondition(ctx context.Context, arg sqlc.UpsertClusterConditionParams) (sqlc.ClusterCondition, error)
}

type CRDOwnershipDriftDeps struct {
	Queries CRDOwnershipDriftQuerier
	Dynamic dynamic.Interface
}

var crdOwnershipDriftDeps CRDOwnershipDriftDeps

func ConfigureCRDOwnershipDrift(deps CRDOwnershipDriftDeps) {
	crdOwnershipDriftDeps = deps
}

func ResetCRDOwnershipDrift() {
	crdOwnershipDriftDeps = CRDOwnershipDriftDeps{}
}

func NewCRDOwnershipDriftCheckTask() *asynq.Task {
	return asynq.NewTask(CRDOwnershipDriftCheckType, nil, asynq.MaxRetry(2))
}

func HandleCRDOwnershipDriftCheck(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, CRDOwnershipDriftCheckType, func() error {
		deps := crdOwnershipDriftDeps
		if deps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "crd ownership drift runtime not configured, skipping")
			return nil
		}
		if deps.Dynamic == nil {
			runtimeLogger().InfoContext(ctx, "crd ownership drift skipped: dynamic kubernetes client not configured")
			return nil
		}
		rows, err := deps.Queries.ListCRDOwnedClusters(ctx, 1000)
		if err != nil {
			return fmt.Errorf("list crd-owned clusters: %w", err)
		}
		var firstErr error
		for _, row := range rows {
			if err := checkCRDOwnedClusterRef(ctx, deps, row); err != nil {
				runtimeLogger().WarnContext(ctx, "crd ownership drift check failed",
					"cluster_id", row.ID.String(),
					"kind", row.ExternalRefKind,
					"namespace", row.ExternalRefNamespace,
					"name", row.ExternalRefName,
					"error", err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		metadata := map[string]any{"checked_clusters": len(rows)}
		if firstErr != nil {
			recordRepairJobFailure(ctx, deps.Queries, CRDOwnershipDriftCheckType, firstErr, metadata)
		} else {
			recordRepairJobSuccess(ctx, deps.Queries, CRDOwnershipDriftCheckType, metadata)
		}
		return firstErr
	})
}

func checkCRDOwnedClusterRef(ctx context.Context, deps CRDOwnershipDriftDeps, row sqlc.FleetOwnership) error {
	if !strings.EqualFold(strings.TrimSpace(row.ExternalRefKind), "Cluster") {
		return nil
	}
	namespace := strings.TrimSpace(row.ExternalRefNamespace)
	name := strings.TrimSpace(row.ExternalRefName)
	if namespace == "" || name == "" {
		return nil
	}
	gvr, err := ownershipGVR(row)
	if err != nil {
		return err
	}
	_, err = deps.Dynamic.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		_, err = deps.Queries.UpsertClusterCondition(ctx, sqlc.UpsertClusterConditionParams{
			ClusterID: row.ID,
			Type:      ConditionCRDOwnership,
			Status:    "True",
			Reason:    "ExternalRefFound",
			Message:   fmt.Sprintf("CRD external reference %s/%s is present.", namespace, name),
		})
		return err
	}
	if apierrors.IsNotFound(err) {
		_, err = deps.Queries.UpsertClusterCondition(ctx, sqlc.UpsertClusterConditionParams{
			ClusterID: row.ID,
			Type:      ConditionCRDOwnership,
			Status:    "False",
			Reason:    "ExternalRefMissing",
			Message:   fmt.Sprintf("CRD-owned cluster row points at missing Cluster CR %s/%s.", namespace, name),
		})
		return err
	}
	return fmt.Errorf("get cluster CR %s/%s: %w", namespace, name, err)
}

func ownershipGVR(row sqlc.FleetOwnership) (schema.GroupVersionResource, error) {
	gv := crd.GroupVersion
	if raw := strings.TrimSpace(row.ExternalRefApiVersion); raw != "" {
		parsed, err := schema.ParseGroupVersion(raw)
		if err != nil {
			return schema.GroupVersionResource{}, fmt.Errorf("parse external apiVersion %q: %w", raw, err)
		}
		gv = parsed
	}
	return schema.GroupVersionResource{
		Group:    gv.Group,
		Version:  gv.Version,
		Resource: "clusters",
	}, nil
}
