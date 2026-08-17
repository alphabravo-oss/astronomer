package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type sourceQueryFake struct {
	countFn   func(context.Context, sqlc.CountDeliverySourcesParams) (int64, error)
	listFn    func(context.Context, sqlc.ListDeliverySourcesParams) ([]sqlc.ListDeliverySourcesRow, error)
	createFn  func(context.Context, sqlc.CreateDeliverySourceParams) (sqlc.CreateDeliverySourceRow, error)
	getFn     func(context.Context, sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error)
	deleteFn  func(context.Context, sqlc.DeleteDeliverySourceParams) (int64, error)
	updateFn  func(context.Context, sqlc.UpdateDeliverySourceParams) (sqlc.UpdateDeliverySourceRow, error)
	rotateFn  func(context.Context, sqlc.RotateDeliverySourceCredentialParams) (sqlc.RotateDeliverySourceCredentialRow, error)
	resolveFn func(context.Context, sqlc.CreateDeliverySourceResolutionAndOutboxParams) (sqlc.CreateDeliverySourceResolutionAndOutboxRow, error)
}

func (f *sourceQueryFake) CountDeliverySources(ctx context.Context, arg sqlc.CountDeliverySourcesParams) (int64, error) {
	if f.countFn == nil {
		panic("unexpected CountDeliverySources")
	}
	return f.countFn(ctx, arg)
}

func (f *sourceQueryFake) ListDeliverySources(ctx context.Context, arg sqlc.ListDeliverySourcesParams) ([]sqlc.ListDeliverySourcesRow, error) {
	if f.listFn == nil {
		panic("unexpected ListDeliverySources")
	}
	return f.listFn(ctx, arg)
}

func (f *sourceQueryFake) CreateDeliverySource(ctx context.Context, arg sqlc.CreateDeliverySourceParams) (sqlc.CreateDeliverySourceRow, error) {
	if f.createFn == nil {
		panic("unexpected CreateDeliverySource")
	}
	return f.createFn(ctx, arg)
}

func (f *sourceQueryFake) GetDeliverySource(ctx context.Context, arg sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error) {
	if f.getFn == nil {
		panic("unexpected GetDeliverySource")
	}
	return f.getFn(ctx, arg)
}

func (f *sourceQueryFake) DeleteDeliverySource(ctx context.Context, arg sqlc.DeleteDeliverySourceParams) (int64, error) {
	if f.deleteFn == nil {
		panic("unexpected DeleteDeliverySource")
	}
	return f.deleteFn(ctx, arg)
}

func (f *sourceQueryFake) UpdateDeliverySource(ctx context.Context, arg sqlc.UpdateDeliverySourceParams) (sqlc.UpdateDeliverySourceRow, error) {
	if f.updateFn == nil {
		panic("unexpected UpdateDeliverySource")
	}
	return f.updateFn(ctx, arg)
}

func (f *sourceQueryFake) RotateDeliverySourceCredential(ctx context.Context, arg sqlc.RotateDeliverySourceCredentialParams) (sqlc.RotateDeliverySourceCredentialRow, error) {
	if f.rotateFn == nil {
		panic("unexpected RotateDeliverySourceCredential")
	}
	return f.rotateFn(ctx, arg)
}

func (f *sourceQueryFake) CreateDeliverySourceResolutionAndOutbox(ctx context.Context, arg sqlc.CreateDeliverySourceResolutionAndOutboxParams) (sqlc.CreateDeliverySourceResolutionAndOutboxRow, error) {
	if f.resolveFn == nil {
		panic("unexpected CreateDeliverySourceResolutionAndOutbox")
	}
	return f.resolveFn(ctx, arg)
}

