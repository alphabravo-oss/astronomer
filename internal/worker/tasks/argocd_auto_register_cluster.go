package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	argocdclient "github.com/alphabravocompany/astronomer-go/internal/handler/argocd"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const ArgoCDAutoRegisterClusterType = "argocd:auto_register_cluster"

const (
	argoCDApplicationControllerSA     = "argocd-application-controller"
	platformSettingArgoCDAutoAdoptKey = "argocd.auto_adopt_clusters"
	ConditionArgoCDAdopted            = "ArgoCDAdopted"
)

var argoCDProxyTokenTTL = 180 * 24 * time.Hour

type ArgoCDAutoRegisterQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
	ListArgoCDInstances(ctx context.Context, arg sqlc.ListArgoCDInstancesParams) ([]sqlc.ArgocdInstance, error)
	CreateArgoCDManagedCluster(ctx context.Context, arg sqlc.CreateArgoCDManagedClusterParams) (sqlc.ArgocdManagedCluster, error)
	GetActiveArgoCDClusterProxyTokenByClusterID(ctx context.Context, clusterID uuid.UUID) (sqlc.ArgocdClusterProxyToken, error)
	UpsertArgoCDClusterProxyToken(ctx context.Context, arg sqlc.UpsertArgoCDClusterProxyTokenParams) (sqlc.ArgocdClusterProxyToken, error)
}

type ArgoCDAutoRegisterDeps struct {
	Queries             ArgoCDAutoRegisterQuerier
	Encryptor           *auth.Encryptor
	K8s                 kubernetes.Interface
	ClusterProxyBaseURL string
	Registration        ArgoCDRegistrationTimeline
}

var argoCDAutoRegisterDeps ArgoCDAutoRegisterDeps

type ArgoCDRegistrationTimeline interface {
	WriteStep(ctx context.Context, clusterID uuid.UUID, in registration.StepInput) (sqlc.ClusterRegistrationStep, error)
}

type argoCDConditionWriter interface {
	UpsertClusterCondition(ctx context.Context, arg sqlc.UpsertClusterConditionParams) (sqlc.ClusterCondition, error)
}

type argoCDManagedClusterLister interface {
	ListArgoCDManagedClustersByCluster(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ArgocdManagedCluster, error)
}

func ConfigureArgoCDAutoRegister(deps ArgoCDAutoRegisterDeps) {
	deps.ClusterProxyBaseURL = strings.TrimRight(strings.TrimSpace(deps.ClusterProxyBaseURL), "/")
	argoCDAutoRegisterDeps = deps
}

func ResetArgoCDAutoRegister() {
	argoCDAutoRegisterDeps = ArgoCDAutoRegisterDeps{}
}

type ArgoCDAutoRegisterClusterPayload struct {
	ClusterID string `json:"cluster_id,omitempty"`
}

func NewArgoCDAutoRegisterClusterTask(clusterID uuid.UUID) (*asynq.Task, error) {
	data, err := json.Marshal(ArgoCDAutoRegisterClusterPayload{ClusterID: clusterID.String()})
	if err != nil {
		return nil, fmt.Errorf("marshal argocd auto-register payload: %w", err)
	}
	return asynq.NewTask(ArgoCDAutoRegisterClusterType, data, asynq.MaxRetry(5), asynq.Unique(10*time.Minute)), nil
}

