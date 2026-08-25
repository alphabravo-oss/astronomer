// Package deferredreplay owns authenticated replay of maintenance-window
// mutations through the same HTTP pipeline that originally accepted them.
package deferredreplay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// Cipher decrypts a durable deferred-operation envelope.
type Cipher interface {
	DecryptBytes(string) ([]byte, error)
}

// Queries is the database lookup needed to revalidate Helm uninstall targets.
type Queries interface {
	GetInstalledChartByID(context.Context, uuid.UUID) (sqlc.InstalledChart, error)
}

// NewHTTPReplayers builds the complete production replay map. Each operation
// re-enters the authenticated API pipeline and is reauthorized as its original
// requesting principal at dispatch time.
func NewHTTPReplayers(router http.Handler, jwtManager *auth.JWTManager, cipher Cipher, queries Queries) (map[string]tasks.DeferredReplayer, error) {
	if router == nil || jwtManager == nil || cipher == nil || queries == nil {
		return nil, fmt.Errorf("deferred replay requires router, JWT, encryption, and database dependencies")
	}
	replay := func(ctx context.Context, row sqlc.DeferredOperation) error {
		return ReplayHTTPRequest(ctx, router, jwtManager, cipher, queries, row)
	}
	return map[string]tasks.DeferredReplayer{
		maintenance.OpClusterDelete:        replay,
		maintenance.OpProjectDelete:        replay,
		maintenance.OpToolInstall:          replay,
		maintenance.OpToolUpgrade:          replay,
		maintenance.OpToolUninstall:        replay,
		maintenance.OpHelmInstall:          replay,
		maintenance.OpHelmUninstall:        replay,
		maintenance.OpClusterTemplateApply: replay,
	}, nil
}

// ReplayHTTPRequest validates, authenticates, and dispatches one durable row.
func ReplayHTTPRequest(ctx context.Context, router http.Handler, jwtManager *auth.JWTManager, cipher Cipher, queries Queries, row sqlc.DeferredOperation) error {
	if !row.RequestedBy.Valid || row.RequestedBy.Bytes == uuid.Nil {
		return fmt.Errorf("deferred operation has no active requesting principal")
	}
	spec, clearSpec, err := decodeOperationSpec(row.OperationSpec, cipher)
	if clearSpec != nil {
		defer clear(clearSpec)
	}
	if err != nil {
		return err
	}
	if len(spec.Body) > 0 {
		defer clear(spec.Body)
	}
	if err := validateReplay(ctx, queries, row, spec); err != nil {
		return err
	}

	token, err := jwtManager.GenerateAccessTokenContext(ctx, uuid.UUID(row.RequestedBy.Bytes))
	if err != nil {
		return fmt.Errorf("mint deferred replay credential: %w", err)
	}
	replayURL := spec.Path
	if len(spec.QueryParams) > 0 {
		query := make(url.Values, len(spec.QueryParams))
		for key, value := range spec.QueryParams {
			query.Set(key, value)
		}
		replayURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, spec.Method, replayURL, bytes.NewReader(spec.Body))
	if err != nil {
		return fmt.Errorf("construct deferred replay request: %w", err)
	}
	req.Host = "astronomer.internal"
	req.RemoteAddr = "127.0.0.1:0"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "deferred-replay:"+row.ID.String())
	req.Header.Set("X-Correlation-ID", "deferred-replay:"+row.ID.String())

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code < http.StatusOK || recorder.Code >= http.StatusMultipleChoices {
		message := strings.TrimSpace(recorder.Body.String())
		if len(message) > 512 {
			message = message[:512]
		}
		return fmt.Errorf("deferred API replay returned HTTP %d: %s", recorder.Code, message)
	}
	return nil
}

func decodeOperationSpec(raw json.RawMessage, cipher Cipher) (handler.DeferredOpSpec, []byte, error) {
	if len(raw) == 0 || len(raw) > 2<<20 {
		return handler.DeferredOpSpec{}, nil, fmt.Errorf("deferred operation envelope has an invalid size")
	}
	var envelope handler.EncryptedDeferredOpSpec
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return handler.DeferredOpSpec{}, nil, fmt.Errorf("decode deferred operation envelope: %w", err)
	}
	plain := []byte(raw)
	if envelope.SchemaVersion != 0 || envelope.Ciphertext != "" {
		if envelope.SchemaVersion != 1 || strings.TrimSpace(envelope.Ciphertext) == "" {
			return handler.DeferredOpSpec{}, nil, fmt.Errorf("unsupported deferred operation envelope version %d", envelope.SchemaVersion)
		}
		var err error
		plain, err = cipher.DecryptBytes(envelope.Ciphertext)
		if err != nil {
			return handler.DeferredOpSpec{}, nil, fmt.Errorf("decrypt deferred operation envelope: %w", err)
		}
	}
	var spec handler.DeferredOpSpec
	if err := json.Unmarshal(plain, &spec); err != nil {
		if envelope.SchemaVersion == 1 {
			clear(plain)
		}
		return handler.DeferredOpSpec{}, nil, fmt.Errorf("decode deferred operation request: %w", err)
	}
	if envelope.SchemaVersion == 1 {
		return spec, plain, nil
	}
	return spec, nil, nil
}

