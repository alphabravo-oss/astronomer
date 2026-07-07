package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// --- CR rendering tests ---------------------------------------------------

func TestRenderVeleroBackup_BasicShape(t *testing.T) {
	got := renderVeleroBackup(VeleroBackupRender{
		Name:               "backup-team-a",
		Namespace:          "velero",
		BackupStorageName:  "primary-s3",
		IncludedNamespaces: []string{"team-a", "team-b"},
		TTL:                "168h",
	})
	if got["apiVersion"] != "velero.io/v1" {
		t.Fatalf("apiVersion = %v, want velero.io/v1", got["apiVersion"])
	}
	if got["kind"] != "Backup" {
		t.Fatalf("kind = %v, want Backup", got["kind"])
	}
	spec, ok := got["spec"].(map[string]any)
	if !ok {
		t.Fatalf("spec is not a map: %T", got["spec"])
	}
	if spec["storageLocation"] != "primary-s3" {
		t.Fatalf("storageLocation = %v", spec["storageLocation"])
	}
	if spec["ttl"] != "168h" {
		t.Fatalf("ttl = %v", spec["ttl"])
	}
	included, _ := spec["includedNamespaces"].([]string)
	if len(included) != 2 || included[0] != "team-a" {
		t.Fatalf("includedNamespaces = %v", spec["includedNamespaces"])
	}
}

func TestRenderVeleroBackup_OmitsEmptyOptionals(t *testing.T) {
	got := renderVeleroBackup(VeleroBackupRender{
		Name:              "b",
		BackupStorageName: "x",
	})
	spec := got["spec"].(map[string]any)
	if _, ok := spec["includedNamespaces"]; ok {
		t.Fatalf("includedNamespaces should be omitted when empty")
	}
	if _, ok := spec["excludedNamespaces"]; ok {
		t.Fatalf("excludedNamespaces should be omitted when empty")
	}
	if _, ok := spec["ttl"]; ok {
		t.Fatalf("ttl should be omitted when empty")
	}
	meta := got["metadata"].(map[string]any)
	if meta["namespace"] != defaultVeleroNamespace {
		t.Fatalf("namespace fallback failed: %v", meta["namespace"])
	}
}

func TestRenderVeleroSchedule_NestedTemplate(t *testing.T) {
	got := renderVeleroSchedule(VeleroScheduleRender{
		Name:               "sched-daily",
		Namespace:          "velero",
		BackupStorageName:  "primary-s3",
		Cron:               "0 2 * * *",
		IncludedNamespaces: []string{"prod"},
		TTL:                "720h",
	})
	if got["kind"] != "Schedule" {
		t.Fatalf("kind = %v", got["kind"])
	}
	spec := got["spec"].(map[string]any)
	if spec["schedule"] != "0 2 * * *" {
		t.Fatalf("schedule = %v", spec["schedule"])
	}
	template := spec["template"].(map[string]any)
	if template["storageLocation"] != "primary-s3" {
		t.Fatalf("template.storageLocation = %v", template["storageLocation"])
	}
	if template["ttl"] != "720h" {
		t.Fatalf("template.ttl = %v", template["ttl"])
	}
}

func TestRenderVeleroRestore_WithMapping(t *testing.T) {
	got := renderVeleroRestore(VeleroRestoreRender{
		Name:               "restore-1",
		Namespace:          "velero",
		BackupName:         "backup-team-a",
		IncludedNamespaces: []string{"team-a"},
		NamespaceMapping:   map[string]string{"team-a": "team-a-restored"},
	})
	spec := got["spec"].(map[string]any)
	if spec["backupName"] != "backup-team-a" {
		t.Fatalf("backupName = %v", spec["backupName"])
	}
	mapping := spec["namespaceMapping"].(map[string]string)
	if mapping["team-a"] != "team-a-restored" {
		t.Fatalf("namespaceMapping = %v", mapping)
	}
}

