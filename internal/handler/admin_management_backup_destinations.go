package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

const defaultManagementBackupImage = "ghcr.io/alphabravocompany/pgdump-s3:16-awscli"

const managementBackupDumpScript = `set -eu
STAMP="$(date -u +%Y-%m-%dT%H-%M-%SZ)"
DOW="$(date -u +%u)"
DOM="$(date -u +%d)"
BUCKET="${MANAGEMENT_BACKUP_BUCKET}"
PREFIX="${MANAGEMENT_BACKUP_PREFIX:-astronomer-pg}"
RELEASE="${MANAGEMENT_BACKUP_RELEASE}"
DAILY_KEY="${PREFIX}/${RELEASE}/daily/${STAMP}.pgcustom"
WEEKLY_KEY="${PREFIX}/${RELEASE}/weekly/${STAMP}.pgcustom"
MONTHLY_KEY="${PREFIX}/${RELEASE}/monthly/${STAMP}.pgcustom"
DUMP_PATH="/tmp/${STAMP}.pgcustom"
export AWS_SHARED_CREDENTIALS_FILE="/var/run/aws/credentials"
export AWS_DEFAULT_REGION="${MANAGEMENT_BACKUP_REGION}"
AWS_S3_FLAGS=""
if [ -n "${MANAGEMENT_BACKUP_ENDPOINT:-}" ]; then
  AWS_S3_FLAGS="--endpoint-url ${MANAGEMENT_BACKUP_ENDPOINT}"
fi
echo "Starting pg_dump at ${STAMP} (release=${RELEASE} dest=${MANAGEMENT_BACKUP_DEST_NAME})"
pg_dump --dbname="${DATABASE_URL}" --format=custom --no-owner --no-acl --file="${DUMP_PATH}"
DUMP_SIZE="$(wc -c < "${DUMP_PATH}")"
echo "pg_dump complete: ${DUMP_SIZE} bytes"
echo "Uploading daily: s3://${BUCKET}/${DAILY_KEY}"
aws s3 cp ${AWS_S3_FLAGS} "${DUMP_PATH}" "s3://${BUCKET}/${DAILY_KEY}"
if [ "${DOW}" = "7" ]; then
  echo "Promoting to weekly: s3://${BUCKET}/${WEEKLY_KEY}"
  aws s3 cp ${AWS_S3_FLAGS} "${DUMP_PATH}" "s3://${BUCKET}/${WEEKLY_KEY}"
fi
if [ "${DOM}" = "01" ]; then
  echo "Promoting to monthly: s3://${BUCKET}/${MONTHLY_KEY}"
  aws s3 cp ${AWS_S3_FLAGS} "${DUMP_PATH}" "s3://${BUCKET}/${MONTHLY_KEY}"
fi
rm -f "${DUMP_PATH}"
prune_tier() {
  TIER="$1"
  KEEP="$2"
  PFX="${PREFIX}/${RELEASE}/${TIER}/"
  echo "Pruning ${TIER} retention (keep ${KEEP})"
  KEYS="$(aws s3api list-objects-v2 ${AWS_S3_FLAGS} --bucket "${BUCKET}" --prefix "${PFX}" --query 'Contents[].Key' --output text 2>/dev/null || echo '')"
  if [ -z "${KEYS}" ] || [ "${KEYS}" = "None" ]; then
    echo "  (no objects under ${PFX})"
    return 0
  fi
  echo "${KEYS}" | tr '\t' '\n' | tr ' ' '\n' | sed '/^$/d' | sort -r | tail -n +"$((KEEP + 1))" | while read -r OLD; do
    [ -z "${OLD}" ] && continue
    echo "  rm s3://${BUCKET}/${OLD}"
    aws s3 rm ${AWS_S3_FLAGS} "s3://${BUCKET}/${OLD}" || true
  done
}
prune_tier "daily"   "${MANAGEMENT_BACKUP_KEEP_DAILY}"
prune_tier "weekly"  "${MANAGEMENT_BACKUP_KEEP_WEEKLY}"
prune_tier "monthly" "${MANAGEMENT_BACKUP_KEEP_MONTHLY}"
echo "Backup OK."
`

// ManagementBackupDestinationWrite is the create/update body.
type ManagementBackupDestinationWrite struct {
	Name        string `json:"name" validate:"required,max=255"`
	Bucket      string `json:"bucket" validate:"required,max=255"`
	Prefix      string `json:"prefix"`
	Region      string `json:"region"`
	EndpointURL string `json:"endpoint_url"`
	AccessKey   string `json:"access_key"`
	SecretKey   string `json:"secret_key"`
	Schedule    string `json:"schedule"`
	Enabled     *bool  `json:"enabled"`
	KeepDaily   *int32 `json:"keep_daily"`
	KeepWeekly  *int32 `json:"keep_weekly"`
	KeepMonthly *int32 `json:"keep_monthly"`
}

