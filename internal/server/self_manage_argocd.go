package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/argolabels"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
)

const (
	localArgoInstanceName = "local"
	// ArgoCD ships as the astro-argocd subchart of the astronomer chart, in the
	// astronomer namespace — so its resources are astro-argocd-* and it is
	// reachable in-namespace (no separate argocd namespace).
	localArgoReleaseName       = "astro-argocd"
	localArgoNamespace         = "astronomer"
	localArgoAPIURL            = "http://astro-argocd-server.astronomer.svc.cluster.local/argocd"
	localArgoRepoSecretName    = "astronomer-self-repo"
	localArgoClusterSecretName = "astronomer-local-cluster"
	localArgoApplicationName   = "astronomer-self-manage"
	localArgoRepoURL           = "http://astronomer-server.astronomer.svc.cluster.local:8000/helm-repo/astronomer-v2"
	// The argo-cd subchart's fullnameOverride prefixes workloads
	// (astro-argocd-server, astro-argocd-application-controller) but its
	// ServiceAccounts keep the chart's fixed unprefixed names.
	localArgoAppControllerSA     = "argocd-application-controller"
	localArgoServerDeployment    = "astro-argocd-server"
	localArgoControllerWorkload  = "astro-argocd-application-controller"
	localArgoAppControllerTTL    = 24 * time.Hour
	localArgoBootstrapPeriod     = 30 * time.Second
	localArgoBootstrapTimeout    = 60 * time.Second
	localArgoHTTPTimeout         = 10 * time.Second
	localAstronomerReleaseName   = "astronomer"
	localAstronomerNamespace     = "astronomer"
	selfManagedDatabaseSecret    = "astronomer-self-manage-database"
	selfManagedRedisSecret       = "astronomer-self-manage-redis"
	selfManagedDexSecret         = "astronomer-self-manage-dex"
	selfManagedSecretOwnerLabel  = "astronomer.io/self-manage-credential"
	selfManagedPhaseAnnotation   = "astronomer.io/self-manage-phase"
	selfManagedHashAnnotation    = "astronomer.io/self-manage-spec-hash"
	selfManagedApproveAnnotation = "astronomer.io/self-manage-approved-hash"
	selfManagedPhaseAwaiting     = "awaiting-approval"
	selfManagedPhaseActive       = "active"
)

type selfManagedSecretRef struct {
	Name string
	Key  string
}

var argocdApplicationGVR = kubeutil.ArgoApplicationGVR

var containerImageTagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

