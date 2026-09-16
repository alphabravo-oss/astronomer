package handler

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// maxSignedManifestTTL bounds how far in the future an attacker-presented
// signed-manifest expiry may sit. The wizard mints 15m windows; anything
// claiming validity past this ceiling is rejected by the verifier even if
// the HMAC checks out, so a leaked signing key can't be used to forge
// effectively-permanent URLs.
const maxSignedManifestTTL = 30 * time.Minute

// SetRegistrationTokenTTL overrides the registration-token TTL (task A3).
// Non-positive values clamp to the 1h default so a missing/zero config never
// mints a zero-lifetime token.
func (h *ClusterHandler) SetRegistrationTokenTTL(d time.Duration) {
	if h == nil {
		return
	}
	if d <= 0 {
		d = time.Hour
	}
	h.registrationTokenTTL = d
}

// SetManifestSigningSecret wires the HMAC key for the signed
// manifest-download URL. Set once at startup; nil-safe. Empty secret
// leaves the signed-URL endpoint disabled (503).
func (h *ClusterHandler) SetManifestSigningSecret(secret string) {
	if h == nil {
		return
	}
	if secret == "" {
		h.manifestSigningSecret = nil
		return
	}
	// Domain-separate the manifest HMAC key from the raw secret. When the
	// wiring layer falls back to cfg.SecretKey (the JWT signing secret),
	// using it verbatim would make the manifest signer and the JWT signer
	// share an identical key; derive a distinct subkey so the two are not
	// the same bytes.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("manifest-signing"))
	h.manifestSigningSecret = mac.Sum(nil)
}

