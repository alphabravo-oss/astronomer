package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
	if errors.Is(err, errManagementBackupReconcileActive) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Destination reconciliation is active; retry after the current generation settles")
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A destination with that name already exists")
		return
	}
	RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to persist management backup destination")
}

func (h *AdminDrillHandler) encryptDestinationCredentials(access, secret string) (string, error) {
	if h == nil || h.encryptor == nil {
		return "", errors.New("management backup credential encryption is not configured")
	}
	payload, err := json.Marshal(map[string]string{"access_key": access, "secret_key": secret})
	if err != nil {
		return "", err
	}
	return h.encryptor.Encrypt(string(payload))
}

func (h *AdminDrillHandler) decryptDestinationCredentials(row sqlc.ManagementBackupDestination) (string, string, error) {
	if h == nil || h.encryptor == nil || row.EncryptedCredentials == "" {
		return "", "", errors.New("management backup credentials are unavailable")
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

func (h *AdminDrillHandler) reconcileDestination(ctx context.Context, row sqlc.ManagementBackupDestination, access, secret string, fence func() error) error {
	if h.k8s == nil || h.namespace == "" {
		return nil
	}
	if !row.Enabled {
		return h.deleteDestinationResources(ctx, row.ID, row.DesiredGeneration, fence)
	}
	if err := fence(); err != nil {
		return err
	}
	if err := h.upsertDestinationSecret(ctx, row.ID, row.DesiredGeneration, access, secret, fence); err != nil {
		return err
	}
	if err := fence(); err != nil {
		return err
	}
	return h.upsertDestinationCronJob(ctx, row, fence)
}

func (h *AdminDrillHandler) deleteDestinationResources(ctx context.Context, id uuid.UUID, generation int64, fence func() error) error {
	if h.k8s == nil || h.namespace == "" {
		return nil
	}
	name := h.destinationResourceName(id)
	cronJobs := h.k8s.BatchV1().CronJobs(h.namespace)
	cronJob, err := cronJobs.Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		if managementBackupObjectGeneration(cronJob.Annotations) > generation {
			return errManagementBackupStaleGeneration
		}
		if err = fence(); err != nil {
			return err
		}
		rv := cronJob.ResourceVersion
		if err = cronJobs.Delete(ctx, name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{ResourceVersion: &rv}}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	secrets := h.k8s.CoreV1().Secrets(h.namespace)
	secret, err := secrets.Get(ctx, name+"-aws", metav1.GetOptions{})
	if err == nil {
		if managementBackupObjectGeneration(secret.Annotations) > generation {
			return errManagementBackupStaleGeneration
		}
		if err = fence(); err != nil {
			return err
		}
		rv := secret.ResourceVersion
		if err = secrets.Delete(ctx, name+"-aws", metav1.DeleteOptions{Preconditions: &metav1.Preconditions{ResourceVersion: &rv}}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (h *AdminDrillHandler) upsertDestinationSecret(ctx context.Context, id uuid.UUID, generation int64, access, secret string, fence func() error) error {
	name := h.destinationResourceName(id) + "-aws"
	body := fmt.Sprintf("[default]\naws_access_key_id = %s\naws_secret_access_key = %s\n", access, secret)
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: h.namespace,
			Labels:    h.destinationLabels(id),
			Annotations: map[string]string{
				"astronomer.io/management-backup-generation": strconv.FormatInt(generation, 10),
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"credentials": []byte(body)},
	}
	if existing, err := h.k8s.CoreV1().Secrets(h.namespace).Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		if err = fence(); err != nil {
			return err
		}
		_, err = h.k8s.CoreV1().Secrets(h.namespace).Create(ctx, sec, metav1.CreateOptions{})
		return err
	} else if err != nil {
		return err
	} else {
		if managementBackupObjectGeneration(existing.Annotations) > generation {
			return errManagementBackupStaleGeneration
		}
		sec.ResourceVersion = existing.ResourceVersion
	}
	if err := fence(); err != nil {
		return err
	}
	_, err := h.k8s.CoreV1().Secrets(h.namespace).Update(ctx, sec, metav1.UpdateOptions{})
	return err
}

func (h *AdminDrillHandler) upsertDestinationCronJob(ctx context.Context, row sqlc.ManagementBackupDestination, fence func() error) error {
	name := h.destinationResourceName(row.ID)
	cj := h.buildDestinationCronJob(ctx, row)
	if existing, err := h.k8s.BatchV1().CronJobs(h.namespace).Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		if err = fence(); err != nil {
			return err
		}
		_, err = h.k8s.BatchV1().CronJobs(h.namespace).Create(ctx, cj, metav1.CreateOptions{})
		return err
	} else if err != nil {
		return err
	} else {
		if managementBackupObjectGeneration(existing.Annotations) > row.DesiredGeneration {
			return errManagementBackupStaleGeneration
		}
		cj.ResourceVersion = existing.ResourceVersion
		if err = fence(); err != nil {
			return err
		}
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
			Annotations: map[string]string{
				"astronomer.io/management-backup-generation": strconv.FormatInt(row.DesiredGeneration, 10),
			},
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