// CreateDestination handles POST /admin/management-backup/destinations/.
func (h *AdminDrillHandler) CreateDestination(w http.ResponseWriter, r *http.Request) {
	if !h.gateAction(w, r, "admin.management_backup.destination.created") {
		return
	}
	var req ManagementBackupDestinationWrite
	if !decodeAndValidate(w, r, &req) {
		return
	}
	req.normalize()
	if req.AccessKey == "" || req.SecretKey == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Access key and secret key are required")
		return
	}
	encrypted, err := h.encryptDestinationCredentials(req.AccessKey, req.SecretKey)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CryptoError, "Failed to encrypt credentials")
		return
	}
	createdBy := pgtype.UUID{}
	if user, ok := middleware.GetAuthenticatedUser(r.Context()); ok && user != nil {
		if id, err := uuid.Parse(user.ID); err == nil {
			createdBy = pgtype.UUID{Bytes: id, Valid: true}
		}
	}
	row, err := h.queries.CreateManagementBackupDestination(r.Context(), sqlc.CreateManagementBackupDestinationParams{
		Name:                 req.Name,
		Bucket:               req.Bucket,
		Prefix:               req.Prefix,
		Region:               req.Region,
		EndpointUrl:          req.EndpointURL,
		EncryptedCredentials: encrypted,
		Schedule:             req.Schedule,
		Enabled:              derefBool(req.Enabled, true),
		KeepDaily:            derefInt32(req.KeepDaily, 30),
		KeepWeekly:           derefInt32(req.KeepWeekly, 12),
		KeepMonthly:          derefInt32(req.KeepMonthly, 6),
		CreatedByID:          createdBy,
	})
	if err != nil {
		h.respondDestinationWriteError(w, r, err)
		return
	}
	if err := h.reconcileDestination(r.Context(), row, req.AccessKey, req.SecretKey); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, err.Error())
		return
	}
	RespondJSON(w, http.StatusCreated, destinationView(row))
}

// UpdateDestination handles PUT /admin/management-backup/destinations/{id}/.
func (h *AdminDrillHandler) UpdateDestination(w http.ResponseWriter, r *http.Request) {
	if !h.gateAction(w, r, "admin.management_backup.destination.updated") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid destination ID")
		return
	}
	existing, err := h.queries.GetManagementBackupDestination(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Destination not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	var req ManagementBackupDestinationWrite
	if !decodeAndValidate(w, r, &req) {
		return
	}
	req.normalize()
	encrypted := existing.EncryptedCredentials
	access, secret, decErr := h.decryptDestinationCredentials(existing)
	if decErr != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CryptoError, "Failed to decrypt credentials")
		return
	}
	if req.AccessKey != "" && req.AccessKey != PasswordSentinelEncrypted {
		access = req.AccessKey
	}
	if req.SecretKey != "" && req.SecretKey != PasswordSentinelEncrypted {
		secret = req.SecretKey
	}
	if access != "" && secret != "" {
		enc, encErr := h.encryptDestinationCredentials(access, secret)
		if encErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CryptoError, "Failed to encrypt credentials")
			return
		}
		if enc != "" {
			encrypted = enc
		}
	}
	row, err := h.queries.UpdateManagementBackupDestination(r.Context(), sqlc.UpdateManagementBackupDestinationParams{
		ID:                   id,
		Name:                 req.Name,
		Bucket:               req.Bucket,
		Prefix:               req.Prefix,
		Region:               req.Region,
		EndpointUrl:          req.EndpointURL,
		EncryptedCredentials: encrypted,
		Schedule:             req.Schedule,
		Enabled:              derefBool(req.Enabled, existing.Enabled),
		KeepDaily:            derefInt32(req.KeepDaily, existing.KeepDaily),
		KeepWeekly:           derefInt32(req.KeepWeekly, existing.KeepWeekly),
		KeepMonthly:          derefInt32(req.KeepMonthly, existing.KeepMonthly),
	})
	if err != nil {
		h.respondDestinationWriteError(w, r, err)
		return
	}
	if err := h.reconcileDestination(r.Context(), row, access, secret); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, destinationView(row))
}