// manifestSignature computes the HMAC-SHA256 over "cluster_id|expiry"
// (expiry as unix seconds). Hex-encoded so it's URL-safe and constant
// across encodings.
func (h *ClusterHandler) manifestSignature(clusterID uuid.UUID, expiry int64) string {
	mac := hmac.New(sha256.New, h.manifestSigningSecret)
	mac.Write([]byte(clusterID.String() + "|" + strconv.FormatInt(expiry, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// SignManifestURL returns a relative, time-limited signed path the
// wizard can hand to operators:
//
//	/api/v1/register/signed/{cluster_id}?expires=<unix>&sig=<hmac>
//
// ttl bounds the validity window (caller passes 15m). Returns "" when
// no signing secret is configured.
func (h *ClusterHandler) SignManifestURL(clusterID uuid.UUID, ttl time.Duration) string {
	if h == nil || len(h.manifestSigningSecret) == 0 {
		return ""
	}
	expiry := time.Now().Add(ttl).Unix()
	sig := h.manifestSignature(clusterID, expiry)
	return fmt.Sprintf("/api/v1/register/signed/%s?expires=%d&sig=%s",
		clusterID.String(), expiry, url.QueryEscape(sig))
}

// verifyManifestSignature checks expiry and the constant-time HMAC.
// Returns nil when valid.
func (h *ClusterHandler) verifyManifestSignature(clusterID uuid.UUID, expiry int64, sig string) error {
	if len(h.manifestSigningSecret) == 0 {
		return errors.New("signing disabled")
	}
	now := time.Now()
	if now.Unix() > expiry {
		return errors.New("expired")
	}
	// Reject expiries further out than we'd ever legitimately mint, so a
	// forged or replayed URL can't claim a long-lived window.
	if expiry > now.Add(maxSignedManifestTTL).Unix() {
		return errors.New("expiry too far in future")
	}
	want := h.manifestSignature(clusterID, expiry)
	if subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
		return errors.New("bad signature")
	}
	return nil
}

func (h *ClusterHandler) SetAgentImage(repository, tag string) {
	if h == nil {
		return
	}
	if repository == "" {
		repository = "ghcr.io/alphabravo-oss/astronomer-go-agent"
	}
	if tag == "" {
		tag = "latest"
	}
	h.agentImage = targetAgentImage(repository, tag)
}

// SetDeliverySystemBootstrap configures the immutable, signed Flux system
// artifact embedded in new-cluster registration manifests. Invalid or partial
// values are omitted by the renderer; production startup validation rejects
// that configuration before the API is served.
func (h *ClusterHandler) SetDeliverySystemBootstrap(repository, digest, issuer, identity string) {
	if h == nil {
		return
	}
	h.systemArtifactURL = strings.TrimSpace(repository)
	h.systemArtifactDigest = strings.TrimSpace(digest)
	h.systemOIDCIssuer = strings.TrimSpace(issuer)
	h.systemOIDCIdentity = strings.TrimSpace(identity)
}

// SetRegistrationService wires the wizard-phase service so cluster
// Create can stamp the first two cluster_registration_steps rows
// (cluster_created + manifest_generated). nil-safe.
func (h *ClusterHandler) SetRegistrationService(s *registration.Service) {
	if h == nil {
		return
	}
	h.registration = s
}

// GenerateRegistrationToken handles POST /api/v1/clusters/{id}/register/.
func (h *ClusterHandler) GenerateRegistrationToken(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}

	// Verify cluster exists.
	if _, err := h.queries.GetClusterByID(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	// Generate a random registration token.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenError, "Failed to generate registration token")
		return
	}
	tokenStr := base64.URLEncoding.EncodeToString(b)

	params := sqlc.CreateClusterRegistrationTokenParams{
		ClusterID: id,
		TokenHash: auth.HashOpaqueToken(tokenStr),
		ExpiresAt: time.Now().Add(h.registrationTokenTTL),
	}
	token, err := executeMutation(r, h.runTx,
		func(q ClusterMutationTx) (sqlc.ClusterRegistrationToken, error) {
			return q.CreateClusterRegistrationToken(r.Context(), params)
		},
		func(token sqlc.ClusterRegistrationToken) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cluster.register_token", resourceType: "cluster", resourceID: id.String(),
				status: http.StatusCreated,
				detail: map[string]any{"token_id": token.ID.String(), "expires_at": token.ExpiresAt.UTC().Format(time.RFC3339)},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create registration token")
		return
	}
	token.Token = tokenStr

	RespondJSON(w, http.StatusCreated, token)
}

// RotateAgentToken handles POST /api/v1/clusters/{id}/agent-token/rotate/.
// It sets rotation_pending_at on the cluster's durable agent token so the
// agent's NEXT CONNECT performs the grace rotation (mint fresh, demote old to
// previous, deliver fresh in the ACK). It does NOT change the live token, so
// there is no mid-rotation lockout: the agent keeps using its held token until
// it adopts the freshly-minted one.
func (h *ClusterHandler) RotateAgentToken(w http.ResponseWriter, r *http.Request) {
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "cluster mutation transaction runner is not configured")
		return
	}
	r = r.WithContext(withOperationIdempotency(r, "agent_token_rotation"))
	digest, err := canonicalOperationRequestDigest(struct {
		ClusterID string `json:"cluster_id"`
	}{ClusterID: id.String()})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode token rotation request")
		return
	}
	receipt := AgentTokenRotationReceipt{ClusterID: id.String(), RotationPending: true, Message: "rotation will complete on the agent's next connect"}
	receipt, err = executeMutation(r, h.runTx, func(q ClusterMutationTx) (AgentTokenRotationReceipt, error) {
		idemQ, ok := q.(resourceOperationIdempotencyQuerier)
		if !ok {
			return AgentTokenRotationReceipt{}, errors.New("cluster token rotation idempotency store is not configured")
		}
		_, stored, replay, claimErr := claimOperationReceipt[AgentTokenRotationReceipt](r.Context(), idemQ, "cluster_agent_token_rotations", digest)
		if claimErr != nil {
			return AgentTokenRotationReceipt{}, claimErr
		}
		if replay {
			return stored, nil
		}
		rows, mutationErr := q.SetClusterAgentTokenRotationPending(r.Context(), id)
		if mutationErr != nil {
			return AgentTokenRotationReceipt{}, mutationErr
		}
		if rows == 0 {
			return AgentTokenRotationReceipt{}, errAgentTokenRotationIneligible
		}
		if auditErr := recordAuditOutbox(r, q, "agent.token.rotate.requested", "cluster", id.String(), cluster.Name, http.StatusAccepted, map[string]any{"cluster_id": id.String(), "trigger": "admin_api", "rows_affected": rows}); auditErr != nil {
			return AgentTokenRotationReceipt{}, auditErr
		}
		if attachErr := attachOperationReceipt(r.Context(), idemQ, "cluster_agent_token_rotations", id, digest, receipt); attachErr != nil {
			return AgentTokenRotationReceipt{}, attachErr
		}
		return receipt, nil
	}, func(AgentTokenRotationReceipt) mutationAuditEvent { return mutationAuditEvent{} })
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different token rotation")
			return
		}
		if errors.Is(err, errAgentTokenRotationIneligible) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "No agent token eligible for rotation: none is active or a rotation is already in flight")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to request agent token rotation")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/clusters/"+id.String()+"/", receipt)
}

