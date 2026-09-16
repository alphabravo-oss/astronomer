package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *CloudCredentialHandler) canonicaliseTargetRefs(ctx context.Context, projectID uuid.UUID, in []TargetRef, credName string) ([]TargetRef, error) {
	if len(in) == 0 {
		return []TargetRef{}, nil
	}
	rows, err := h.queries.ListProjectNamespaces(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to verify target namespace ownership")
	}
	ownedNS := make(map[string]struct{}, len(rows))
	for _, ns := range rows {
		ownedNS[ns.ClusterID.String()+"|"+ns.Namespace] = struct{}{}
	}
	defaultName := defaultSecretName(credName)
	out := make([]TargetRef, 0, len(in))
	seen := map[string]struct{}{}
	for _, ref := range in {
		if ref.ClusterID == uuid.Nil {
			return nil, fmt.Errorf("target_ref.cluster_id must be set")
		}
		ns := strings.TrimSpace(ref.Namespace)
		if ns == "" {
			return nil, fmt.Errorf("target_ref.namespace must be set")
		}
		secret := strings.TrimSpace(ref.SecretName)
		if secret == "" {
			secret = defaultName
		} else {
			secret = sanitiseSecretName(secret)
			if secret == "" {
				return nil, fmt.Errorf("target_ref.secret_name produces an empty RFC 1123 name")
			}
		}
		// Verify the cluster exists. 404 here surfaces the operator-
		// recoverable failure cleanly instead of letting the worker
		// queue a doomed task.
		if _, err := h.queries.GetClusterByID(ctx, ref.ClusterID); err != nil {
			return nil, fmt.Errorf("target_ref.cluster_id %q not found", ref.ClusterID.String())
		}
		// Authorize the (cluster, namespace) against the project's owned
		// namespaces. Fail closed: an unowned pair is rejected so a
		// low-privileged caller can't write/delete Secrets cross-tenant.
		if _, owned := ownedNS[ref.ClusterID.String()+"|"+ns]; !owned {
			return nil, fmt.Errorf("target_ref cluster %s namespace %q is not owned by this project", ref.ClusterID.String(), ns)
		}
		key := ref.ClusterID.String() + "|" + ns
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("duplicate target_ref for cluster %s namespace %q", ref.ClusterID.String(), ns)
		}
		seen[key] = struct{}{}
		out = append(out, TargetRef{
			ClusterID:  ref.ClusterID,
			Namespace:  ns,
			SecretName: secret,
		})
	}
	return out, nil
}

func (h *CloudCredentialHandler) stageMaterializationRefs(ctx context.Context, q cloudCredentialMaterializationTaskOutboxQuerier, cred sqlc.CloudCredential, refs []TargetRef, op string) error {
	for _, ref := range refs {
		if err := stageCloudCredentialMaterialization(ctx, q, cred, ref, op); err != nil {
			return err
		}
	}
	return nil
}

func (h *CloudCredentialHandler) stageDeleteMaterializationRefs(ctx context.Context, q cloudCredentialMaterializationTaskOutboxQuerier, credentialID uuid.UUID, refs []TargetRef) error {
	operationID := reqctx.RequestID(ctx)
	if operationID == "" {
		operationID = uuid.NewString()
	}
	for _, ref := range refs {
		if err := stageCloudCredentialMaterializationDelete(ctx, q, credentialID, ref, operationID); err != nil {
			return err
		}
	}
	return nil
}