// DeleteDestination handles DELETE /admin/management-backup/destinations/{id}/.
func (h *AdminDrillHandler) DeleteDestination(w http.ResponseWriter, r *http.Request) {
	if !h.gateAction(w, r, "admin.management_backup.destination.deleted") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid destination ID")
		return
	}
	if _, err := h.queries.GetManagementBackupDestination(r.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Destination not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	if err := h.deleteDestinationResources(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, err.Error())
		return
	}
	if err := h.queries.DeleteManagementBackupDestination(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TestDestination handles POST /admin/management-backup/destinations/{id}/test/.
func (h *AdminDrillHandler) TestDestination(w http.ResponseWriter, r *http.Request) {
	if !h.gateAction(w, r, "admin.management_backup.destination.tested") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid destination ID")
		return
	}
	row, err := h.queries.GetManagementBackupDestination(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Destination not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	access, secret, err := h.decryptDestinationCredentials(row)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CryptoError, "Failed to decrypt credentials")
		return
	}
	if err := h.probeManagementBackupS3(r.Context(), row, access, secret); err != nil {
		RespondJSON(w, http.StatusOK, map[string]any{"success": false, "message": err.Error()})
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Bucket is reachable and credentials are valid"})
}

// RunDestination handles POST /admin/management-backup/destinations/{id}/run/.
func (h *AdminDrillHandler) RunDestination(w http.ResponseWriter, r *http.Request) {
	if !h.gateAction(w, r, "admin.management_backup.destination.ran") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid destination ID")
		return
	}
	row, err := h.queries.GetManagementBackupDestination(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Destination not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	if h.k8s == nil || h.namespace == "" {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Management Kubernetes client is not available")
		return
	}
	access, secret, err := h.decryptDestinationCredentials(row)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CryptoError, "Failed to decrypt credentials")
		return
	}
	if err := h.reconcileDestination(r.Context(), row, access, secret); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, err.Error())
		return
	}
	cj, err := h.destinationCronJob(r.Context(), row.ID)
	if err != nil || cj == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Backup CronJob is not ready")
		return
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: cj.Name + "-",
			Namespace:    h.namespace,
			Labels:       cj.Spec.JobTemplate.Labels,
		},
		Spec: cj.Spec.JobTemplate.Spec,
	}
	created, err := h.k8s.BatchV1().Jobs(h.namespace).Create(r.Context(), job, metav1.CreateOptions{})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, err.Error())
		return
	}
	RespondJSON(w, http.StatusAccepted, map[string]any{"name": created.Name})
}

func (req *ManagementBackupDestinationWrite) normalize() {
	req.Name = strings.TrimSpace(req.Name)
	req.Bucket = strings.TrimSpace(req.Bucket)
	req.Prefix = strings.TrimSpace(req.Prefix)
	if req.Prefix == "" {
		req.Prefix = "astronomer-pg"
	}
	req.Region = strings.TrimSpace(req.Region)
	if req.Region == "" {
		req.Region = "us-east-1"
	}
	req.EndpointURL = strings.TrimSpace(req.EndpointURL)
	req.Schedule = strings.TrimSpace(req.Schedule)
	if req.Schedule == "" {
		req.Schedule = "0 3 * * *"
	}
}

func derefBool(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func derefInt32(v *int32, fallback int32) int32 {
	if v == nil {
		return fallback
	}
	return *v
}

func (h *AdminDrillHandler) respondDestinationWriteError(w http.ResponseWriter, r *http.Request, err error) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A destination with that name already exists")
		return
	}
	RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
}

func (h *AdminDrillHandler) encryptDestinationCredentials(access, secret string) (string, error) {
	if h == nil || h.encryptor == nil {
		return "", nil
	}
	payload, err := json.Marshal(map[string]string{"access_key": access, "secret_key": secret})
	if err != nil {
		return "", err
	}
	return h.encryptor.Encrypt(string(payload))
}

func (h *AdminDrillHandler) decryptDestinationCredentials(row sqlc.ManagementBackupDestination) (string, string, error) {
	if h == nil || h.encryptor == nil || row.EncryptedCredentials == "" {
		return "", "", nil
	}
	plaintext, err := h.encryptor.Decrypt(row.EncryptedCredentials)
	if err != nil {
		return "", "", err
	}
	var creds struct {
		AccessKey string `json:"access_key"`
		SecretKey string `json:"secret_key"`
	}
	if err := json.Unmarshal([]byte(plaintext), &creds); err != nil {
		return "", "", err
	}
	return creds.AccessKey, creds.SecretKey, nil
}