type AgentTokenRotationReceipt struct {
	ClusterID       string `json:"cluster_id"`
	RotationPending bool   `json:"rotation_pending"`
	Message         string `json:"message"`
}

// RevokeAgentToken handles POST /api/v1/clusters/{id}/agent-token/revoke/.
// It hard-revokes the durable agent token (sets revoked_at, clears the grace
// previous_token_hash). After revoke, the agent's token fails validation, so
// its next CONNECT is denied (401/policy violation) and an operator must
// re-import the cluster to issue a fresh credential.
func (h *ClusterHandler) RevokeAgentToken(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	_, err = executeMutation(r, h.runTx,
		func(q ClusterMutationTx) (int64, error) {
			rows, mutationErr := q.RevokeClusterAgentToken(r.Context(), id)
			if mutationErr == nil && rows == 0 {
				mutationErr = errAgentTokenNotActive
			}
			return rows, mutationErr
		},
		func(rows int64) mutationAuditEvent {
			return mutationAuditEvent{
				action: "agent.token.revoked", resourceType: "cluster", resourceID: id.String(), resourceName: cluster.Name,
				status: http.StatusOK, detail: map[string]any{"cluster_id": id.String(), "trigger": "admin_api", "rows_affected": rows},
			}
		})
	if err != nil {
		if errors.Is(err, errAgentTokenNotActive) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster has no active agent token to revoke")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to revoke agent token")
		return
	}
	// Sever the live tunnel NOW so a compromised/rogue agent loses access
	// immediately rather than persisting on its already-authenticated session
	// until it happens to reconnect. The DB revoke above guarantees the
	// subsequent CONNECT is denied; this just collapses the window to ~0.
	disconnected := false
	if h.agentDisconnector != nil {
		disconnected = h.agentDisconnector.Disconnect(id.String())
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"cluster_id":      id.String(),
		"revoked":         true,
		"session_severed": disconnected,
		"message":         "agent token revoked; re-import the cluster to issue a new credential",
	})
}