func startLocalArgoSelfManagement(ctx context.Context, logger *slog.Logger, cfg *config.Config, queries *sqlc.Queries, toolHandler *handler.ToolHandler, encryptor *auth.Encryptor, localCluster *sqlc.Cluster) {
	if logger == nil || cfg == nil || queries == nil || toolHandler == nil || localCluster == nil {
		return
	}
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		logger.Warn("local argocd self-management disabled: not running in-cluster", "error", err)
		return
	}
	k8s, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		logger.Warn("local argocd self-management disabled: kubernetes client error", "error", err)
		return
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		logger.Warn("local argocd self-management disabled: dynamic client error", "error", err)
		return
	}

	go func() {
		ticker := time.NewTicker(localArgoBootstrapPeriod)
		defer ticker.Stop()
		for {
			runCtx, cancel := context.WithTimeout(ctx, localArgoBootstrapTimeout)
			err := reconcileLocalArgoSelfManagement(runCtx, logger, cfg, queries, encryptor, k8s, dyn, *localCluster, toolHandler)
			cancel()
			if err != nil {
				logger.Warn("local argocd self-management reconcile failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func reconcileLocalArgoSelfManagement(ctx context.Context, logger *slog.Logger, cfg *config.Config, queries *sqlc.Queries, encryptor *auth.Encryptor, k8s kubernetes.Interface, dyn dynamic.Interface, localCluster sqlc.Cluster, toolHandler *handler.ToolHandler) error {
	// ArgoCD ships as the bundled astro-argocd subchart of the astronomer
	// release, so it is already installed. We just wait for it to be ready
	// instead of helm-installing it as a separate tool — that would collide on
	// the argoproj.io CRDs already owned by the astronomer release.
	if err := waitForDeploymentReady(ctx, k8s, localArgoNamespace, localArgoServerDeployment); err != nil {
		return fmt.Errorf("argocd-server not ready: %w", err)
	}
	// A legacy Application may contain plaintext in spec/status/history. Gate
	// the entire reconcile before any Secret or Application mutation; the same
	// check is repeated at the full-object scrub write boundary.
	if err := preflightSelfManagedApplicationCredentialMigration(ctx, k8s, dyn); err != nil {
		return err
	}

	token, tokenErr := loginToArgoCDWithInitialAdminSecret(ctx, k8s, localArgoAPIURL)
	instance, err := ensureLocalArgoInstanceRow(ctx, queries, encryptor, localCluster.ID, token)
	if err != nil {
		return fmt.Errorf("ensure argocd instance row: %w", err)
	}
	if tokenErr != nil && logger != nil {
		logger.Warn("argocd instance row created without upstream session token", "error", tokenErr)
	}

	projects, err := localArgoProjectsForCluster(ctx, queries, localCluster.ID)
	if err != nil {
		return fmt.Errorf("list local cluster projects: %w", err)
	}
	clusterSecretName, err := ensureLocalArgoClusterSecret(ctx, k8s, localCluster, projects)
	if err != nil {
		return fmt.Errorf("ensure argocd local cluster secret: %w", err)
	}
	if err := ensureLocalManagedClusterRow(ctx, queries, instance.ID, localCluster, clusterSecretName, projects); err != nil {
		return fmt.Errorf("ensure argocd managed cluster row: %w", err)
	}
	if err := ensureLocalArgoRepoSecret(ctx, k8s); err != nil {
		return fmt.Errorf("ensure argocd repo secret: %w", err)
	}
	// Single-owner stand-down (H6): the server-pushed baseline ApplicationSets
	// and the agent's PULL reconcile loop both render the astronomer-* footprint
	// (e.g. kube-state-metrics/node-exporter), so exactly one may own it. When
	// PullReconcileEnabled is false (the default, soak-validated admin path) push
	// generates the baseline exactly as before. When it is true the pull loop on
	// every agent owns the footprint, so push stands down AND tears down any
	// appset it previously created (a flip-to-pull prunes them; a green-field
	// pull deploy never created them, so the teardown is a no-op).
	if !cfg.PullReconcileEnabled {
		if argoCDManagePlatformBaselineEnabled(ctx, queries) {
			if err := ensureBaselineApplicationSets(ctx, dyn, queries); err != nil {
				return fmt.Errorf("ensure baseline applicationsets: %w", err)
			}
		}
	} else if err := removeBaselineApplicationSets(ctx, dyn); err != nil {
		return fmt.Errorf("remove baseline applicationsets: %w", err)
	}

	platform, err := queries.GetPlatformConfig(ctx)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return err
	}
	serverURL := strings.TrimSpace(platform.ServerUrl)
	if serverURL == "" {
		return nil
	}
	currentSafeValues := currentReferenceOnlySelfManagedValues(ctx, dyn)
	valuesYAML, err := buildSelfManagedAstronomerValues(ctx, cfg, k8s, serverURL, currentSafeValues)
	if err != nil {
		return fmt.Errorf("build self-managed values: %w", err)
	}
	if err := ensureSelfManagedAstronomerApplication(ctx, k8s, dyn, localCluster, valuesYAML); err != nil {
		return fmt.Errorf("ensure self-managed application: %w", err)
	}
	return nil
}

func waitForDeploymentReady(ctx context.Context, k8s kubernetes.Interface, namespace, name string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		deploy, err := k8s.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil && deploy.Status.AvailableReplicas >= 1 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func ensureLocalArgoInstanceRow(ctx context.Context, queries *sqlc.Queries, encryptor *auth.Encryptor, clusterID uuid.UUID, token string) (sqlc.ArgocdInstance, error) {
	authColumn := strings.TrimSpace(token)
	if encryptor != nil && authColumn != "" {
		ciphertext, err := encryptor.Encrypt(authColumn)
		if err != nil {
			return sqlc.ArgocdInstance{}, err
		}
		authColumn = ciphertext
	}
	instance, err := queries.GetArgoCDInstanceByName(ctx, localArgoInstanceName)
	if err == nil {
		if authColumn == "" {
			authColumn = instance.AuthTokenEncrypted
		}
		return queries.UpdateArgoCDInstance(ctx, sqlc.UpdateArgoCDInstanceParams{
			ID:                 instance.ID,
			Name:               localArgoInstanceName,
			ApiUrl:             localArgoAPIURL,
			AuthTokenEncrypted: authColumn,
			VerifySsl:          false,
		})
	}
	if err != pgx.ErrNoRows {
		return sqlc.ArgocdInstance{}, err
	}
	return queries.CreateArgoCDInstance(ctx, sqlc.CreateArgoCDInstanceParams{
		Name:               localArgoInstanceName,
		ClusterID:          clusterID,
		ApiUrl:             localArgoAPIURL,
		AuthTokenEncrypted: authColumn,
		VerifySsl:          false,
	})
}

func ensureLocalManagedClusterRow(ctx context.Context, queries *sqlc.Queries, instanceID uuid.UUID, cluster sqlc.Cluster, secretName string, projects []sqlc.Project) error {
	labels, _ := json.Marshal(localArgoManagedClusterLabelsForProjects(cluster, projects))
	_, err := queries.CreateArgoCDManagedCluster(ctx, sqlc.CreateArgoCDManagedClusterParams{
		ArgocdInstanceID:  instanceID,
		ClusterID:         cluster.ID,
		ClusterSecretName: secretName,
		ServerUrl:         cluster.ApiServerUrl,
		Labels:            labels,
	})
	return err
}

func ensureLocalArgoRepoSecret(ctx context.Context, k8s kubernetes.Interface) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      localArgoRepoSecretName,
			Namespace: localArgoNamespace,
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "repository",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name": []byte("astronomer-local"),
			"type": []byte("helm"),
			"url":  []byte(localArgoRepoURL),
		},
	}
	return applyLocalArgoSecret(ctx, k8s, secret)
}