func (h *AdminDrillHandler) reconcileDestination(ctx context.Context, row sqlc.ManagementBackupDestination, access, secret string) error {
	if h.k8s == nil || h.namespace == "" {
		return nil
	}
	if !row.Enabled {
		return h.deleteDestinationResources(ctx, row.ID)
	}
	if err := h.upsertDestinationSecret(ctx, row.ID, access, secret); err != nil {
		return err
	}
	return h.upsertDestinationCronJob(ctx, row)
}

func (h *AdminDrillHandler) deleteDestinationResources(ctx context.Context, id uuid.UUID) error {
	if h.k8s == nil || h.namespace == "" {
		return nil
	}
	name := h.destinationResourceName(id)
	if err := h.k8s.BatchV1().CronJobs(h.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err := h.k8s.CoreV1().Secrets(h.namespace).Delete(ctx, name+"-aws", metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (h *AdminDrillHandler) upsertDestinationSecret(ctx context.Context, id uuid.UUID, access, secret string) error {
	name := h.destinationResourceName(id) + "-aws"
	body := fmt.Sprintf("[default]\naws_access_key_id = %s\naws_secret_access_key = %s\n", access, secret)
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: h.namespace,
			Labels:    h.destinationLabels(id),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"credentials": []byte(body)},
	}
	if _, err := h.k8s.CoreV1().Secrets(h.namespace).Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		_, err = h.k8s.CoreV1().Secrets(h.namespace).Create(ctx, sec, metav1.CreateOptions{})
		return err
	} else if err != nil {
		return err
	}
	_, err := h.k8s.CoreV1().Secrets(h.namespace).Update(ctx, sec, metav1.UpdateOptions{})
	return err
}

func (h *AdminDrillHandler) upsertDestinationCronJob(ctx context.Context, row sqlc.ManagementBackupDestination) error {
	name := h.destinationResourceName(row.ID)
	cj := h.buildDestinationCronJob(ctx, row)
	if existing, err := h.k8s.BatchV1().CronJobs(h.namespace).Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		_, err = h.k8s.BatchV1().CronJobs(h.namespace).Create(ctx, cj, metav1.CreateOptions{})
		return err
	} else if err != nil {
		return err
	} else {
		cj.ResourceVersion = existing.ResourceVersion
		_, err = h.k8s.BatchV1().CronJobs(h.namespace).Update(ctx, cj, metav1.UpdateOptions{})
		return err
	}
}

func (h *AdminDrillHandler) destinationLabels(id uuid.UUID) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/name":      "astronomer",
		"app.kubernetes.io/instance":  h.resourcePrefix(),
		"app.kubernetes.io/component": managementBackupComponent,
		"app.kubernetes.io/part-of":   "astronomer",
		destinationIDLabel:            id.String(),
	}
	return labels
}

