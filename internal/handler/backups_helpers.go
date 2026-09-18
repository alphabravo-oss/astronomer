package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// optionalClusterID parses an optional UUID string into a pgtype.UUID. Empty
// input is permitted and returns an unset value.
func (h *BackupHandler) optionalClusterID(s string) (pgtype.UUID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return pgtype.UUID{}, nil
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}

// encryptCredentials encrypts an aws-style access/secret pair using the
// configured Fernet encryptor. Credential-bearing writes fail closed when the
// encryptor is unavailable so new plaintext secrets can never enter the legacy
// columns.
func (h *BackupHandler) encryptCredentials(access, secret string) (string, error) {
	if access == "" && secret == "" {
		return "", nil
	}
	if h == nil || h.encryptor == nil {
		return "", fmt.Errorf("backup credential encryption is not configured")
	}
	payload, err := json.Marshal(map[string]string{
		"access_key": access,
		"secret_key": secret,
	})
	if err != nil {
		return "", err
	}
	return h.encryptor.Encrypt(string(payload))
}

func legacyBackupCredentialColumns(_, _, _ string) (string, string) {
	return "", ""
}

// decryptCredentials returns the access/secret pair for a storage config,
// preferring the encrypted column when available and falling back to the
// legacy plaintext columns when no encryptor is configured.
func (h *BackupHandler) decryptCredentials(cfg sqlc.BackupStorageConfig) (string, string, error) {
	if h != nil && h.encryptor != nil && cfg.EncryptedCredentials != "" {
		plaintext, err := h.encryptor.Decrypt(cfg.EncryptedCredentials)
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
	return cfg.AccessKey, cfg.SecretKey, nil
}

// storageResponse builds the API representation of a storage config. The
// raw access/secret keys are *never* surfaced; we return only metadata.
func (h *BackupHandler) storageResponse(c sqlc.BackupStorageConfig) map[string]any {
	out := map[string]any{
		"id":               c.ID.String(),
		"name":             c.Name,
		"storage_type":     c.StorageType,
		"bucket":           c.Bucket,
		"prefix":           c.Prefix,
		"region":           c.Region,
		"endpoint_url":     c.EndpointUrl,
		"is_default":       c.IsDefault,
		"velero_namespace": c.VeleroNamespace,
		"bsl_name":         veleroBSLNameFor(c),
		"created_at":       c.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":       c.UpdatedAt.UTC().Format(time.RFC3339),
		"has_credentials":  c.EncryptedCredentials != "" || c.AccessKey != "",
	}
	if c.ClusterID.Valid {
		out["cluster_id"] = uuid.UUID(c.ClusterID.Bytes).String()
	}
	return out
}

// applyVeleroBSL ensures both the credentials Secret and the BSL CR exist on
// the target cluster. No-ops when the storage config has no cluster scope or
// no kubernetes requester is configured.
func (h *BackupHandler) applyVeleroBSL(ctx context.Context, cfg sqlc.BackupStorageConfig, accessKey, secretKey string) error {
	if h == nil || h.requester == nil {
		return nil
	}
	if !cfg.ClusterID.Valid {
		return nil
	}
	clusterID := uuid.UUID(cfg.ClusterID.Bytes).String()
	bslName := veleroBSLNameFor(cfg)
	secretName := veleroSecretNameFor(cfg)
	namespace := cfg.VeleroNamespace
	if namespace == "" {
		namespace = defaultVeleroNamespace
	}

	if accessKey != "" || secretKey != "" {
		secret := renderVeleroCredentialsSecret(secretName, namespace, accessKey, secretKey)
		secretBody, err := json.Marshal(secret)
		if err != nil {
			return err
		}
		createPath, patchPath := veleroSecretPath(namespace, secretName)
		if err := applyJSONBody(ctx, h.requester, clusterID, patchPath, createPath, secretBody); err != nil {
			return fmt.Errorf("apply credentials secret: %w", err)
		}
	}

	bsl := renderVeleroBSL(VeleroBSLRender{
		Name:             bslName,
		Namespace:        namespace,
		Provider:         veleroProviderForStorageType(cfg.StorageType),
		Bucket:           cfg.Bucket,
		Prefix:           cfg.Prefix,
		Region:           cfg.Region,
		S3URL:            cfg.EndpointUrl,
		S3ForcePathStyle: cfg.EndpointUrl != "",
		CredentialSecret: secretName,
		Default:          cfg.IsDefault,
	})
	body, err := json.Marshal(bsl)
	if err != nil {
		return err
	}
	createPath, patchPath := veleroCRDPath(namespace, "backupstoragelocations", bslName)
	return applyJSONBody(ctx, h.requester, clusterID, patchPath, createPath, body)
}

// applyVeleroSchedule projects a BackupSchedule row into a Velero Schedule CR.
func (h *BackupHandler) applyVeleroSchedule(ctx context.Context, sched sqlc.BackupSchedule, storage sqlc.BackupStorageConfig) error {
	if h == nil || h.requester == nil {
		return nil
	}
	if !sched.ClusterID.Valid && !storage.ClusterID.Valid {
		return nil
	}
	clusterPg := sched.ClusterID
	if !clusterPg.Valid {
		clusterPg = storage.ClusterID
	}
	clusterID := uuid.UUID(clusterPg.Bytes).String()
	namespace := sched.VeleroNamespace
	if namespace == "" {
		namespace = defaultVeleroNamespace
	}
	schedName := sched.VeleroScheduleName
	if schedName == "" {
		schedName = veleroResourceName("schedule", sched.Name)
	}
	bslName := veleroBSLNameFor(storage)
	body, err := json.Marshal(renderVeleroSchedule(VeleroScheduleRender{
		Name:               schedName,
		Namespace:          namespace,
		BackupStorageName:  bslName,
		Cron:               sched.CronExpression,
		IncludedNamespaces: veleroNamespacesFromJSON(sched.IncludedNamespaces),
		ExcludedNamespaces: veleroNamespacesFromJSON(sched.ExcludedNamespaces),
		TTL:                sched.Ttl,
		Labels: map[string]string{
			"astronomer.io/schedule-id": sched.ID.String(),
		},
	}))
	if err != nil {
		return err
	}
	createPath, patchPath := veleroCRDPath(namespace, "schedules", schedName)
	return applyJSONBody(ctx, h.requester, clusterID, patchPath, createPath, body)
}

// applyVeleroBackupForRow projects a Backup row into a Velero Backup CR. The
// CR's name comes from the row's velero_backup_name column so subsequent
// status polls can find it.
func (h *BackupHandler) applyVeleroBackupForRow(ctx context.Context, backup sqlc.Backup, storage sqlc.BackupStorageConfig) error {
	if h == nil || h.requester == nil {
		return nil
	}
	clusterPg := backup.ClusterID
	if !clusterPg.Valid {
		clusterPg = storage.ClusterID
	}
	if !clusterPg.Valid {
		return nil
	}
	clusterID := uuid.UUID(clusterPg.Bytes).String()
	namespace := backup.VeleroNamespace
	if namespace == "" {
		namespace = defaultVeleroNamespace
	}
	backupName := backup.VeleroBackupName
	if backupName == "" {
		backupName = veleroResourceName("backup", backup.Name)
	}
	bslName := veleroBSLNameFor(storage)
	body, err := json.Marshal(renderVeleroBackup(VeleroBackupRender{
		Name:               backupName,
		Namespace:          namespace,
		BackupStorageName:  bslName,
		IncludedNamespaces: veleroNamespacesFromJSON(backup.IncludedNamespaces),
		ExcludedNamespaces: veleroNamespacesFromJSON(backup.ExcludedNamespaces),
		Labels: map[string]string{
			"astronomer.io/backup-id": backup.ID.String(),
		},
	}))
	if err != nil {
		return err
	}
	createPath, patchPath := veleroCRDPath(namespace, "backups", backupName)
	if err := applyJSONBody(ctx, h.requester, clusterID, patchPath, createPath, body); err != nil {
		return err
	}
	return h.queries.UpdateBackupVeleroIdentity(ctx, sqlc.UpdateBackupVeleroIdentityParams{
		ID:               backup.ID,
		VeleroBackupName: backupName,
		VeleroNamespace:  namespace,
		ClusterID:        clusterPg,
	})
}

// veleroResourceName produces a DNS-1123-compliant CR name from a kind prefix
// and an arbitrary user-supplied label. We strip non-alphanumerics, lower-case
// and trim to 63 chars to satisfy Kubernetes naming rules.
func veleroResourceName(kind, label string) string {
	parts := []rune{}
	for _, r := range strings.ToLower(label) {
		switch {
		case r >= 'a' && r <= 'z':
			parts = append(parts, r)
		case r >= '0' && r <= '9':
			parts = append(parts, r)
		case r == '-' || r == '.':
			parts = append(parts, r)
		case r == ' ' || r == '_' || r == '/' || r == ':':
			parts = append(parts, '-')
		}
	}
	body := strings.Trim(string(parts), "-.")
	if body == "" {
		body = "x"
	}
	out := kind + "-" + body
	if len(out) > 63 {
		out = out[:63]
	}
	return strings.Trim(out, "-.")
}

// probeS3Bucket issues an authenticated AWS Sig-V4 GET against the bucket's
// list-objects-v2 endpoint with max-keys=1. This is the same probe that
// `velero install` uses to validate a BackupStorageLocation's reachability.
//
// 200 / 204 / 206  → ok.
// 403 with InvalidAccessKeyId / SignatureDoesNotMatch → wrong credentials.
// 404 with NoSuchBucket  → bucket missing.
// network error → wrong endpoint / firewall.
func (h *BackupHandler) probeS3Bucket(ctx context.Context, cfg sqlc.BackupStorageConfig, accessKey, secretKey string) error {
	endpoint := strings.TrimSpace(cfg.EndpointUrl)
	if endpoint == "" {
		// Default to the canonical AWS endpoint for the configured region.
		region := cfg.Region
		if region == "" {
			region = "us-east-1"
		}
		endpoint = fmt.Sprintf("https://s3.%s.amazonaws.com", region)
	}
	host, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint url: %w", err)
	}
	// Path-style addressing (endpoint/bucket/?list-type=2) is what Velero/MinIO
	// use; virtual-host-style works against canonical AWS but breaks against
	// MinIO so we always use path-style.
	host.Path = strings.TrimRight(host.Path, "/") + "/" + cfg.Bucket + "/"
	q := host.Query()
	q.Set("list-type", "2")
	q.Set("max-keys", "1")
	host.RawQuery = q.Encode()

	// SSRF guard: the endpoint URL is operator-supplied (BackupStorageConfig),
	// so refuse to dial a loopback/internal/metadata address. Do not echo the
	// endpoint in the error.
	if err := httpclient.GuardPublicHost(host.String()); err != nil {
		return fmt.Errorf("endpoint is not a permitted public address")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host.String(), nil)
	if err != nil {
		return err
	}

	region := cfg.Region
	if region == "" {
		region = "us-east-1"
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
	defer func() {
		_ = resp.Body.Close()
	}()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusPartialContent:
		return nil
	case http.StatusForbidden:
		return fmt.Errorf("forbidden (likely invalid credentials): %s", strings.TrimSpace(string(body)))
	case http.StatusNotFound:
		return fmt.Errorf("bucket not found: %s", cfg.Bucket)
	default:
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

// signAWSV4 attaches AWS Signature Version 4 headers to req. This is a minimal
// implementation sufficient for unauthenticated GETs against S3-compatible
// endpoints. It does NOT cover streaming uploads, presigned URLs, or chunked
// transfers — those are out of scope for a connectivity probe.
//
// Reference: https://docs.aws.amazon.com/general/latest/gr/sigv4_signing.html
func signAWSV4(req *http.Request, accessKey, secretKey, region, service string, now time.Time) {
	const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	timeStamp := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	req.Header.Set("X-Amz-Date", timeStamp)
	req.Header.Set("X-Amz-Content-Sha256", emptyPayloadHash)
	req.Header.Set("Host", req.Host)

	// Canonical query string: keys sorted, RFC 3986-encoded.
	values := req.URL.Query()
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	canonicalQuery := ""
	for i, k := range keys {
		if i > 0 {
			canonicalQuery += "&"
		}
		canonicalQuery += awsURIEscape(k) + "=" + awsURIEscape(values.Get(k))
	}

	// Canonical headers (host, x-amz-content-sha256, x-amz-date).
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n",
		req.Host, emptyPayloadHash, timeStamp)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"

	canonicalRequest := strings.Join([]string{
		req.Method,
		awsURIEscapePath(req.URL.Path),
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		emptyPayloadHash,
	}, "\n")

	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, service)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		timeStamp,
		credentialScope,
		hashSHA256(canonicalRequest),
	}, "\n")

	dateKey := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	regionKey := hmacSHA256(dateKey, region)
	serviceKey := hmacSHA256(regionKey, service)
	signingKey := hmacSHA256(serviceKey, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature,
	))
}

func hashSHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, value string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(value))
	return h.Sum(nil)
}

// awsURIEscape encodes a string for use in an AWS Sig-V4 canonical query
// string. AWS requires unreserved characters per RFC 3986 only; everything
// else is percent-encoded with upper-case hex.
func awsURIEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'),
			c == '-' || c == '_' || c == '.' || c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// awsURIEscapePath escapes a URL path segment-by-segment, leaving '/' literal.
func awsURIEscapePath(p string) string {
	if p == "" {
		return "/"
	}
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = awsURIEscape(part)
	}
	return strings.Join(parts, "/")
}