func TestRenderVeleroBSL_S3MinIO(t *testing.T) {
	got := renderVeleroBSL(VeleroBSLRender{
		Name:             "primary",
		Provider:         "aws",
		Bucket:           "backups",
		Prefix:           "prod",
		Region:           "us-east-1",
		S3URL:            "https://minio.example.com",
		S3ForcePathStyle: true,
		CredentialSecret: "primary-credentials",
		Default:          true,
	})
	spec := got["spec"].(map[string]any)
	if spec["provider"] != "aws" {
		t.Fatalf("provider = %v", spec["provider"])
	}
	if spec["default"] != true {
		t.Fatalf("default = %v", spec["default"])
	}
	cfg := spec["config"].(map[string]string)
	if cfg["s3Url"] != "https://minio.example.com" {
		t.Fatalf("s3Url = %v", cfg["s3Url"])
	}
	if cfg["s3ForcePathStyle"] != "true" {
		t.Fatalf("s3ForcePathStyle = %v", cfg["s3ForcePathStyle"])
	}
	cred := spec["credential"].(map[string]any)
	if cred["name"] != "primary-credentials" {
		t.Fatalf("credential.name = %v", cred["name"])
	}
	if cred["key"] != "cloud" {
		t.Fatalf("credential.key = %v (default should be 'cloud')", cred["key"])
	}
}

func TestVeleroProviderForStorageType(t *testing.T) {
	cases := map[string]string{
		"":          "aws",
		"s3":        "aws",
		"S3":        "aws",
		"minio":     "aws",
		"gcs":       "gcp",
		"GCP":       "gcp",
		"azure":     "azure",
		"azureblob": "azure",
		"unknown":   "unknown",
	}
	for in, want := range cases {
		if got := veleroProviderForStorageType(in); got != want {
			t.Errorf("veleroProviderForStorageType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVeleroResourceName_DNSCompliant(t *testing.T) {
	cases := []struct {
		kind, label, want string
	}{
		{"backup", "Team A backup!!", "backup-team-a-backup"},
		{"restore", "ALL_CAPS", "restore-all-caps"},
		{"schedule", "weekly:prod/team-1", "schedule-weekly-prod-team-1"},
		{"backup", "", "backup-x"},
	}
	for _, c := range cases {
		got := veleroResourceName(c.kind, c.label)
		if got != c.want {
			t.Errorf("veleroResourceName(%q,%q) = %q, want %q", c.kind, c.label, got, c.want)
		}
		if len(got) > 63 {
			t.Errorf("veleroResourceName(%q,%q) too long: %d chars", c.kind, c.label, len(got))
		}
	}
}

// --- TestStorageConfig connectivity probe (no live network) --------------

func TestProbeS3Bucket_Success(t *testing.T) {
	defer httpclient.DisableGuardForTest()()
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		if r.URL.Query().Get("list-type") != "2" {
			t.Errorf("missing list-type=2 query: %s", r.URL.RawQuery)
		}
		if !strings.Contains(r.URL.Path, "/my-bucket/") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `<?xml version="1.0"?><ListBucketResult/>`)
	}))
	defer srv.Close()

	h := &BackupHandler{httpClient: srv.Client()}
	cfg := sqlc.BackupStorageConfig{
		Bucket:      "my-bucket",
		Region:      "us-east-1",
		EndpointUrl: srv.URL,
	}
	if err := h.probeS3Bucket(context.Background(), cfg, "AKIAEXAMPLE", "secretkeyhere"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !strings.HasPrefix(capturedAuth, "AWS4-HMAC-SHA256 ") {
		t.Fatalf("expected sigv4 Authorization header, got %q", capturedAuth)
	}
	if !strings.Contains(capturedAuth, "Credential=AKIAEXAMPLE/") {
		t.Fatalf("Credential field missing access key: %q", capturedAuth)
	}
}

