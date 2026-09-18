package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// SetEncryptor wires the Fernet encryptor used for chart-repository
// credentials at rest (migration 145).
func (h *CatalogHandler) SetEncryptor(encryptor *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = encryptor
}

// decryptor returns h.encryptor as a catalog.Decryptor, or a genuinely nil
// interface when none is wired. Returning h.encryptor directly would hand back
// a non-nil interface holding a nil *auth.Encryptor, and the nil check in
// catalog.ResolveAuthConfig would pass straight into a nil-receiver Decrypt.
func (h *CatalogHandler) decryptor() catalog.Decryptor {
	if h == nil || h.encryptor == nil {
		return nil
	}
	return h.encryptor
}

// sealer mirrors decryptor for the write path.
func (h *CatalogHandler) sealer() catalog.Encryptor {
	if h == nil || h.encryptor == nil {
		return nil
	}
	return h.encryptor
}

// TestRepoConnection handles POST /api/v1/catalog/repositories/{id}/test-connection/.
// Probes the repository's index.yaml endpoint to verify reachability.
func (h *CatalogHandler) respondRepoConnectionResult(w http.ResponseWriter, r *http.Request, repo sqlc.HelmRepository, status int, success bool, message string, upstreamStatus int) {
	detail := map[string]any{"success": success, "repo_type": repo.RepoType}
	if upstreamStatus > 0 {
		detail["upstream_status"] = upstreamStatus
	}
	if err := recordMandatoryAudit(r, h.queries, "catalog.repo.test_connection", "helm_repository", repo.ID.String(), repo.Name, detail); err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
			"Mandatory audit storage is unavailable; the connection result was not returned")
		return
	}
	RespondJSON(w, status, map[string]any{"success": success, "message": message})
}

func (h *CatalogHandler) TestRepoConnection(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid repository ID")
		return
	}
	repo, err := h.queries.GetHelmRepositoryByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Repository not found")
		return
	}
	if isOCIRepoSpec(repo) {
		// For OCI we just hit the /v2/ ping endpoint, which all
		// distribution-spec registries implement and all return 200/401
		// (401 here still proves the host is a registry).
		host, _, err := splitOCIURL(repo.Url)
		if err != nil {
			h.respondRepoConnectionResult(w, r, repo, http.StatusBadGateway, false, "Stored OCI repository URL is invalid.", 0)
			return
		}
		pingURL := "https://" + host + "/v2/"
		// SSRF backstop: this handler fetches an operator-supplied URL and
		// echoes the upstream status/error back, so reject probes aimed at
		// loopback / RFC-1918 / link-local (incl. the 169.254.169.254 metadata
		// endpoint) before any request leaves the process.
		if err := httpclient.GuardPublicHost(pingURL); err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidURL, "repository host is not permitted")
			return
		}
		client := httpclient.SafeClient(10 * time.Second)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, pingURL, nil)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidURL, err.Error())
			return
		}
		// Test-connection is the one place an operator is explicitly asking
		// "does this credential work", so an unreadable credential is reported
		// as such instead of being downgraded to an anonymous probe that
		// answers "reachable" and teaches them nothing.
		cfg, err := h.resolveOCIAuthConfig(repo)
		if err != nil {
			h.log.Error("test connection: chart repository credential could not be decrypted",
				"repository", repo.Name, "error", err)
			h.respondRepoConnectionResult(w, r, repo, http.StatusOK, false, "Stored credentials could not be decrypted; check the platform encryption key.", 0)
			return
		}
		if cfg.Username != "" || cfg.Password != "" {
			req.SetBasicAuth(cfg.Username, cfg.Password)
		}
		resp, err := client.Do(req)
		if err != nil {
			h.respondRepoConnectionResult(w, r, repo, http.StatusBadGateway, false, "Repository connection failed.", 0)
			return
		}
		defer func() {
			_ = resp.Body.Close()
		}()
		if resp.StatusCode >= 200 && resp.StatusCode < 500 {
			h.respondRepoConnectionResult(w, r, repo, http.StatusOK, true, fmt.Sprintf("OCI registry reachable (status %d).", resp.StatusCode), resp.StatusCode)
			return
		}
		h.respondRepoConnectionResult(w, r, repo, http.StatusBadGateway, false, fmt.Sprintf("Registry returned status %d.", resp.StatusCode), resp.StatusCode)
		return
	}
	url := strings.TrimRight(repo.Url, "/") + "/index.yaml"
	// SSRF backstop (see the OCI branch above): block probes at non-public
	// hosts before dialing.
	if err := httpclient.GuardPublicHost(url); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidURL, "repository host is not permitted")
		return
	}
	client := httpclient.SafeClient(10 * time.Second)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidURL, err.Error())
		return
	}
	// Same rule as the OCI branch above: test-connection is the one endpoint
	// where the operator is explicitly asking "does this credential work", so
	// resolve it here rather than through applyRepoIndexAuth, which logs a
	// decrypt failure and continues unauthenticated. Doing that here would
	// report the upstream's 401 as the answer, and the operator would conclude
	// the password is wrong when the real fault is the platform encryption key
	// — verbatim the misdiagnosis catalog.ErrAuthConfigUnavailable exists to
	// prevent.
	authCfg, err := catalog.ResolveIndexAuthConfig(repo, h.decryptor())
	if err != nil {
		h.log.Error("test connection: chart repository credential could not be decrypted",
			"repository", repo.Name, "error", err)
		h.respondRepoConnectionResult(w, r, repo, http.StatusOK, false, "Stored credentials could not be decrypted; check the platform encryption key.", 0)
		return
	}
	catalog.SetIndexAuthHeader(req, repo.AuthType, authCfg)
	resp, err := client.Do(req)
	if err != nil {
		h.respondRepoConnectionResult(w, r, repo, http.StatusBadGateway, false, "Repository connection failed.", 0)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= http.StatusBadRequest {
		h.respondRepoConnectionResult(w, r, repo, http.StatusBadGateway, false, fmt.Sprintf("Repository returned status %d.", resp.StatusCode), resp.StatusCode)
		return
	}
	h.respondRepoConnectionResult(w, r, repo, http.StatusOK, true, "Connection successful.", resp.StatusCode)
}