func TestSourceCreateUsesFernetAndNeverEchoesCredential(t *testing.T) {
	projectID := uuid.New()
	sourceID := uuid.New()
	secret := "fixture-password-do-not-echo"
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	var persisted sqlc.CreateDeliverySourceParams
	fake := &sourceQueryFake{createFn: func(_ context.Context, arg sqlc.CreateDeliverySourceParams) (sqlc.CreateDeliverySourceRow, error) {
		persisted = arg
		return createSourceRow(sourceID, arg), nil
	}}
	handler := NewSourceHandler(fake, encryptor, 7)
	body := fmt.Sprintf(`{"project_id":%q,"name":"platform-source","type":"git","url":"https://git.example.test/platform/config.git","auth_mode":"basic","credential":{"username":"release-bot","password":%q},"trust_policy":{"allow_unsigned":true}}`, projectID, secret)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/delivery/sources", strings.NewReader(body))
	recorder := httptest.NewRecorder()

	handler.Create(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if persisted.ProjectID != projectID || persisted.CredentialKeyVersion != 7 || persisted.CredentialEpoch != 1 {
		t.Fatalf("unexpected persisted credential metadata: %#v", persisted)
	}
	if persisted.CredentialEncrypted == "" || strings.Contains(persisted.CredentialEncrypted, secret) {
		t.Fatalf("credential was not sealed: %q", persisted.CredentialEncrypted)
	}
	plaintext, err := encryptor.Decrypt(persisted.CredentialEncrypted)
	if err != nil {
		t.Fatalf("decrypt persisted credential: %v", err)
	}
	if !strings.Contains(plaintext, secret) || !strings.Contains(plaintext, `"username":"release-bot"`) {
		t.Fatalf("sealed payload does not contain typed Flux credential: %s", plaintext)
	}
	if strings.Contains(recorder.Body.String(), secret) || strings.Contains(recorder.Body.String(), persisted.CredentialEncrypted) {
		t.Fatalf("response exposed credential material: %s", recorder.Body.String())
	}
	for _, forbidden := range []string{"credential_encrypted", "ca_bundle", "password", "username"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("response exposed write-only field %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestSourceUpdateChangesMetadataWithoutReadingCredentials(t *testing.T) {
	projectID, sourceID := uuid.New(), uuid.New()
	now := time.Now().UTC()
	var updated sqlc.UpdateDeliverySourceParams
	fake := &sourceQueryFake{
		getFn: func(_ context.Context, arg sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error) {
			if arg.ID != sourceID || arg.ProjectID != projectID {
				t.Fatalf("unscoped get: %#v", arg)
			}
			return sqlc.GetDeliverySourceRow{
				ID: sourceID, ProjectID: projectID, Name: "platform-source", Description: "old",
				SourceType: "git", Url: "https://git.example.test/platform/config.git", AuthMode: "none",
				TrustPolicy: json.RawMessage(`{"allow_unsigned":true}`), Status: "ready", CreatedAt: now, UpdatedAt: now,
			}, nil
		},
		updateFn: func(_ context.Context, arg sqlc.UpdateDeliverySourceParams) (sqlc.UpdateDeliverySourceRow, error) {
			updated = arg
			return sqlc.UpdateDeliverySourceRow{
				ID: sourceID, ProjectID: projectID, Name: "platform-source", Description: arg.Description,
				SourceType: "git", Url: arg.Url, AuthMode: "none", TrustPolicy: arg.TrustPolicy,
				Status: "ready", CreatedAt: now, UpdatedAt: now,
			}, nil
		},
	}
	body := fmt.Sprintf(`{"project_id":%q,"description":"next","url":"https://git.example.test/platform/config.git"}`, projectID)
	request := requestWithPathParams(http.MethodPatch, "/api/v1/delivery/sources/"+sourceID.String(), strings.NewReader(body), map[string]string{"id": sourceID.String()})
	request.Header.Set("Idempotency-Key", "source-update-1")
	recorder := httptest.NewRecorder()
	NewSourceHandler(fake, nil, 0).Update(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if updated.Description != "next" || updated.ReplaceCaBundle || strings.Contains(recorder.Body.String(), "credential_encrypted") {
		t.Fatalf("update leaked or mutated secrets: %#v body=%s", updated, recorder.Body.String())
	}
}

func TestSourceVerifyQueuesOnlyPublicIdentity(t *testing.T) {
	projectID, sourceID, resolutionID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	var queued sqlc.CreateDeliverySourceResolutionAndOutboxParams
	fake := &sourceQueryFake{
		getFn: func(_ context.Context, arg sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error) {
			if arg.ID != sourceID || arg.ProjectID != projectID {
				t.Fatalf("unscoped source lookup: %#v", arg)
			}
			return sqlc.GetDeliverySourceRow{
				ID: sourceID, ProjectID: projectID, Name: "charts", SourceType: "helm_http",
				Url: "https://charts.example.test/stable", AuthMode: "basic",
				TrustPolicy: json.RawMessage(`{"allow_unsigned":true}`), Status: "ready", CreatedAt: now, UpdatedAt: now,
			}, nil
		},
		resolveFn: func(_ context.Context, arg sqlc.CreateDeliverySourceResolutionAndOutboxParams) (sqlc.CreateDeliverySourceResolutionAndOutboxRow, error) {
			queued = arg
			return sqlc.CreateDeliverySourceResolutionAndOutboxRow{ID: resolutionID, SourceID: sourceID, RequestedRevision: arg.RequestedRevision, ChartName: arg.ChartName, Status: "pending"}, nil
		},
	}
	body := fmt.Sprintf(`{"project_id":%q,"requested_revision":"1.2.3","chart":"widget"}`, projectID)
	request := requestWithPathParams(http.MethodPost, "/api/v1/delivery/sources/"+sourceID.String()+"/verify", strings.NewReader(body), map[string]string{"id": sourceID.String()})
	request.Header.Set("Idempotency-Key", "verify-widget-1.2.3")
	recorder := httptest.NewRecorder()
	NewSourceHandler(fake, nil, 0).Verify(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if queued.SourceID != sourceID || queued.BundleVersionID.Valid || queued.RequestedRevision != "1.2.3" || queued.ChartName != "widget" {
		t.Fatalf("unexpected queued identity: %#v", queued)
	}
	if strings.Contains(recorder.Body.String(), "credential") || strings.Contains(recorder.Body.String(), "charts.example") {
		t.Fatalf("verify response leaked source details: %s", recorder.Body.String())
	}
}

func TestSourceCreateEncryptionFailureIsFailClosed(t *testing.T) {
	projectID := uuid.New()
	secret := "fixture-token-do-not-echo"
	createCalled := false
	fake := &sourceQueryFake{createFn: func(context.Context, sqlc.CreateDeliverySourceParams) (sqlc.CreateDeliverySourceRow, error) {
		createCalled = true
		return sqlc.CreateDeliverySourceRow{}, nil
	}}
	handler := NewSourceHandler(fake, failingEncryptor{}, 1)
	body := fmt.Sprintf(`{"project_id":%q,"name":"registry-source","type":"oci_artifact","url":"oci://registry.example.test/platform/config","auth_mode":"bearer","credential":{"token":%q},"trust_policy":{"allow_unsigned":true}}`, projectID, secret)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/delivery/sources", strings.NewReader(body))
	recorder := httptest.NewRecorder()

	handler.Create(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if createCalled {
		t.Fatal("database write occurred after encryption failure")
	}
	if strings.Contains(recorder.Body.String(), secret) {
		t.Fatalf("encryption error echoed secret: %s", recorder.Body.String())
	}
}

func TestSourceCreateRejectsUnknownAndInvalidCredentialShapes(t *testing.T) {
	projectID := uuid.New()
	tests := []struct {
		name string
		body string
	}{
		{"unknown top-level field", fmt.Sprintf(`{"project_id":%q,"name":"source","type":"git","url":"https://git.example.test/repo.git","auth_mode":"none","trust_policy":{"allow_unsigned":true},"mystery":true}`, projectID)},
		{"unknown credential field", fmt.Sprintf(`{"project_id":%q,"name":"source","type":"git","url":"https://git.example.test/repo.git","auth_mode":"basic","credential":{"username":"u","password":"p","otp":"123"},"trust_policy":{"allow_unsigned":true}}`, projectID)},
		{"basic missing password", fmt.Sprintf(`{"project_id":%q,"name":"source","type":"git","url":"https://git.example.test/repo.git","auth_mode":"basic","credential":{"username":"u"},"trust_policy":{"allow_unsigned":true}}`, projectID)},
		{"secret with public source", fmt.Sprintf(`{"project_id":%q,"name":"source","type":"git","url":"https://git.example.test/repo.git","auth_mode":"none","credential":{"token":"secret"},"trust_policy":{"allow_unsigned":true}}`, projectID)},
		{"embedded URL password", fmt.Sprintf(`{"project_id":%q,"name":"source","type":"git","url":"https://user:secret@git.example.test/repo.git","auth_mode":"basic","credential":{"username":"u","password":"p"},"trust_policy":{"allow_unsigned":true}}`, projectID)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewSourceHandler(&sourceQueryFake{}, passthroughEncryptor{}, 1)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/delivery/sources", strings.NewReader(test.body))
			recorder := httptest.NewRecorder()
			handler.Create(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestSourceScopeMismatchAndCrossProjectLookup(t *testing.T) {
	projectA := uuid.New()
	projectB := uuid.New()
	sourceID := uuid.New()
	t.Run("body and query mismatch before persistence", func(t *testing.T) {
		body := fmt.Sprintf(`{"project_id":%q,"name":"source","type":"git","url":"https://git.example.test/repo.git","auth_mode":"none","trust_policy":{"allow_unsigned":true}}`, projectB)
		request := httptest.NewRequest(http.MethodPost, "/api/v1/delivery/sources?project_id="+projectA.String(), strings.NewReader(body))
		recorder := httptest.NewRecorder()
		NewSourceHandler(&sourceQueryFake{}, passthroughEncryptor{}, 1).Create(recorder, request)
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "project scopes do not match") {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("detail query is project scoped", func(t *testing.T) {
		fake := &sourceQueryFake{getFn: func(_ context.Context, arg sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error) {
			if arg.ProjectID != projectB || arg.ID != sourceID {
				t.Fatalf("unscoped query: %#v", arg)
			}
			return sqlc.GetDeliverySourceRow{}, pgx.ErrNoRows
		}}
		request := requestWithPathParams(http.MethodGet, "/api/v1/delivery/sources/"+sourceID.String()+"?project_id="+projectB.String(), nil, map[string]string{"id": sourceID.String()})
		recorder := httptest.NewRecorder()
		NewSourceHandler(fake, nil, 0).Get(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestSourceRotateIsWriteOnlyAndScopedBeforeEncryption(t *testing.T) {
	projectID := uuid.New()
	sourceID := uuid.New()
	secret := "rotated-password-do-not-echo"
	trust := json.RawMessage(`{"allow_unsigned":true}`)
	now := time.Now().UTC()
	var rotated sqlc.RotateDeliverySourceCredentialParams
	fake := &sourceQueryFake{
		getFn: func(_ context.Context, arg sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error) {
			return sqlc.GetDeliverySourceRow{ID: sourceID, ProjectID: projectID, Name: "source", SourceType: "git", Url: "https://git.example.test/repo.git", AuthMode: "basic", TrustPolicy: trust, Status: "ready", CreatedAt: now, UpdatedAt: now}, nil
		},
		rotateFn: func(_ context.Context, arg sqlc.RotateDeliverySourceCredentialParams) (sqlc.RotateDeliverySourceCredentialRow, error) {
			rotated = arg
			return sqlc.RotateDeliverySourceCredentialRow{ID: sourceID, ProjectID: projectID, Name: "source", SourceType: "git", Url: "https://git.example.test/repo.git", AuthMode: arg.AuthMode, CredentialKeyVersion: arg.CredentialKeyVersion, CredentialEpoch: 3, TrustPolicy: trust, Status: "pending", CreatedAt: now, UpdatedAt: now}, nil
		},
	}
	body := fmt.Sprintf(`{"project_id":%q,"auth_mode":"basic","credential":{"username":"release-bot","password":%q}}`, projectID, secret)
	request := requestWithPathParams(http.MethodPost, "/api/v1/delivery/sources/"+sourceID.String()+"/rotate-credential", strings.NewReader(body), map[string]string{"id": sourceID.String()})
	recorder := httptest.NewRecorder()
	NewSourceHandler(fake, passthroughEncryptor{}, 2).RotateCredential(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if rotated.ProjectID != projectID || rotated.ID != sourceID || rotated.CredentialEncrypted == "" {
		t.Fatalf("unexpected rotate query: %#v", rotated)
	}
	if strings.Contains(recorder.Body.String(), secret) || strings.Contains(recorder.Body.String(), rotated.CredentialEncrypted) {
		t.Fatalf("rotate response echoed secret: %s", recorder.Body.String())
	}
}

func TestSourceDeleteUsesProjectScopeAndReportsMissing(t *testing.T) {
	projectID := uuid.New()
	sourceID := uuid.New()
	for _, test := range []struct {
		name       string
		rows       int64
		wantStatus int
	}{
		{"deleted", 1, http.StatusNoContent},
		{"cross-project or missing", 0, http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &sourceQueryFake{deleteFn: func(_ context.Context, arg sqlc.DeleteDeliverySourceParams) (int64, error) {
				if arg.ID != sourceID || arg.ProjectID != projectID {
					t.Fatalf("delete was not project scoped: %#v", arg)
				}
				return test.rows, nil
			}}
			target := "/api/v1/delivery/sources/" + sourceID.String()
			request := requestWithPathParams(http.MethodDelete, target, nil, map[string]string{"id": sourceID.String()})
			request.Header.Set("X-Project-ID", projectID.String())
			recorder := httptest.NewRecorder()
			NewSourceHandler(fake, nil, 0).Delete(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestSourceListPaginationValidationAndSecretFreeProjection(t *testing.T) {
	projectID := uuid.New()
	sourceID := uuid.New()
	now := time.Now().UTC()
	trust := json.RawMessage(`{"allow_unsigned":true}`)
	fake := &sourceQueryFake{
		listFn: func(_ context.Context, arg sqlc.ListDeliverySourcesParams) ([]sqlc.ListDeliverySourcesRow, error) {
			if arg.QueryLimit != 2 || arg.QueryOffset != 1 || arg.ProjectID != projectID {
				t.Fatalf("unexpected list args: %#v", arg)
			}
			return []sqlc.ListDeliverySourcesRow{{ID: sourceID, ProjectID: projectID, Name: "source", SourceType: "git", Url: "https://git.example.test/repo.git", AuthMode: "basic", CredentialKeyVersion: 4, CredentialEpoch: 9, TrustPolicy: trust, Status: "ready", CreatedAt: now, UpdatedAt: now}}, nil
		},
		countFn: func(context.Context, sqlc.CountDeliverySourcesParams) (int64, error) { return 4, nil },
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/delivery/sources?project_id="+projectID.String()+"&limit=2&offset=1", nil)
	recorder := httptest.NewRecorder()
	NewSourceHandler(fake, nil, 0).List(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, forbidden := range []string{"credential_encrypted", "ca_bundle_encrypted", "private_key", "password"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("list exposed %q: %s", forbidden, recorder.Body.String())
		}
	}
	if !strings.Contains(recorder.Body.String(), `"next":"/api/v1/delivery/sources?limit=2\u0026offset=3\u0026project_id=`) {
		t.Fatalf("pagination did not preserve project filter: %s", recorder.Body.String())
	}

	badRequest := httptest.NewRequest(http.MethodGet, "/api/v1/delivery/sources?project_id="+projectID.String()+"&limit=201", nil)
	badRecorder := httptest.NewRecorder()
	NewSourceHandler(&sourceQueryFake{}, nil, 0).List(badRecorder, badRequest)
	if badRecorder.Code != http.StatusBadRequest {
		t.Fatalf("oversized page status=%d body=%s", badRecorder.Code, badRecorder.Body.String())
	}
}

type failingEncryptor struct{}

func (failingEncryptor) Encrypt(string) (string, error) { return "", errors.New("sealed") }

type passthroughEncryptor struct{}

func (passthroughEncryptor) Encrypt(value string) (string, error) { return "sealed:" + value, nil }

func createSourceRow(id uuid.UUID, arg sqlc.CreateDeliverySourceParams) sqlc.CreateDeliverySourceRow {
	now := time.Now().UTC()
	return sqlc.CreateDeliverySourceRow{
		ID: id, ProjectID: arg.ProjectID, Name: arg.Name, Description: arg.Description,
		SourceType: arg.SourceType, Url: arg.Url, AuthMode: arg.AuthMode,
		CredentialKeyVersion: arg.CredentialKeyVersion, CredentialEpoch: arg.CredentialEpoch,
		ProxyRef: arg.ProxyRef, TrustPolicy: arg.TrustPolicy, Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
}

func requestWithPathParams(method, target string, body *strings.Reader, values map[string]string) *http.Request {
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, body)
	}
	routeContext := chi.NewRouteContext()
	for key, value := range values {
		routeContext.URLParams.Add(key, value)
	}
	return request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
}

func TestSourceRequestBodyLimit(t *testing.T) {
	projectID := uuid.New()
	padding := bytes.Repeat([]byte("a"), maxRequestBytes+1)
	body := fmt.Sprintf(`{"project_id":%q,"name":"source","description":%q,"type":"git","url":"https://git.example.test/repo.git","auth_mode":"none","trust_policy":{"allow_unsigned":true}}`, projectID, string(padding))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/delivery/sources", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	NewSourceHandler(&sourceQueryFake{}, nil, 0).Create(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "exceeds") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
