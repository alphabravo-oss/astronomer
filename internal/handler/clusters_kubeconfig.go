package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"sigs.k8s.io/yaml"
)

// SetDirectKubeconfigRequester wires the tunnel used for the dedicated
// read-only ServiceAccount TokenRequest. No direct credential can be issued
// while this dependency is absent.
func (h *ClusterHandler) SetDirectKubeconfigRequester(r K8sRequester) {
	if h != nil {
		h.directRequester = r
	}
}

// GenerateKubeconfig handles POST /api/v1/clusters/{id}/generate-kubeconfig/.
// Returns a short-lived, read-only kubeconfig routed through Astronomer. This
// audited proxy path complements the separately supported hardened direct
// kubeconfig capability for clusters whose API endpoint is explicitly set.
func (h *ClusterHandler) GenerateKubeconfig(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	serverURL := agentServerURLFor(r.Context(), h.queries, r)
	userEmail := authenticatedEmail(r)
	token, expiresAt, err := h.mintKubeconfigToken(r, cluster)
	if err != nil {
		slog.Default().ErrorContext(r.Context(), "mint proxy kubeconfig credential", "cluster_id", cluster.ID.String(), "error", err)
		if errors.Is(err, audit.ErrOutboxUnavailable) {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable, "Mandatory audit storage is unavailable; the proxy credential was not issued")
		} else {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "A short-lived proxy credential could not be issued")
		}
		return
	}
	kubeconfig := buildProxyKubeconfig(cluster, userEmail, serverURL, token)
	yamlBytes, err := yaml.Marshal(kubeconfig)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.RenderError, "Failed to render kubeconfig")
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-proxy-kubeconfig.yaml"`, cluster.Name))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Astronomer-Kubeconfig-Mode", "proxy")
	w.Header().Set("X-Astronomer-Credential-Expires-At", expiresAt.Format(time.RFC3339))
	_, _ = w.Write(yamlBytes)
}

// PreviewKubeconfig handles GET /api/v1/clusters/{id}/kubeconfig-preview/.
// Returns the proxy kubeconfig as JSON for UI display.
func (h *ClusterHandler) PreviewKubeconfig(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	serverURL := agentServerURLFor(r.Context(), h.queries, r)
	userEmail := authenticatedEmail(r)
	// Preview is a UI display (JSON), not a download for kubectl — keep the
	// placeholder here rather than minting a live credential into a view that
	// may be rendered/logged. The download path (GenerateKubeconfig) mints.
	kubeconfig := buildProxyKubeconfig(cluster, userEmail, serverURL, "")
	RespondJSON(w, http.StatusOK, kubeconfig)
}

func buildProxyKubeconfig(cluster sqlc.Cluster, userEmail, serverURL, token string) map[string]any {
	if userEmail == "" {
		userEmail = "user"
	}
	// When we couldn't mint a token (unauthenticated caller or store without
	// token support) fall back to the placeholder so the YAML still renders and
	// the user can paste a token by hand — the old always-broken behaviour, now
	// only the degraded path.
	if token == "" {
		token = "REPLACE_WITH_API_TOKEN"
	}
	proxyURL := fmt.Sprintf("%s/api/v1/clusters/%s/k8s", serverURL, cluster.ID.String())
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Config",
		"clusters": []map[string]any{
			{
				"cluster": map[string]any{
					"server":                   proxyURL,
					"insecure-skip-tls-verify": false,
				},
				"name": cluster.Name,
			},
		},
		"contexts": []map[string]any{
			{
				"context": map[string]any{
					"cluster": cluster.Name,
					"user":    userEmail,
				},
				"name": cluster.Name + "-context",
			},
		},
		"current-context": cluster.Name + "-context",
		"users": []map[string]any{
			{
				"name": userEmail,
				"user": map[string]any{
					"token": token,
				},
			},
		},
	}
}

const kubeconfigTokenTTL = time.Hour

// mintKubeconfigToken creates a short-lived, caller-owned, read-only API token.
// The token row and sanitized audit intent share one commit decision, so an
// auditable credential is the only credential that can become usable.
func (h *ClusterHandler) mintKubeconfigToken(r *http.Request, cluster sqlc.Cluster) (string, time.Time, error) {
	if h == nil || h.runTx == nil {
		return "", time.Time{}, audit.ErrOutboxUnavailable
	}
	userID := currentUserUUID(r)
	if !userID.Valid {
		return "", time.Time{}, errors.New("authenticated user identity is unavailable")
	}
	plaintext, hash, prefix, err := generateAPIToken()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("generate credential: %w", err)
	}
	expiresAt := time.Now().UTC().Add(kubeconfigTokenTTL)
	err = h.runTx(r.Context(), func(q ClusterMutationTx) error {
		if _, createErr := q.CreateAPIToken(r.Context(), sqlc.CreateAPITokenParams{
			UserID:       uuid.UUID(userID.Bytes),
			Name:         fmt.Sprintf("kubeconfig-%s", cluster.Name),
			TokenHash:    hash,
			Prefix:       prefix,
			ExpiresAt:    pgtype.Timestamptz{Time: expiresAt, Valid: true},
			Scopes:       json.RawMessage(`["read"]`),
			AllowedCidrs: "",
		}); createErr != nil {
			return fmt.Errorf("persist credential: %w", createErr)
		}
		return recordAuditOutbox(r, q, "cluster.proxy_kubeconfig.issued", "cluster", cluster.ID.String(), cluster.Name, http.StatusOK, map[string]any{
			"mode": "proxy", "access": "read_only", "expires_at": expiresAt.Format(time.RFC3339),
		})
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return plaintext, expiresAt, nil
}

func authenticatedEmail(r *http.Request) string {
	if user, ok := reqctx.AuthenticatedUser(r.Context()); ok && user != nil {
		return user.Email
	}
	return ""
}