func ensureLocalArgoClusterSecret(ctx context.Context, k8s kubernetes.Interface, cluster sqlc.Cluster, projects []sqlc.Project) (string, error) {
	token, err := createLocalArgoApplicationControllerToken(ctx, k8s)
	if err != nil {
		return "", err
	}
	cfg := map[string]any{
		"bearerToken": token,
		"tlsClientConfig": map[string]any{
			"insecure": cluster.CaCertificate == "",
		},
	}
	if cluster.CaCertificate != "" {
		cfg["tlsClientConfig"].(map[string]any)["caData"] = cluster.CaCertificate
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      localArgoClusterSecretName,
			Namespace: localArgoNamespace,
			Labels:    localArgoClusterSecretLabelsForProjects(cluster, projects),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(cluster.Name),
			"server": []byte(cluster.ApiServerUrl),
			"config": cfgJSON,
		},
	}
	return localArgoClusterSecretName, applyLocalArgoSecret(ctx, k8s, secret)
}

func localArgoClusterSecretLabelsForProjects(cluster sqlc.Cluster, projects []sqlc.Project) map[string]string {
	labels := localArgoManagedClusterLabelsForProjects(cluster, projects)
	labels[argolabels.ArgoCDClusterSecretTypeLabel] = argolabels.ArgoCDClusterSecretTypeValue
	return labels
}

func localArgoManagedClusterLabelsForProjects(cluster sqlc.Cluster, projects []sqlc.Project) map[string]string {
	cluster.IsLocal = true
	return argolabels.ManagedClusterLabels(cluster, projects)
}

func localArgoProjectsForCluster(ctx context.Context, queries *sqlc.Queries, clusterID uuid.UUID) ([]sqlc.Project, error) {
	return queries.ListProjectsByCluster(ctx, sqlc.ListProjectsByClusterParams{ClusterID: clusterID, Limit: 1000, Offset: 0})
}