func HandleArgoCDAutoRegisterCluster(ctx context.Context, t *asynq.Task) error {
	deps := argoCDAutoRegisterDeps
	if deps.Queries == nil {
		runtimeLogger().InfoContext(ctx, "argocd auto-register runtime not configured, skipping")
		return nil
	}
	enabled, err := readArgoCDAutoAdoptSetting(ctx, deps.Queries)
	if err != nil {
		return err
	}
	if !enabled {
		runtimeLogger().InfoContext(ctx, "argocd auto-register disabled by platform setting")
		return nil
	}
	var p ArgoCDAutoRegisterClusterPayload
	if len(t.Payload()) > 0 {
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal argocd auto-register payload: %w", err)
		}
	}
	if strings.TrimSpace(p.ClusterID) != "" {
		clusterID, err := uuid.Parse(p.ClusterID)
		if err != nil {
			return fmt.Errorf("invalid cluster_id: %w", err)
		}
		cluster, err := deps.Queries.GetClusterByID(ctx, clusterID)
		if err != nil {
			return err
		}
		return autoRegisterClusterIntoArgoCD(ctx, deps, cluster)
	}

	runStarted := time.Now().UTC()
	clusters, err := deps.Queries.ListClusters(ctx, sqlc.ListClustersParams{Limit: 1000, Offset: 0})
	if err != nil {
		runErr := fmt.Errorf("list clusters: %w", err)
		recordRepairJobFailure(ctx, deps.Queries, ArgoCDAutoRegisterClusterType, runErr, map[string]any{"mode": "sweep"})
		return runErr
	}
	instances, err := deps.Queries.ListArgoCDInstances(ctx, sqlc.ListArgoCDInstancesParams{Limit: 1000, Offset: 0})
	if err != nil {
		runErr := fmt.Errorf("list argocd instances for repair: %w", err)
		recordRepairJobFailure(ctx, deps.Queries, ArgoCDAutoRegisterClusterType, runErr, map[string]any{
			"mode":             "sweep",
			"clusters_checked": len(clusters),
		})
		return runErr
	}
	var firstErr error
	eligibleClusters := 0
	for _, cluster := range clusters {
		if !cluster.IsLocal && !cluster.LastHeartbeat.Valid {
			continue
		}
		eligibleClusters++
		if err := autoRegisterClusterIntoArgoCD(ctx, deps, cluster); err != nil {
			runtimeLogger().WarnContext(ctx, "argocd auto-register failed",
				"cluster_id", cluster.ID.String(),
				"error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	metadata := map[string]any{
		"mode":              "sweep",
		"clusters_listed":   len(clusters),
		"eligible_clusters": eligibleClusters,
		"argocd_instances":  len(instances),
		"duration_ms":       time.Since(runStarted).Milliseconds(),
	}
	if err := repairArgoCDManagedClusterIndex(ctx, deps, instances); err != nil {
		runtimeLogger().WarnContext(ctx, "argocd managed-cluster index repair failed", "error", err)
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		recordRepairJobFailure(ctx, deps.Queries, ArgoCDAutoRegisterClusterType, firstErr, metadata)
	} else {
		recordRepairJobSuccess(ctx, deps.Queries, ArgoCDAutoRegisterClusterType, metadata)
	}
	return firstErr
}

func readArgoCDAutoAdoptSetting(ctx context.Context, q ArgoCDAutoRegisterQuerier) (bool, error) {
	row, err := q.GetPlatformSetting(ctx, platformSettingArgoCDAutoAdoptKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil
		}
		return false, fmt.Errorf("read %s setting: %w", platformSettingArgoCDAutoAdoptKey, err)
	}
	var enabled bool
	if err := json.Unmarshal(row.Value, &enabled); err != nil {
		return false, fmt.Errorf("parse %s setting: %w", platformSettingArgoCDAutoAdoptKey, err)
	}
	return enabled, nil
}