func TestProbeS3Bucket_Forbidden(t *testing.T) {
	defer httpclient.DisableGuardForTest()()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<Error><Code>InvalidAccessKeyId</Code></Error>`)
	}))
	defer srv.Close()
	h := &BackupHandler{httpClient: srv.Client()}
	cfg := sqlc.BackupStorageConfig{
		Bucket:      "x",
		Region:      "us-east-1",
		EndpointUrl: srv.URL,
	}
	err := h.probeS3Bucket(context.Background(), cfg, "k", "s")
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("error did not mention forbidden: %v", err)
	}
}

func TestProbeS3Bucket_NoSuchBucket(t *testing.T) {
	defer httpclient.DisableGuardForTest()()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchBucket</Code></Error>`)
	}))
	defer srv.Close()
	h := &BackupHandler{httpClient: srv.Client()}
	cfg := sqlc.BackupStorageConfig{
		Bucket:      "missing",
		Region:      "us-east-1",
		EndpointUrl: srv.URL,
	}
	err := h.probeS3Bucket(context.Background(), cfg, "k", "s")
	if err == nil || !strings.Contains(err.Error(), "bucket not found") {
		t.Fatalf("expected 'bucket not found' error, got %v", err)
	}
}

// TestProbeS3Bucket_RejectsPrivateEndpoint proves the SSRF guard blocks a
// loopback/internal endpoint URL before the signed GET is dialed.
func TestProbeS3Bucket_RejectsPrivateEndpoint(t *testing.T) {
	var reached bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := &BackupHandler{httpClient: srv.Client()}
	cfg := sqlc.BackupStorageConfig{
		Bucket:      "x",
		Region:      "us-east-1",
		EndpointUrl: srv.URL, // 127.0.0.1
	}
	err := h.probeS3Bucket(context.Background(), cfg, "k", "s")
	if err == nil {
		t.Fatal("expected SSRF guard to reject loopback endpoint")
	}
	if reached {
		t.Fatal("SSRF guard failed: request reached the loopback endpoint")
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error leaks internal address: %v", err)
	}
}

// --- BSL apply round-trip via a stub K8sRequester -----------------------

// stubK8sRequester records every request handed to it and returns a fixed
// response. Provides minimum surface for the apply paths.
type stubK8sRequester struct {
	mu       sync.Mutex
	requests []stubReq
	respFn   func(req stubReq) (*protocol.K8sResponsePayload, error)
}

type stubReq struct {
	ClusterID string
	Method    string
	Path      string
	Body      []byte
	Headers   map[string]string
}

func (s *stubK8sRequester) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error) {
	s.mu.Lock()
	req := stubReq{
		ClusterID: clusterID,
		Method:    method,
		Path:      path,
		Body:      append([]byte(nil), body...),
		Headers:   headers,
	}
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	if s.respFn != nil {
		return s.respFn(req)
	}
	return &protocol.K8sResponsePayload{StatusCode: http.StatusOK}, nil
}

func (s *stubK8sRequester) snapshot() []stubReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]stubReq, len(s.requests))
	copy(out, s.requests)
	return out
}

func TestApplyVeleroBSL_RoundTrip(t *testing.T) {
	clusterUUID := uuid.New()
	cfg := sqlc.BackupStorageConfig{
		ID:              uuid.New(),
		Name:            "primary",
		StorageType:     "s3",
		Bucket:          "team-backups",
		Region:          "us-east-1",
		EndpointUrl:     "https://minio.example.com",
		ClusterID:       pgtype.UUID{Bytes: clusterUUID, Valid: true},
		VeleroNamespace: "velero",
		BslName:         "primary",
		IsDefault:       true,
	}

	// Force PATCH to return 404 so we exercise the POST path. If we returned
	// 200 on PATCH the apply would short-circuit and we'd never see the BSL
	// body in a POST payload.
	stub := &stubK8sRequester{
		respFn: func(req stubReq) (*protocol.K8sResponsePayload, error) {
			if req.Method == http.MethodPatch {
				return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound}, nil
			}
			return &protocol.K8sResponsePayload{StatusCode: http.StatusCreated}, nil
		},
	}
	h := &BackupHandler{requester: stub}

	if err := h.applyVeleroBSL(context.Background(), cfg, "ACCESS", "SECRET"); err != nil {
		t.Fatalf("applyVeleroBSL: %v", err)
	}
	reqs := stub.snapshot()
	if len(reqs) == 0 {
		t.Fatal("expected at least one request")
	}
	// We expect 4 requests: PATCH+POST for Secret, PATCH+POST for BSL.
	if len(reqs) != 4 {
		t.Fatalf("expected 4 requests, got %d: %+v", len(reqs), reqs)
	}
	// Find the BSL POST and inspect its body.
	var bslPost *stubReq
	for i, r := range reqs {
		if r.Method == http.MethodPost && strings.Contains(r.Path, "/backupstoragelocations") {
			bslPost = &reqs[i]
		}
	}
	if bslPost == nil {
		t.Fatalf("no POST to backupstoragelocations: %+v", reqs)
	}
	var doc map[string]any
	if err := json.Unmarshal(bslPost.Body, &doc); err != nil {
		t.Fatalf("unmarshal BSL body: %v", err)
	}
	if doc["kind"] != "BackupStorageLocation" {
		t.Errorf("kind = %v", doc["kind"])
	}
	spec := doc["spec"].(map[string]any)
	if spec["provider"] != "aws" {
		t.Errorf("provider = %v", spec["provider"])
	}
	cred := spec["credential"].(map[string]any)
	if cred["name"] != "primary-credentials" {
		t.Errorf("credential.name = %v", cred["name"])
	}
}