func createLocalArgoApplicationControllerToken(ctx context.Context, k8s kubernetes.Interface) (string, error) {
	duration := int64(localArgoAppControllerTTL.Seconds())
	req, err := k8s.CoreV1().ServiceAccounts(localArgoNamespace).CreateToken(ctx, localArgoAppControllerSA, &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{
			ExpirationSeconds: &duration,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(req.Status.Token), nil
}

func applyLocalArgoSecret(ctx context.Context, k8s kubernetes.Interface, secret *corev1.Secret) error {
	// Argo's own cluster controller also writes to astronomer-local-cluster
	// (status fields, last-seen timestamps) on its own cadence, so a naive
	// Get→Update on every 30s reconcile tick lost roughly every other write
	// to a stale resourceVersion. retry.RetryOnConflict re-fetches the row
	// and reapplies the patch on Conflict, which is the standard k8s
	// pattern for this. Other failure modes (network, NotFound on the
	// initial Create path) bubble up unchanged.
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := k8s.CoreV1().Secrets(secret.Namespace).Get(ctx, secret.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = k8s.CoreV1().Secrets(secret.Namespace).Create(ctx, secret, metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		current.Labels = secret.Labels
		if secret.Annotations != nil {
			if current.Annotations == nil {
				current.Annotations = map[string]string{}
			}
			for key, value := range secret.Annotations {
				current.Annotations[key] = value
			}
		}
		current.Type = secret.Type
		current.Data = secret.Data
		_, err = k8s.CoreV1().Secrets(secret.Namespace).Update(ctx, current, metav1.UpdateOptions{})
		return err
	})
}

func selfManagedPublicListenerValues(ctx context.Context, k8s kubernetes.Interface, fallbackHost string) (map[string]any, error) {
	ingress, err := k8s.NetworkingV1().Ingresses(localAstronomerNamespace).Get(ctx, localAstronomerReleaseName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("read existing ingress for self-managed values: %w", err)
	}
	if err == nil {
		return selfManagedIngressValues(ingress, fallbackHost), nil
	}
	return map[string]any{
		"ingress": map[string]any{
			"enabled": false,
		},
		"gateway": map[string]any{
			"enabled":   true,
			"className": "nginx",
			"hosts":     []string{fallbackHost},
		},
	}, nil
}

func selfManagedIngressValues(ingress *networkingv1.Ingress, fallbackHost string) map[string]any {
	host := fallbackHost
	for _, rule := range ingress.Spec.Rules {
		if strings.TrimSpace(rule.Host) != "" {
			host = rule.Host
			break
		}
	}
	className := "nginx"
	if ingress.Spec.IngressClassName != nil && strings.TrimSpace(*ingress.Spec.IngressClassName) != "" {
		className = *ingress.Spec.IngressClassName
	}
	values := map[string]any{
		"ingress": map[string]any{
			"enabled":   true,
			"className": className,
			"host":      host,
		},
		"gateway": map[string]any{
			"enabled": false,
		},
	}
	for _, tls := range ingress.Spec.TLS {
		if strings.TrimSpace(tls.SecretName) != "" {
			values["tls"] = map[string]any{
				"source":     "secret",
				"secretName": tls.SecretName,
			}
			break
		}
	}
	return values
}

func deploymentImages(ctx context.Context, k8s kubernetes.Interface, namespace, name string) (string, int32, string, error) {
	deploy, err := k8s.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", 0, "", err
	}
	replicas := int32(1)
	if deploy.Spec.Replicas != nil {
		replicas = *deploy.Spec.Replicas
	}
	var mainImage string
	var migrateImage string
	for _, c := range deploy.Spec.Template.Spec.Containers {
		if c.Name == "server" || c.Name == "worker" || c.Name == "frontend" {
			_, err = parseImageRef(c.Image)
			if err != nil {
				return "", 0, "", fmt.Errorf("deployment %s primary image: %w", name, err)
			}
			mainImage = c.Image
			break
		}
	}
	for _, c := range deploy.Spec.Template.Spec.InitContainers {
		if c.Name == "migrate" {
			_, err = parseImageRef(c.Image)
			if err != nil {
				return "", 0, "", fmt.Errorf("deployment %s migrate image: %w", name, err)
			}
			migrateImage = c.Image
			break
		}
	}
	if mainImage == "" {
		return "", 0, "", fmt.Errorf("deployment %s has no primary image", name)
	}
	return mainImage, replicas, migrateImage, nil
}

func parseImageRef(ref string) (map[string]any, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("image reference is empty")
	}
	if strings.ContainsAny(ref, " \t\r\n") || strings.Contains(ref, "://") {
		return nil, fmt.Errorf("image reference %q is malformed", ref)
	}
	if strings.Contains(ref, "@") {
		return nil, fmt.Errorf("image reference %q uses a digest; self-management currently supports tag references only", ref)
	}

	lastSlash := strings.LastIndex(ref, "/")
	lastColon := strings.LastIndex(ref, ":")
	if lastColon <= lastSlash || lastColon == len(ref)-1 {
		return nil, fmt.Errorf("image reference %q must include an explicit tag", ref)
	}
	name, tag := ref[:lastColon], ref[lastColon+1:]
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "//") || strings.HasSuffix(name, "/") {
		return nil, fmt.Errorf("image reference %q has an invalid repository path", ref)
	}
	if !containerImageTagPattern.MatchString(tag) {
		return nil, fmt.Errorf("image reference %q has invalid tag %q", ref, tag)
	}

	registry := ""
	repository := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		registry, repository = name[:i], name[i+1:]
	}
	if repository == "" {
		return nil, fmt.Errorf("image reference %q has an invalid repository path", ref)
	}
	return map[string]any{
		"registry":   registry,
		"repository": repository,
		"tag":        tag,
	}, nil
}

func loginToArgoCDWithInitialAdminSecret(ctx context.Context, k8s kubernetes.Interface, apiURL string) (string, error) {
	secret, err := k8s.CoreV1().Secrets(localArgoNamespace).Get(ctx, "argocd-initial-admin-secret", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	password := strings.TrimSpace(string(secret.Data["password"]))
	if password == "" {
		return "", fmt.Errorf("argocd initial admin password is empty")
	}
	payload, _ := json.Marshal(map[string]string{
		"username": "admin",
		"password": password,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(apiURL, "/")+"/api/v1/session", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpclient.New(localArgoHTTPTimeout).Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("argocd session login failed with status %d", resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.Token) == "" {
		return "", fmt.Errorf("argocd session login returned empty token")
	}
	return out.Token, nil
}

func yamlOrPanic(v any) []byte {
	data, err := yaml.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