func stageCloudCredentialMaterialization(ctx context.Context, q cloudCredentialMaterializationTaskOutboxQuerier, cred sqlc.CloudCredential, ref TargetRef, op string) error {
	task, err := tasks.NewCloudCredentialMaterializeTask(tasks.CloudCredentialMaterializePayload{
		CredentialID: cred.ID.String(),
		ClusterID:    ref.ClusterID.String(),
		Namespace:    ref.Namespace,
		SecretName:   ref.SecretName,
		Op:           op,
	})
	if err != nil {
		return err
	}
	payload := observability.EnrichTaskPayload(ctx, task.Payload(), reqctx.CorrelationID(ctx))
	task = asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3))
	_, err = q.UpsertCloudCredentialMaterializationWithTaskOutbox(ctx, sqlc.UpsertCloudCredentialMaterializationWithTaskOutboxParams{
		CredentialID:        cred.ID,
		ClusterID:           ref.ClusterID,
		Namespace:           ref.Namespace,
		SecretName:          ref.SecretName,
		DedupeKey:           pgtype.Text{String: cloudCredentialMaterializeDedupeKey(cred.ID, ref, op, cloudCredentialDataVersion(cred)), Valid: true},
		TaskType:            task.Type(),
		Payload:             task.Payload(),
		QueueName:           tasks.ClusterTemplateApplyQueueName,
		MaxRetry:            3,
		MaxDeliveryAttempts: 20,
		NextAttemptAt:       pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	return err
}

func stageCloudCredentialMaterializationDelete(ctx context.Context, q cloudCredentialMaterializationTaskOutboxQuerier, credentialID uuid.UUID, ref TargetRef, operationID string) error {
	task, err := tasks.NewCloudCredentialMaterializeTask(tasks.CloudCredentialMaterializePayload{
		CredentialID: uuid.Nil.String(),
		ClusterID:    ref.ClusterID.String(),
		Namespace:    ref.Namespace,
		SecretName:   ref.SecretName,
		Op:           "delete",
	})
	if err != nil {
		return err
	}
	payload := observability.EnrichTaskPayload(ctx, task.Payload(), reqctx.CorrelationID(ctx))
	task = asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3))
	err = q.DeleteCloudCredentialMaterializationWithTaskOutbox(ctx, sqlc.DeleteCloudCredentialMaterializationWithTaskOutboxParams{
		CredentialID:        credentialID,
		ClusterID:           ref.ClusterID,
		Namespace:           ref.Namespace,
		DedupeKey:           pgtype.Text{String: cloudCredentialMaterializeDedupeKey(credentialID, ref, "delete", operationID), Valid: true},
		TaskType:            task.Type(),
		Payload:             task.Payload(),
		QueueName:           tasks.ClusterTemplateApplyQueueName,
		MaxRetry:            3,
		MaxDeliveryAttempts: 20,
		NextAttemptAt:       pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	return err
}

// cloudCredentialMaterializeDedupeKey builds the task_outbox / materialization
// dedupe key. dataVersion is a short fingerprint of the credential's current
// encrypted value (empty for deletes). Folding it in is what makes a credential
// *rotation* re-materialize: without it, an UPDATE that changes only
// data_encrypted (secret_name and refs unchanged) produced the identical key, so
// the already-'delivered' outbox row and 'applied' materialization row both
// deduped the change away and the in-cluster Secret silently kept the old value.
// A changed value now yields a new key → a fresh 'pending' outbox row → the
// worker re-applies the rotated credential.
func cloudCredentialMaterializeDedupeKey(credentialID uuid.UUID, ref TargetRef, op string, dataVersion string) string {
	return fmt.Sprintf("cloud_credential_materialize:%s:%s:%s:%s:%s:%s",
		credentialID.String(),
		ref.ClusterID.String(),
		ref.Namespace,
		ref.SecretName,
		op,
		dataVersion,
	)
}

// cloudCredentialDataVersion is a short, stable fingerprint of a credential's
// current encrypted payload. data_encrypted is re-written (freshly Fernet-
// encrypted) on every credential update, so this changes whenever the stored
// value changes — exactly when a re-materialize must fire.
func cloudCredentialDataVersion(cred sqlc.CloudCredential) string {
	sum := sha256.Sum256([]byte(cred.DataEncrypted))
	return hex.EncodeToString(sum[:6])
}