// --- TestStorageConfig (handler endpoint) end-to-end -----------------------

// fakeBackupQuerier implements the BackupQuerier interface for tests. We
// only need GetBackupStorageConfigByID for TestStorageConfig; everything
// else returns zero values.
type fakeBackupQuerier struct {
	cfg             sqlc.BackupStorageConfig
	createArg       sqlc.CreateBackupStorageConfigParams
	updateArg       sqlc.UpdateBackupStorageConfigParams
	restoreByKey    map[string]sqlc.RestoreOperation
	restoreCreates  []sqlc.CreateRestoreOperationParams
	idemRestoreArgs []sqlc.CreateRestoreOperationIdempotentParams
}

func (f *fakeBackupQuerier) GetBackupStorageConfigByID(ctx context.Context, id uuid.UUID) (sqlc.BackupStorageConfig, error) {
	if id != f.cfg.ID {
		return sqlc.BackupStorageConfig{}, errors.New("not found")
	}
	return f.cfg, nil
}

// All other BackupQuerier methods are stubbed since the test only exercises
// the storage-config Test endpoint. They return zero values / errors.

func (f *fakeBackupQuerier) ListBackupStorageConfigs(ctx context.Context, arg sqlc.ListBackupStorageConfigsParams) ([]sqlc.BackupStorageConfig, error) {
	return nil, nil
}
func (f *fakeBackupQuerier) CreateBackupStorageConfig(ctx context.Context, arg sqlc.CreateBackupStorageConfigParams) (sqlc.BackupStorageConfig, error) {
	f.createArg = arg
	return sqlc.BackupStorageConfig{
		ID:                   uuid.New(),
		Name:                 arg.Name,
		StorageType:          arg.StorageType,
		Bucket:               arg.Bucket,
		Prefix:               arg.Prefix,
		Region:               arg.Region,
		EndpointUrl:          arg.EndpointUrl,
		AccessKey:            arg.AccessKey,
		SecretKey:            arg.SecretKey,
		IsDefault:            arg.IsDefault,
		CreatedByID:          arg.CreatedByID,
		ClusterID:            arg.ClusterID,
		VeleroNamespace:      arg.VeleroNamespace,
		BslName:              arg.BslName,
		EncryptedCredentials: arg.EncryptedCredentials,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}, nil
}
func (f *fakeBackupQuerier) UpdateBackupStorageConfig(ctx context.Context, arg sqlc.UpdateBackupStorageConfigParams) (sqlc.BackupStorageConfig, error) {
	f.updateArg = arg
	return sqlc.BackupStorageConfig{
		ID:                   arg.ID,
		Name:                 arg.Name,
		StorageType:          arg.StorageType,
		Bucket:               arg.Bucket,
		Prefix:               arg.Prefix,
		Region:               arg.Region,
		EndpointUrl:          arg.EndpointUrl,
		AccessKey:            arg.AccessKey,
		SecretKey:            arg.SecretKey,
		IsDefault:            arg.IsDefault,
		ClusterID:            arg.ClusterID,
		VeleroNamespace:      arg.VeleroNamespace,
		BslName:              arg.BslName,
		EncryptedCredentials: arg.EncryptedCredentials,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}, nil
}
func (f *fakeBackupQuerier) DeleteBackupStorageConfig(ctx context.Context, id uuid.UUID) error {
	return errors.New("not implemented")
}
func (f *fakeBackupQuerier) CountBackupStorageConfigs(ctx context.Context) (int64, error) {
	return 0, nil
}
func (f *fakeBackupQuerier) ListBackups(ctx context.Context, arg sqlc.ListBackupsParams) ([]sqlc.Backup, error) {
	return nil, nil
}
func (f *fakeBackupQuerier) ListRunningBackupsForPolling(ctx context.Context, limit int32) ([]sqlc.Backup, error) {
	return nil, nil
}
func (f *fakeBackupQuerier) GetBackupByID(ctx context.Context, id uuid.UUID) (sqlc.Backup, error) {
	return sqlc.Backup{}, errors.New("not implemented")
}
func (f *fakeBackupQuerier) CreateBackup(ctx context.Context, arg sqlc.CreateBackupParams) (sqlc.Backup, error) {
	return sqlc.Backup{}, errors.New("not implemented")
}
func (f *fakeBackupQuerier) UpdateBackupVeleroIdentity(ctx context.Context, arg sqlc.UpdateBackupVeleroIdentityParams) error {
	return nil
}
func (f *fakeBackupQuerier) UpdateBackupStarted(ctx context.Context, id uuid.UUID) error {
	return nil
}
func (f *fakeBackupQuerier) UpdateBackupCompleted(ctx context.Context, arg sqlc.UpdateBackupCompletedParams) error {
	return nil
}
func (f *fakeBackupQuerier) UpdateBackupFailed(ctx context.Context, arg sqlc.UpdateBackupFailedParams) error {
	return nil
}
func (f *fakeBackupQuerier) TouchBackupPolling(ctx context.Context, id uuid.UUID) error {
	return nil
}
func (f *fakeBackupQuerier) DeleteBackup(ctx context.Context, id uuid.UUID) error {
	return nil
}
func (f *fakeBackupQuerier) CountBackups(ctx context.Context) (int64, error) {
	return 0, nil
}
func (f *fakeBackupQuerier) ListBackupSchedules(ctx context.Context, arg sqlc.ListBackupSchedulesParams) ([]sqlc.BackupSchedule, error) {
	return nil, nil
}
func (f *fakeBackupQuerier) GetBackupScheduleByID(ctx context.Context, id uuid.UUID) (sqlc.BackupSchedule, error) {
	return sqlc.BackupSchedule{}, errors.New("not implemented")
}
func (f *fakeBackupQuerier) CreateBackupSchedule(ctx context.Context, arg sqlc.CreateBackupScheduleParams) (sqlc.BackupSchedule, error) {
	return sqlc.BackupSchedule{}, errors.New("not implemented")
}
func (f *fakeBackupQuerier) UpdateBackupSchedule(ctx context.Context, arg sqlc.UpdateBackupScheduleParams) (sqlc.BackupSchedule, error) {
	return sqlc.BackupSchedule{}, errors.New("not implemented")
}
func (f *fakeBackupQuerier) DeleteBackupSchedule(ctx context.Context, id uuid.UUID) error {
	return nil
}
func (f *fakeBackupQuerier) CountBackupSchedules(ctx context.Context) (int64, error) {
	return 0, nil
}
func (f *fakeBackupQuerier) ListRestoreOperations(ctx context.Context, arg sqlc.ListRestoreOperationsParams) ([]sqlc.RestoreOperation, error) {
	return nil, nil
}
func (f *fakeBackupQuerier) ListRunningRestoresForPolling(ctx context.Context, limit int32) ([]sqlc.RestoreOperation, error) {
	return nil, nil
}
func (f *fakeBackupQuerier) GetRestoreOperationByID(ctx context.Context, id uuid.UUID) (sqlc.RestoreOperation, error) {
	return sqlc.RestoreOperation{}, errors.New("not implemented")
}
func (f *fakeBackupQuerier) CreateRestoreOperation(ctx context.Context, arg sqlc.CreateRestoreOperationParams) (sqlc.RestoreOperation, error) {
	f.restoreCreates = append(f.restoreCreates, arg)
	return restoreOperationFromParams(arg), nil
}
func (f *fakeBackupQuerier) CreateRestoreOperationIdempotent(ctx context.Context, arg sqlc.CreateRestoreOperationIdempotentParams) (sqlc.RestoreOperation, error) {
	f.idemRestoreArgs = append(f.idemRestoreArgs, arg)
	if f.restoreByKey == nil {
		f.restoreByKey = map[string]sqlc.RestoreOperation{}
	}
	key := arg.Scope + "|" + arg.IdempotencyKey
	if restore, ok := f.restoreByKey[key]; ok {
		return restore, nil
	}
	restore := restoreOperationFromParams(sqlc.CreateRestoreOperationParams{
		BackupID:           arg.BackupID,
		Status:             arg.Status,
		InitiatedByID:      arg.InitiatedByID,
		ClusterID:          arg.ClusterID,
		VeleroNamespace:    arg.VeleroNamespace,
		VeleroRestoreName:  arg.VeleroRestoreName,
		IncludedNamespaces: arg.IncludedNamespaces,
		NamespaceMapping:   arg.NamespaceMapping,
	})
	f.restoreByKey[key] = restore
	return restore, nil
}
func (f *fakeBackupQuerier) UpdateRestoreOperationStarted(ctx context.Context, id uuid.UUID) error {
	return nil
}
func (f *fakeBackupQuerier) UpdateRestoreOperationCompleted(ctx context.Context, id uuid.UUID) error {
	return nil
}
func (f *fakeBackupQuerier) UpdateRestoreOperationFailed(ctx context.Context, arg sqlc.UpdateRestoreOperationFailedParams) error {
	return nil
}
func (f *fakeBackupQuerier) TouchRestorePolling(ctx context.Context, id uuid.UUID) error {
	return nil
}
func (f *fakeBackupQuerier) CountRestoreOperations(ctx context.Context) (int64, error) {
	return 0, nil
}