func validateReplay(ctx context.Context, queries Queries, row sqlc.DeferredOperation, spec handler.DeferredOpSpec) error {
	path := strings.TrimSuffix(spec.Path, "/")
	clusterID := uuid.Nil
	if row.TargetClusterID.Valid {
		clusterID = uuid.UUID(row.TargetClusterID.Bytes)
	}
	projectID := uuid.Nil
	if row.TargetProjectID.Valid {
		projectID = uuid.UUID(row.TargetProjectID.Bytes)
	}
	if row.OperationType != maintenance.OpClusterDelete && len(spec.QueryParams) > 0 {
		return fmt.Errorf("deferred %s request contains unsupported query parameters", row.OperationType)
	}
	switch row.OperationType {
	case maintenance.OpClusterDelete:
		if spec.Method != http.MethodDelete || clusterID == uuid.Nil || path != "/api/v1/clusters/"+clusterID.String() {
			return fmt.Errorf("deferred cluster.delete request does not match its target")
		}
		for key := range spec.QueryParams {
			if key != "force" {
				return fmt.Errorf("deferred cluster.delete contains unsupported query parameter %q", key)
			}
		}
	case maintenance.OpProjectDelete:
		if spec.Method != http.MethodDelete || projectID == uuid.Nil || path != "/api/v1/projects/"+projectID.String() {
			return fmt.Errorf("deferred project.delete request does not match its target")
		}
	case maintenance.OpClusterTemplateApply:
		if spec.Method != http.MethodPost || clusterID == uuid.Nil || path != "/api/v1/clusters/"+clusterID.String()+"/template" {
			return fmt.Errorf("deferred cluster_template.apply request does not match its target")
		}
	case maintenance.OpToolInstall, maintenance.OpToolUpgrade, maintenance.OpToolUninstall:
		method, suffix := http.MethodPost, "/install"
		switch row.OperationType {
		case maintenance.OpToolUpgrade:
			method, suffix = http.MethodPut, "/upgrade"
		case maintenance.OpToolUninstall:
			method, suffix = http.MethodDelete, "/uninstall"
		}
		prefix := "/api/v1/tools/"
		slug := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
		decodedSlug, decodeErr := url.PathUnescape(slug)
		if spec.Method != method || !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) || slug == "" || decodeErr != nil || decodedSlug != slug || strings.ContainsAny(slug, "/\\") {
			return fmt.Errorf("deferred %s request path is invalid", row.OperationType)
		}
		if err := bodyTargetsCluster(spec.Body, clusterID); err != nil {
			return fmt.Errorf("deferred %s request: %w", row.OperationType, err)
		}
	case maintenance.OpHelmInstall:
		if spec.Method != http.MethodPost || path != "/api/v1/catalog/installed" {
			return fmt.Errorf("deferred helm.install request path is invalid")
		}
		if err := bodyTargetsCluster(spec.Body, clusterID); err != nil {
			return fmt.Errorf("deferred helm.install request: %w", err)
		}
	case maintenance.OpHelmUninstall:
		prefix := "/api/v1/catalog/installed/"
		id, err := uuid.Parse(strings.TrimPrefix(path, prefix))
		if spec.Method != http.MethodDelete || !strings.HasPrefix(path, prefix) || err != nil {
			return fmt.Errorf("deferred helm.uninstall request path is invalid")
		}
		installed, err := queries.GetInstalledChartByID(ctx, id)
		if err != nil || clusterID == uuid.Nil || installed.ClusterID != clusterID {
			return fmt.Errorf("deferred helm.uninstall target no longer matches the installation")
		}
	default:
		return fmt.Errorf("operation type %q is not replayable", row.OperationType)
	}
	return nil
}

func bodyTargetsCluster(body json.RawMessage, clusterID uuid.UUID) error {
	if clusterID == uuid.Nil || len(body) == 0 {
		return fmt.Errorf("target cluster or request body is missing")
	}
	var payload struct {
		ClusterID string `json:"cluster_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("request body is invalid: %w", err)
	}
	parsed, err := uuid.Parse(payload.ClusterID)
	if err != nil || parsed != clusterID {
		return fmt.Errorf("request cluster_id does not match its target")
	}
	return nil
}