// GetManifest handles GET /api/v1/clusters/{id}/manifest/.
// Returns the agent install manifest as raw YAML for curl-based installation.
func (h *ClusterHandler) GetManifest(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	// Generate a fresh registration token. T6.078 — short TTL: the
	// manifest is consumed by a single `kubectl apply` shortly after
	// download, so a 1-hour window is plenty in normal operation. A
	// stale token left in scrollback poses a smaller blast radius
	// than the historical 24h. Operators who need a longer window
	// can keep regenerating from the wizard.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenError, "Failed to generate registration token")
		return
	}
	tokenStr := base64.URLEncoding.EncodeToString(b)
	params := sqlc.CreateClusterRegistrationTokenParams{
		ClusterID: id,
		TokenHash: auth.HashOpaqueToken(tokenStr),
		ExpiresAt: time.Now().Add(h.registrationTokenTTL),
	}
	token, err := executeMutation(r, h.runTx,
		func(q ClusterMutationTx) (sqlc.ClusterRegistrationToken, error) {
			return q.CreateClusterRegistrationToken(r.Context(), params)
		},
		func(token sqlc.ClusterRegistrationToken) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cluster.register_token", resourceType: "cluster", resourceID: id.String(), resourceName: cluster.Name,
				status: http.StatusOK,
				detail: map[string]any{
					"token_id": token.ID.String(), "source": "manifest_download",
					"expires_at": token.ExpiresAt.UTC().Format(time.RFC3339),
				},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create registration token")
		return
	}
	token.Token = tokenStr

	manifest, err := h.renderAgentInstallManifest(cluster, tokenStr, agentServerURLFor(r.Context(), h.queries, r))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to render agent configuration")
		return
	}

	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="astronomer-agent-%s.yaml"`, cluster.Name))
	// Expose the freshly-minted registration token via header so the
	// wizard can render the Rancher-style one-liner without having
	// to grep it back out of the YAML body. Token is short-lived (1h
	// per T6.078) and the manifest body already contains it in
	// plaintext, so this isn't widening the secret's exposure surface.
	w.Header().Set("X-Astronomer-Registration-Token", tokenStr)
	w.Header().Set("Access-Control-Expose-Headers", "X-Astronomer-Registration-Token")
	_, _ = w.Write([]byte(manifest))
}

// GetManifestByToken handles GET /api/v1/register/{token}.yaml.
//
// Public (unauthenticated) endpoint that returns the agent install
// manifest for the cluster the token belongs to. The token IS the
// credential — same trust model as the manifest itself, which embeds
// the token in plaintext. This exists so operators can run the
// Rancher-style one-liner:
//
//	curl -sfL https://<server>/api/v1/register/<token>.yaml | kubectl apply --server-side --field-manager=astronomer-bootstrap -f -
//
// rather than copy-paste a multi-kilobyte heredoc.
func (h *ClusterHandler) GetManifestByToken(w http.ResponseWriter, r *http.Request) {
	rawToken := chi.URLParam(r, "token")
	tokenStr := strings.TrimSuffix(rawToken, ".yaml")
	if tokenStr == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidToken, "Missing registration token")
		return
	}
	token, err := h.queries.GetRegistrationTokenByToken(r.Context(), tokenStr)
	if err != nil {
		// GetRegistrationTokenByToken already filters expired rows
		// (WHERE expires_at > now()), so any error here is "no such
		// token". 404 keeps the response opaque.
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Registration token not found or expired")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), token.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	manifest, err := h.renderAgentInstallManifest(cluster, tokenStr, agentServerURLFor(r.Context(), h.queries, r))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to render agent configuration")
		return
	}

	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(manifest))
}

// GetSignedManifest handles GET /api/v1/register/signed/{cluster_id}.
//
// Public (unauthenticated) endpoint guarded by a short-TTL HMAC
// signature over (cluster_id, expiry) instead of a registration token.
// The wizard mints the URL via SignManifestURL with a 15-minute window;
// a tampered or expired URL is rejected (404, opaque) before any DB
// work. On success it mints a fresh registration token and returns the
// install manifest, identically to GetManifestByToken.
func (h *ClusterHandler) GetSignedManifest(w http.ResponseWriter, r *http.Request) {
	if len(h.manifestSigningSecret) == 0 {
		http.Error(w, "signed manifest URLs not enabled", http.StatusServiceUnavailable)
		return
	}
	id, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	expiry, err := strconv.ParseInt(r.URL.Query().Get("expires"), 10, 64)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidToken, "Invalid or missing expiry")
		return
	}
	if err := h.verifyManifestSignature(id, expiry, r.URL.Query().Get("sig")); err != nil {
		// Opaque 404 — don't distinguish expired from tampered.
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Manifest link invalid or expired")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	// Mint a fresh, short-lived registration token for this download.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenError, "Failed to generate registration token")
		return
	}
	tokenStr := base64.URLEncoding.EncodeToString(b)
	// Cap the minted token's lifetime to the remaining signature window
	// rather than a flat hour, so a replayed signed URL can't mint a
	// token that outlives the URL that authorized it.
	tokenExpiry := time.Unix(expiry, 0)
	if max := time.Now().Add(h.registrationTokenTTL); tokenExpiry.After(max) {
		tokenExpiry = max
	}
	if _, err := h.queries.CreateClusterRegistrationToken(r.Context(), sqlc.CreateClusterRegistrationTokenParams{
		ClusterID: id,
		TokenHash: auth.HashOpaqueToken(tokenStr),
		ExpiresAt: tokenExpiry,
	}); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create registration token")
		return
	}

	manifest, err := h.renderAgentInstallManifest(cluster, tokenStr, agentServerURLFor(r.Context(), h.queries, r))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to render agent configuration")
		return
	}
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(manifest))
}

// GetCABundle handles GET /api/v1/register/ca.crt.
//
// Public (unauthenticated) endpoint that returns the operator-provided
// PEM bundle from platform_settings["registration.ca_bundle"], so the
// Rancher-style `curl --cacert /tmp/astronomer-ca.crt -sfL …` variant
// of the registration one-liner works end-to-end. Returns 404 when no
// bundle is configured (either because the platform runs on a public
// CA or because the operator hasn't pasted one yet) — the wizard
// guards the variant on `registration.tls_mode == "private_ca"` so a
// 404 from here is the consistent "nothing to download" signal.
func (h *ClusterHandler) GetCABundle(w http.ResponseWriter, r *http.Request) {
	pem := registrationCABundle(r.Context(), h.queries)
	if pem == "" {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "No CA bundle configured for cluster registration")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="astronomer-ca.crt"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(pem + "\n"))
}

// registrationCABundle returns the operator-provided CA PEM bundle from
// platform_settings[registration.ca_bundle], or "" when none is configured.
// This is the single source of truth for the tunnel CA pin and is shared by the
// HTTP GetCABundle endpoint and the agent install-manifest renderers.
func registrationCABundle(ctx context.Context, q registrationCAQuerier) string {
	if q == nil {
		return ""
	}
	row, err := q.GetPlatformSetting(ctx, "registration.ca_bundle")
	if err != nil || len(row.Value) == 0 {
		return ""
	}
	// platform_settings.value is JSONB carrying a JSON-encoded string; unwrap it.
	var pem string
	_ = json.Unmarshal(row.Value, &pem)
	return strings.TrimSpace(pem)
}

// registrationCAQuerier is the slice of the queries surface registrationCABundle
// needs, so the helper is callable from any caller holding GetPlatformSetting.
type registrationCAQuerier interface {
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
}

func (h *ClusterHandler) renderAgentInstallManifest(cluster sqlc.Cluster, token, serverURL string) (string, error) {
	annotations := clusterAnnotations(cluster.Annotations)
	agentImage := "ghcr.io/alphabravo-oss/astronomer-go-agent:latest"
	if h != nil && h.agentImage != "" {
		agentImage = h.agentImage
	}
	// Server-CA pin: populate the CA bundle + checksum from the operator-provided
	// registration.ca_bundle. Empty when no private CA is configured, in which
	// case the agent falls back to the OS trust store (no behavior change).
	caPEM := ""
	if h != nil {
		caPEM = registrationCABundle(context.Background(), h.queries)
	}
	overrides, err := persistedAgentOverrides(cluster.AgentOverrides)
	if err != nil {
		return "", err
	}
	return agenttemplate.RenderInstallYAML(agenttemplate.InstallTemplateData{
		ServerURL:            serverURL,
		ClusterID:            cluster.ID.String(),
		RegistrationToken:    token,
		CACert:               caPEM,
		CAChecksum:           agenttemplate.CAChecksumFromPEM(caPEM),
		AgentImage:           agentImage,
		PrivilegeProfile:     agenttemplate.NormalizePrivilegeProfile(annotations[agenttemplate.PrivilegeProfileAnnotation]),
		ServiceAccountName:   strings.TrimSpace(annotations[agenttemplate.AgentServiceAccountNameAnnotation]),
		PodLabels:            clusterAgentPodLabels(annotations),
		AgentOverrides:       overrides,
		SystemArtifactURL:    h.systemArtifactURL,
		SystemArtifactDigest: h.systemArtifactDigest,
		SystemOIDCIssuer:     h.systemOIDCIssuer,
		SystemOIDCIdentity:   h.systemOIDCIdentity,
	}), nil
}

func clusterAgentPrivilegeProfile(raw json.RawMessage) string {
	return agenttemplate.NormalizePrivilegeProfile(clusterAnnotations(raw)[agenttemplate.PrivilegeProfileAnnotation])
}

func clusterAnnotations(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return map[string]string{}
	}
	var annotations map[string]string
	if err := json.Unmarshal(raw, &annotations); err != nil {
		return map[string]string{}
	}
	return annotations
}

func clusterAgentPodLabels(annotations map[string]string) map[string]string {
	raw := strings.TrimSpace(annotations[agenttemplate.AgentPodLabelsAnnotation])
	if raw == "" {
		return nil
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		return nil
	}
	return labels
}

func agentServerURL(r *http.Request) string {
	scheme := "https"
	if !reqctx.RequestIsHTTPS(r) {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s", scheme, reqctx.RequestHost(r))
}

// agentServerURLFor prefers platform_configuration.server_url (the
// operator-set authoritative public URL — includes any non-default port)
// over the request-derived value. The nginx → traefik → server chain
// strips the :8080 from the inbound Host header so the request-derived
// URL drops the port and the agent can't connect back. The platform
// config row is seeded at bootstrap from the Helm value so it always
// carries the port.
func agentServerURLFor(ctx context.Context, q interface {
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)
}, r *http.Request) string {
	if q != nil {
		if cfg, err := q.GetPlatformConfig(ctx); err == nil {
			if u := strings.TrimSpace(cfg.ServerUrl); u != "" {
				return strings.TrimRight(u, "/")
			}
		}
	}
	return agentServerURL(r)
}