func restoreOperationFromParams(arg sqlc.CreateRestoreOperationParams) sqlc.RestoreOperation {
	now := time.Now()
	return sqlc.RestoreOperation{
		ID:                 uuid.New(),
		BackupID:           arg.BackupID,
		Status:             arg.Status,
		InitiatedByID:      arg.InitiatedByID,
		ClusterID:          arg.ClusterID,
		VeleroNamespace:    arg.VeleroNamespace,
		VeleroRestoreName:  arg.VeleroRestoreName,
		IncludedNamespaces: arg.IncludedNamespaces,
		NamespaceMapping:   arg.NamespaceMapping,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func TestCreateRestoreOperationUsesIdempotencyKey(t *testing.T) {
	q := &fakeBackupQuerier{}
	h := NewBackupHandler(q)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups/backup-1/restore/", nil)
	req.Header.Set("Idempotency-Key", "restore-retry-1")
	ctx := withOperationIdempotency(req, "restore")
	params := sqlc.CreateRestoreOperationParams{
		BackupID:          uuid.New(),
		Status:            "pending",
		VeleroNamespace:   "velero",
		VeleroRestoreName: "restore-demo",
	}

	first, err := h.createRestoreOperation(ctx, params)
	if err != nil {
		t.Fatalf("first restore: %v", err)
	}
	second, err := h.createRestoreOperation(ctx, params)
	if err != nil {
		t.Fatalf("second restore: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("restore id = %s then %s, want replay to return original", first.ID, second.ID)
	}
	if len(q.restoreCreates) != 0 {
		t.Fatalf("plain restore creates = %d, want idempotent path only", len(q.restoreCreates))
	}
	if len(q.idemRestoreArgs) != 2 {
		t.Fatalf("idempotent restore calls = %d, want 2", len(q.idemRestoreArgs))
	}
}

func TestCreateStorageConfigStoresEncryptedCredentialsOnly(t *testing.T) {
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	q := &fakeBackupQuerier{}
	h := NewBackupHandler(q)
	h.SetEncryptor(enc)

	body := `{"name":"primary","storage_type":"s3","bucket":"backups","access_key":"AKIA","secret_key":"SECRET","velero_namespace":"velero","bsl_name":"primary"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups/storage/", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.CreateStorageConfig(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if q.createArg.AccessKey != "" || q.createArg.SecretKey != "" {
		t.Fatalf("expected plaintext credential columns to be blank, got access=%q secret=%q", q.createArg.AccessKey, q.createArg.SecretKey)
	}
	if q.createArg.EncryptedCredentials == "" {
		t.Fatal("expected encrypted credentials to be stored")
	}
	plain, err := enc.Decrypt(q.createArg.EncryptedCredentials)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !strings.Contains(plain, `"access_key":"AKIA"`) || !strings.Contains(plain, `"secret_key":"SECRET"`) {
		t.Fatalf("encrypted credentials did not contain expected payload: %s", plain)
	}
}

func TestUpdateStorageConfigStoresEncryptedCredentialsOnly(t *testing.T) {
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	id := uuid.New()
	// UpdateStorageConfig now loads the row first (to authorize against its
	// cluster), so the fake must know about it.
	q := &fakeBackupQuerier{cfg: sqlc.BackupStorageConfig{ID: id, Name: "primary", Bucket: "backups"}}
	h := NewBackupHandler(q)
	h.SetEncryptor(enc)

	body := `{"name":"primary","storage_type":"s3","bucket":"backups","access_key":"AKIA","secret_key":"SECRET","velero_namespace":"velero","bsl_name":"primary"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/backups/storage/"+id.String()+"/", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.UpdateStorageConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if q.updateArg.AccessKey != "" || q.updateArg.SecretKey != "" {
		t.Fatalf("expected plaintext credential columns to be blank, got access=%q secret=%q", q.updateArg.AccessKey, q.updateArg.SecretKey)
	}
	if q.updateArg.EncryptedCredentials == "" {
		t.Fatal("expected encrypted credentials to be stored")
	}
}

func TestStorageConfigEndpoint_Success(t *testing.T) {
	defer httpclient.DisableGuardForTest()()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	id := uuid.New()
	q := &fakeBackupQuerier{
		cfg: sqlc.BackupStorageConfig{
			ID:          id,
			Bucket:      "x",
			Region:      "us-east-1",
			EndpointUrl: srv.URL,
			AccessKey:   "AK",
			SecretKey:   "SK",
		},
	}
	h := &BackupHandler{queries: q, httpClient: srv.Client()}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups/storage/"+id.String()+"/test/", nil)
	// chi URL params would normally be set by the router; mimic that.
	req = req.WithContext(setChiURLParam(req.Context(), "id", id.String()))
	h.TestStorageConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var envelope struct {
		Data struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !envelope.Data.Success {
		t.Fatalf("expected success=true, got %+v (raw=%s)", envelope.Data, rec.Body.String())
	}
}

// setChiURLParam injects a chi URL param into a request context for use in
// handler-level tests where the chi router isn't actually running.
func setChiURLParam(ctx context.Context, key, value string) context.Context {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return context.WithValue(ctx, chi.RouteCtxKey, rctx)
}

// --- AWS Sig-V4 header sanity check --------------------------------------

func TestSignAWSV4_StableSignature(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/foo/?list-type=2&max-keys=1", bytes.NewReader(nil))
	req.Host = "example.com"
	now := time.Date(2026, 5, 8, 12, 34, 56, 0, time.UTC)
	signAWSV4(req, "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1", "s3", now)
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20260508/us-east-1/s3/aws4_request, ") {
		t.Fatalf("Credential prefix wrong: %s", auth)
	}
	if !strings.Contains(auth, "SignedHeaders=host;x-amz-content-sha256;x-amz-date,") {
		t.Fatalf("SignedHeaders wrong: %s", auth)
	}
	if !strings.Contains(auth, "Signature=") {
		t.Fatalf("missing Signature: %s", auth)
	}
	if h := req.Header.Get("X-Amz-Date"); h != "20260508T123456Z" {
		t.Fatalf("X-Amz-Date = %q", h)
	}
}