func autoRegisterClusterIntoArgoCD(ctx context.Context, deps ArgoCDAutoRegisterDeps, cluster sqlc.Cluster) error {
	if !cluster.IsLocal && !cluster.LastHeartbeat.Valid {
		return nil
	}
	alreadyManaged := argoCDClusterAlreadyManaged(ctx, deps, cluster.ID)
	if !alreadyManaged {
		writeArgoCDRegistrationStep(ctx, deps, cluster.ID, "argocd_registering", "running", nil, "")
		upsertArgoCDAdoptionCondition(ctx, deps, cluster.ID, "Unknown", "RegistrationInProgress", "Astronomer is registering this cluster into ArgoCD.")
	}
	instances, err := deps.Queries.ListArgoCDInstances(ctx, sqlc.ListArgoCDInstancesParams{Limit: 1000, Offset: 0})
	if err != nil {
		recordArgoCDRegistrationFailure(ctx, deps, cluster.ID, err)
		return fmt.Errorf("list argocd instances: %w", err)
	}
	if len(instances) == 0 {
		if alreadyManaged {
			upsertArgoCDAdoptionCondition(ctx, deps, cluster.ID, "True", "Registered", "Cluster already has an ArgoCD managed-cluster record.")
			return nil
		}
		recordArgoCDRegistrationFailure(ctx, deps, cluster.ID, errors.New("no ArgoCD instances configured"))
		return nil
	}
	var firstErr error
	for _, instance := range instances {
		if err := autoRegisterClusterIntoInstance(ctx, deps, instance, cluster); err != nil {
			runtimeLogger().WarnContext(ctx, "argocd auto-register instance failed",
				"cluster_id", cluster.ID.String(),
				"argocd_instance_id", instance.ID.String(),
				"error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if firstErr != nil {
		recordArgoCDRegistrationFailure(ctx, deps, cluster.ID, firstErr)
		return firstErr
	}
	upsertArgoCDAdoptionCondition(ctx, deps, cluster.ID, "True", "Registered", "Cluster is registered into ArgoCD for baseline reconciliation.")
	if !alreadyManaged {
		writeArgoCDRegistrationStep(ctx, deps, cluster.ID, "argocd_registered", "success", map[string]any{
			"instances": len(instances),
		}, "")
		if !cluster.IsLocal {
			writeArgoCDRegistrationStep(ctx, deps, cluster.ID, "baseline_appsets_matched", "success", map[string]any{
				"selector": "astronomer.io/managed-by=astronomer, astronomer.io/is-local=false",
			}, "")
		}
	}
	return nil
}

func repairArgoCDManagedClusterIndex(ctx context.Context, deps ArgoCDAutoRegisterDeps, instances []sqlc.ArgocdInstance) error {
	if deps.K8s == nil {
		runtimeLogger().InfoContext(ctx, "argocd managed-cluster index repair skipped: kubernetes client not configured")
		return nil
	}
	lister, ok := deps.Queries.(argoCDManagedClusterLister)
	if !ok {
		return nil
	}
	if len(instances) == 0 {
		return nil
	}
	if len(instances) > 1 {
		runtimeLogger().WarnContext(ctx, "argocd managed-cluster index repair skipped: multiple argocd instances configured")
		return nil
	}
	instance := instances[0]
	secrets, err := deps.K8s.CoreV1().Secrets(argoCDNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: argoCDClusterSecretTypeLabel + "=" + argoCDClusterSecretTypeValue,
	})
	if err != nil {
		return fmt.Errorf("list argocd cluster secrets: %w", err)
	}
	var firstErr error
	for i := range secrets.Items {
		if err := repairArgoCDManagedClusterIndexForSecret(ctx, deps, lister, instance, &secrets.Items[i]); err != nil {
			runtimeLogger().WarnContext(ctx, "argocd managed-cluster index repair failed for secret",
				"secret", secrets.Items[i].Name,
				"error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func repairArgoCDManagedClusterIndexForSecret(ctx context.Context, deps ArgoCDAutoRegisterDeps, lister argoCDManagedClusterLister, instance sqlc.ArgocdInstance, secret *corev1.Secret) error {
	if secret == nil || secret.Labels[astronomerManagedByLabelKey] != astronomerManagedByLabelValue {
		return nil
	}
	clusterIDRaw := strings.TrimSpace(secret.Labels[astronomerClusterIDLabelKey])
	if clusterIDRaw == "" {
		return nil
	}
	clusterID, err := uuid.Parse(clusterIDRaw)
	if err != nil {
		runtimeLogger().WarnContext(ctx, "argocd managed-cluster index repair skipped secret with invalid cluster id",
			"secret", secret.Name,
			"cluster_id", clusterIDRaw)
		return nil
	}
	rows, err := lister.ListArgoCDManagedClustersByCluster(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("list managed-cluster rows for %s: %w", clusterID, err)
	}
	for _, row := range rows {
		if row.ArgocdInstanceID == instance.ID {
			return nil
		}
	}
	cluster, err := deps.Queries.GetClusterByID(ctx, clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			runtimeLogger().WarnContext(ctx, "argocd managed-cluster index repair found secret for missing cluster",
				"secret", secret.Name,
				"cluster_id", clusterID.String())
			return nil
		}
		return fmt.Errorf("get cluster %s: %w", clusterID, err)
	}
	if cluster.DecommissionedAt.Valid {
		runtimeLogger().WarnContext(ctx, "argocd managed-cluster index repair found secret for decommissioned cluster",
			"secret", secret.Name,
			"cluster_id", clusterID.String())
		return nil
	}
	desired := managedClusterArgoLabels(cluster)
	if err := refreshSingleManagedClusterSecret(ctx, deps.K8s, sqlc.ArgocdManagedCluster{
		ClusterSecretName: secret.Name,
		ServerUrl:         strings.TrimSpace(string(secret.Data["server"])),
	}, desired); err != nil {
		return fmt.Errorf("refresh repaired secret labels: %w", err)
	}
	labelsJSON, err := json.Marshal(desired)
	if err != nil {
		return fmt.Errorf("marshal repaired labels: %w", err)
	}
	_, err = deps.Queries.CreateArgoCDManagedCluster(ctx, sqlc.CreateArgoCDManagedClusterParams{
		ArgocdInstanceID:  instance.ID,
		ClusterID:         clusterID,
		ClusterSecretName: secret.Name,
		ServerUrl:         strings.TrimSpace(string(secret.Data["server"])),
		Labels:            labelsJSON,
	})
	if err != nil {
		return fmt.Errorf("recreate managed-cluster row: %w", err)
	}
	upsertArgoCDAdoptionCondition(ctx, deps, clusterID, "True", "Registered", "Cluster is registered into ArgoCD for baseline reconciliation.")
	return nil
}

func autoRegisterClusterIntoInstance(ctx context.Context, deps ArgoCDAutoRegisterDeps, instance sqlc.ArgocdInstance, cluster sqlc.Cluster) error {
	token, server, tlsConfig, err := managedClusterCredential(ctx, deps, cluster)
	if err != nil {
		return err
	}
	instanceToken, err := decryptArgoCDInstanceToken(deps.Encryptor, instance)
	if err != nil {
		return err
	}
	client := argocdclient.NewClient(instance.ApiUrl, instanceToken, argocdclient.Options{
		VerifySSL: instance.VerifySsl,
	})
	labels := managedClusterArgoLabels(cluster)
	upstream, err := client.RegisterCluster(ctx, argocdclient.ClusterRegistration{
		Server: server,
		Name:   cluster.Name,
		Config: argocdclient.ClusterConfig{
			BearerToken:     token,
			TLSClientConfig: tlsConfig,
		},
		Labels: labels,
		Upsert: true,
	})
	if err != nil {
		return fmt.Errorf("register cluster with argocd: %w", err)
	}
	labelsJSON, _ := json.Marshal(labels)
	_, err = deps.Queries.CreateArgoCDManagedCluster(ctx, sqlc.CreateArgoCDManagedClusterParams{
		ArgocdInstanceID:  instance.ID,
		ClusterID:         cluster.ID,
		ClusterSecretName: firstNonEmptyArgoString(upstream.Name, cluster.Name),
		ServerUrl:         server,
		Labels:            labelsJSON,
	})
	if err != nil {
		return fmt.Errorf("record managed cluster: %w", err)
	}
	return nil
}

func managedClusterCredential(ctx context.Context, deps ArgoCDAutoRegisterDeps, cluster sqlc.Cluster) (string, string, *argocdclient.TLSClientConfig, error) {
	if cluster.IsLocal {
		if strings.TrimSpace(cluster.ApiServerUrl) == "" {
			return "", "", nil, fmt.Errorf("local cluster %s has no api_server_url", cluster.ID)
		}
		token, err := createArgoCDApplicationControllerToken(ctx, deps.K8s)
		if err != nil {
			return "", "", nil, err
		}
		return token, strings.TrimSpace(cluster.ApiServerUrl), &argocdclient.TLSClientConfig{
			Insecure: cluster.CaCertificate == "",
			CAData:   []byte(cluster.CaCertificate),
		}, nil
	}
	if deps.Encryptor == nil {
		return "", "", nil, fmt.Errorf("encryptor not configured for argocd cluster proxy token")
	}
	if deps.ClusterProxyBaseURL == "" {
		return "", "", nil, fmt.Errorf("argocd cluster proxy base URL is not configured")
	}
	token, err := ensureArgoCDClusterProxyToken(ctx, deps, cluster.ID)
	if err != nil {
		return "", "", nil, err
	}
	server := fmt.Sprintf("%s/api/v1/internal/argocd/clusters/%s/k8s", deps.ClusterProxyBaseURL, cluster.ID.String())
	return token, server, nil, nil
}

func ensureArgoCDClusterProxyToken(ctx context.Context, deps ArgoCDAutoRegisterDeps, clusterID uuid.UUID) (string, error) {
	row, err := deps.Queries.GetActiveArgoCDClusterProxyTokenByClusterID(ctx, clusterID)
	if err == nil {
		token, decErr := deps.Encryptor.Decrypt(row.TokenEncrypted)
		if decErr == nil && strings.HasPrefix(token, auth.ArgoCDClusterProxyTokenPrefix) {
			return token, nil
		}
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("lookup argocd cluster proxy token: %w", err)
	}
	token, err := auth.GenerateArgoCDClusterProxyToken()
	if err != nil {
		return "", err
	}
	encrypted, err := deps.Encryptor.Encrypt(token)
	if err != nil {
		return "", fmt.Errorf("encrypt argocd cluster proxy token: %w", err)
	}
	expiresAt := pgtype.Timestamptz{Time: time.Now().UTC().Add(argoCDProxyTokenTTL), Valid: true}
	_, err = deps.Queries.UpsertArgoCDClusterProxyToken(ctx, sqlc.UpsertArgoCDClusterProxyTokenParams{
		ClusterID:      clusterID,
		TokenHash:      auth.HashArgoCDClusterProxyToken(token),
		TokenPrefix:    auth.ArgoCDClusterProxyTokenDisplayPrefix(token),
		TokenEncrypted: encrypted,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		return "", fmt.Errorf("upsert argocd cluster proxy token: %w", err)
	}
	return token, nil
}

func createArgoCDApplicationControllerToken(ctx context.Context, k8s kubernetes.Interface) (string, error) {
	if k8s == nil {
		return "", fmt.Errorf("kubernetes client not configured")
	}
	duration := int64((24 * time.Hour).Seconds())
	req, err := k8s.CoreV1().ServiceAccounts(argoCDNamespace).CreateToken(ctx, argoCDApplicationControllerSA, &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{ExpirationSeconds: &duration},
	}, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create argocd application-controller token: %w", err)
	}
	return strings.TrimSpace(req.Status.Token), nil
}

func decryptArgoCDInstanceToken(encryptor *auth.Encryptor, instance sqlc.ArgocdInstance) (string, error) {
	raw := strings.TrimSpace(instance.AuthTokenEncrypted)
	if raw == "" {
		return "", fmt.Errorf("argocd instance %s has no auth token", instance.ID)
	}
	if encryptor == nil {
		return raw, nil
	}
	token, err := encryptor.Decrypt(raw)
	if err != nil {
		return "", fmt.Errorf("decrypt argocd instance token: %w", err)
	}
	return strings.TrimSpace(token), nil
}

func firstNonEmptyArgoString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func argoCDClusterAlreadyManaged(ctx context.Context, deps ArgoCDAutoRegisterDeps, clusterID uuid.UUID) bool {
	lister, ok := deps.Queries.(argoCDManagedClusterLister)
	if !ok {
		return false
	}
	rows, err := lister.ListArgoCDManagedClustersByCluster(ctx, clusterID)
	return err == nil && len(rows) > 0
}

func recordArgoCDRegistrationFailure(ctx context.Context, deps ArgoCDAutoRegisterDeps, clusterID uuid.UUID, cause error) {
	msg := "ArgoCD auto-adoption failed."
	if cause != nil {
		msg = cause.Error()
	}
	writeArgoCDRegistrationStep(ctx, deps, clusterID, "argocd_registration_failed", "failed", nil, msg)
	upsertArgoCDAdoptionCondition(ctx, deps, clusterID, "False", "RegistrationFailed", msg)
}

func writeArgoCDRegistrationStep(ctx context.Context, deps ArgoCDAutoRegisterDeps, clusterID uuid.UUID, stepName, status string, detail map[string]any, errMsg string) {
	if deps.Registration == nil {
		return
	}
	_, _ = deps.Registration.WriteStep(ctx, clusterID, registration.StepInput{
		StepName:      stepName,
		Status:        status,
		ProgressPct:   argoCDRegistrationProgress(status),
		Detail:        detail,
		ErrorMessage:  errMsg,
		MarkStarted:   status == "running",
		MarkCompleted: status == "success" || status == "failed" || status == "skipped",
	})
}

func argoCDRegistrationProgress(status string) int32 {
	switch status {
	case "success", "skipped":
		return 100
	default:
		return 0
	}
}

func upsertArgoCDAdoptionCondition(ctx context.Context, deps ArgoCDAutoRegisterDeps, clusterID uuid.UUID, status, reason, message string) {
	writer, ok := deps.Queries.(argoCDConditionWriter)
	if !ok {
		return
	}
	_, _ = writer.UpsertClusterCondition(ctx, sqlc.UpsertClusterConditionParams{
		ClusterID: clusterID,
		Type:      ConditionArgoCDAdopted,
		Status:    status,
		Reason:    reason,
		Message:   message,
	})
}