func (h *AdminDrillHandler) buildDestinationCronJob(ctx context.Context, row sqlc.ManagementBackupDestination) *batchv1.CronJob {
	labels := h.destinationLabels(row.ID)
	sa := h.serviceAccount
	if sa == "" {
		sa = h.resourcePrefix()
	}
	image := h.backupImage
	if image == "" {
		image = defaultManagementBackupImage
	}
	nonRoot := int64(65534)
	falseVal := false
	env := []corev1.EnvVar{
		{Name: "HOME", Value: "/tmp"},
		{Name: "MANAGEMENT_BACKUP_RELEASE", Value: h.resourcePrefix()},
		{Name: "MANAGEMENT_BACKUP_DEST_NAME", Value: row.Name},
		{Name: "MANAGEMENT_BACKUP_BUCKET", Value: row.Bucket},
		{Name: "MANAGEMENT_BACKUP_REGION", Value: row.Region},
		{Name: "MANAGEMENT_BACKUP_PREFIX", Value: row.Prefix},
		{Name: "MANAGEMENT_BACKUP_KEEP_DAILY", Value: strconv.Itoa(int(row.KeepDaily))},
		{Name: "MANAGEMENT_BACKUP_KEEP_WEEKLY", Value: strconv.Itoa(int(row.KeepWeekly))},
		{Name: "MANAGEMENT_BACKUP_KEEP_MONTHLY", Value: strconv.Itoa(int(row.KeepMonthly))},
		h.databaseURLEnv(ctx),
	}
	if row.EndpointUrl != "" {
		env = append(env, corev1.EnvVar{Name: "MANAGEMENT_BACKUP_ENDPOINT", Value: row.EndpointUrl})
	}
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.destinationResourceName(row.ID),
			Namespace: h.namespace,
			Labels:    labels,
		},
		Spec: batchv1.CronJobSpec{
			Schedule:                   row.Schedule,
			ConcurrencyPolicy:          batchv1.ForbidConcurrent,
			SuccessfulJobsHistoryLimit: destInt32Ptr(3),
			FailedJobsHistoryLimit:     destInt32Ptr(3),
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: batchv1.JobSpec{
					BackoffLimit:            destInt32Ptr(2),
					TTLSecondsAfterFinished: destInt32Ptr(86400),
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: labels},
						Spec: corev1.PodSpec{
							RestartPolicy:      corev1.RestartPolicyOnFailure,
							ServiceAccountName: sa,
							SecurityContext: &corev1.PodSecurityContext{
								RunAsNonRoot: destBoolPtr(true),
								RunAsUser:    &nonRoot,
								RunAsGroup:   &nonRoot,
								FSGroup:      &nonRoot,
							},
							Containers: []corev1.Container{{
								Name:    "pgdump-s3",
								Image:   image,
								Command: []string{"/bin/sh", "-eu", "-c"},
								Args:    []string{managementBackupDumpScript},
								Env:     env,
								SecurityContext: &corev1.SecurityContext{
									AllowPrivilegeEscalation: &falseVal,
									ReadOnlyRootFilesystem:   destBoolPtr(true),
									Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
								},
								VolumeMounts: []corev1.VolumeMount{
									{Name: "aws-credentials", MountPath: "/var/run/aws", ReadOnly: true},
									{Name: "scratch", MountPath: "/tmp"},
								},
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("100m"),
										corev1.ResourceMemory: resource.MustParse("256Mi"),
									},
									Limits: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("1000m"),
										corev1.ResourceMemory: resource.MustParse("1Gi"),
									},
								},
							}},
							Volumes: []corev1.Volume{
								{
									Name: "aws-credentials",
									VolumeSource: corev1.VolumeSource{
										Secret: &corev1.SecretVolumeSource{
											SecretName: h.destinationResourceName(row.ID) + "-aws",
											Items:      []corev1.KeyToPath{{Key: "credentials", Path: "credentials"}},
										},
									},
								},
								{
									Name: "scratch",
									VolumeSource: corev1.VolumeSource{
										EmptyDir: &corev1.EmptyDirVolumeSource{
											SizeLimit: resourcePtr("8Gi"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (h *AdminDrillHandler) databaseURLEnv(ctx context.Context) corev1.EnvVar {
	fallback := corev1.EnvVar{
		Name: "DATABASE_URL",
		ValueFrom: &corev1.EnvVarSource{
			ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: h.resourcePrefix() + "-config"},
				Key:                  "DATABASE_URL",
			},
		},
	}
	if h.k8s == nil {
		return fallback
	}
	dep, err := h.k8s.AppsV1().Deployments(h.namespace).Get(ctx, h.resourcePrefix()+"-server", metav1.GetOptions{})
	if err != nil {
		return fallback
	}
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name != "server" {
			continue
		}
		for _, e := range c.Env {
			if e.Name == "DATABASE_URL" {
				return e
			}
		}
	}
	return fallback
}

func (h *AdminDrillHandler) probeManagementBackupS3(ctx context.Context, row sqlc.ManagementBackupDestination, accessKey, secretKey string) error {
	endpoint := strings.TrimSpace(row.EndpointUrl)
	region := row.Region
	if region == "" {
		region = "us-east-1"
	}
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://s3.%s.amazonaws.com", region)
	}
	host, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint url")
	}
	host.Path = strings.TrimRight(host.Path, "/") + "/" + row.Bucket + "/"
	q := host.Query()
	q.Set("list-type", "2")
	q.Set("max-keys", "1")
	host.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host.String(), nil)
	if err != nil {
		return err
	}
	if accessKey != "" && secretKey != "" {
		signAWSV4(req, accessKey, secretKey, region, "s3", time.Now().UTC())
	}
	client := h.httpClient
	if client == nil {
		client = httpclient.DefaultExternal()
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connectivity failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusPartialContent:
		return nil
	case http.StatusForbidden:
		return fmt.Errorf("forbidden (likely invalid credentials)")
	case http.StatusNotFound:
		return fmt.Errorf("bucket not found: %s", row.Bucket)
	default:
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func destInt32Ptr(v int32) *int32 { return &v }

func destBoolPtr(v bool) *bool { return &v }

func resourcePtr(v string) *resource.Quantity {
	q := resource.MustParse(v)
	return &q
}