// redactHelmRepository strips secret fields from auth_config for API responses
// (SEC-01). Mirrors webhook SecretSentinel: clients that echo the sentinel on
// PUT leave the stored secret unchanged.
//
// GET/POST/PUT /api/v1/catalog/repositories/ serialise sqlc.HelmRepository
// wholesale, so this function is the ONLY thing standing between the stored
// credential and the wire. Since migration 145 that means two jobs:
//
//   - auth_config_encrypted is blanked unconditionally. It is ciphertext, it
//     is of no use to any client, and shipping it hands every catalog reader
//     an offline target for whoever later obtains the Fernet key.
//   - the sentinel is reconstructed from the DECRYPTED document, so the
//     response shape is unchanged from before 145: a client can still tell
//     that a password is configured, and can still echo the sentinel back on
//     PUT to leave it alone. When the credential cannot be decrypted the key
//     is simply absent — fail closed, never emit ciphertext as if it were the
//     secret.
func (h *CatalogHandler) redactHelmRepository(repo sqlc.HelmRepository) sqlc.HelmRepository {
	out := repo
	out.AuthConfigEncrypted = ""
	resolved, err := catalog.ResolveAuthConfig(repo, h.decryptor())
	if err != nil {
		h.log.Error("chart repository credential could not be decrypted for redaction",
			"repository", repo.Name, "error", err)
		out.AuthConfig = redactAuthConfigJSON(catalog.StripAuthConfigSecrets(repo.AuthConfig))
		return out
	}
	out.AuthConfig = redactAuthConfigJSON(resolved)
	return out
}

func (h *CatalogHandler) redactHelmRepositories(repos []sqlc.HelmRepository) []sqlc.HelmRepository {
	if len(repos) == 0 {
		return repos
	}
	out := make([]sqlc.HelmRepository, len(repos))
	for i := range repos {
		out[i] = h.redactHelmRepository(repos[i])
	}
	return out
}

func redactAuthConfigJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return json.RawMessage(`{}`)
	}
	// The one list, not a copy of it. redactHelmRepository now feeds the
	// DECRYPTED document in here, so a key that catalog.SealAuthConfig knows
	// is secret but this function did not would be stripped into the envelope
	// and then emitted in the clear in every list/get response.
	for _, k := range catalog.AuthConfigSecretKeys {
		if v, ok := m[k]; ok {
			if s, isStr := v.(string); isStr && s != "" {
				m[k] = SecretSentinel
			}
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
