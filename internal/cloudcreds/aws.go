package cloudcreds

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

const (
	awsCallTimeout = 10 * time.Second
	awsRoleSession = 15 * time.Minute
	awsRefreshSkew = 5 * time.Minute
)

// AWSCredentialResolver is the sole materialization contract used by both the
// credential test endpoint and EKS. Implementations must never include secret
// values in returned errors.
type AWSCredentialResolver interface {
	ResolveAWS(context.Context, map[string]string) (aws.Credentials, error)
}

type AWSFailureKind string

const (
	AWSFailurePermission AWSFailureKind = "permission_denied"
	AWSFailureThrottled  AWSFailureKind = "throttled"
	AWSFailureInvalid    AWSFailureKind = "invalid_credentials"
	AWSFailureTransient  AWSFailureKind = "transient"
)

// AWSCredentialError is safe to return through an API: it deliberately omits
// request bodies, credential identifiers, role ARNs and upstream messages.
type AWSCredentialError struct {
	Kind       AWSFailureKind
	Operation  string
	StatusCode int
	Code       string
}

func (e *AWSCredentialError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("AWS %s failed (%s, status %d, code %s)", e.Operation, e.Kind, e.StatusCode, safeAWSCode(e.Code))
	}
	return fmt.Sprintf("AWS %s failed (%s)", e.Operation, e.Kind)
}

type cachedAWSCredentials struct {
	credentials aws.Credentials
	expires     time.Time
}

// AWSResolver exchanges optional role credentials through STS and caches them
// only until five minutes before expiry. The mutex intentionally covers the
// exchange, preventing a concurrent refresh stampede.
type AWSResolver struct {
	HTTPClient *http.Client
	Endpoint   string
	Now        func() time.Time

	mu    sync.Mutex
	cache map[string]cachedAWSCredentials
}

func NewAWSResolver() *AWSResolver {
	return &AWSResolver{HTTPClient: httpclient.SafeClient(awsCallTimeout), cache: map[string]cachedAWSCredentials{}}
}

func (r *AWSResolver) ResolveAWS(ctx context.Context, blob map[string]string) (aws.Credentials, error) {
	accessKey := strings.TrimSpace(blob["access_key_id"])
	secretKey := strings.TrimSpace(blob["secret_access_key"])
	if accessKey == "" || secretKey == "" {
		return aws.Credentials{}, &AWSCredentialError{Kind: AWSFailureInvalid, Operation: "materialize"}
	}
	base := aws.Credentials{AccessKeyID: accessKey, SecretAccessKey: secretKey, SessionToken: strings.TrimSpace(blob["session_token"]), Source: "astronomer-encrypted-cloud-credential"}
	roleARN := strings.TrimSpace(blob["assume_role_arn"])
	if roleARN == "" {
		return base, nil
	}
	region := strings.TrimSpace(blob["region"])
	if region == "" {
		region = "us-east-1"
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	keySum := sha256.Sum256([]byte(accessKey + "\x00" + secretKey + "\x00" + base.SessionToken + "\x00" + roleARN + "\x00" + region))
	key := hex.EncodeToString(keySum[:])
	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.cache[key]; ok && cached.expires.After(now.Add(awsRefreshSkew)) {
		return cached.credentials, nil
	}
	resolved, expires, err := r.assumeRole(ctx, base, roleARN, region, now)
	if err != nil {
		return aws.Credentials{}, err
	}
	r.cache[key] = cachedAWSCredentials{credentials: resolved, expires: expires}
	return resolved, nil
}

func (r *AWSResolver) assumeRole(ctx context.Context, base aws.Credentials, roleARN, region string, now time.Time) (aws.Credentials, time.Time, error) {
	endpoint := strings.TrimSpace(r.Endpoint)
	if endpoint == "" {
		endpoint = "https://sts.amazonaws.com/"
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" {
		return aws.Credentials{}, time.Time{}, &AWSCredentialError{Kind: AWSFailureInvalid, Operation: "assume_role"}
	}
	q := u.Query()
	q.Set("Action", "AssumeRole")
	q.Set("Version", "2011-06-15")
	q.Set("RoleArn", roleARN)
	q.Set("RoleSessionName", "astronomer-cloud-credential")
	q.Set("DurationSeconds", fmt.Sprint(int(awsRoleSession.Seconds())))
	u.RawQuery = q.Encode()
	callCtx, cancel := context.WithTimeout(ctx, awsCallTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return aws.Credentials{}, time.Time{}, &AWSCredentialError{Kind: AWSFailureInvalid, Operation: "assume_role"}
	}
	emptyHash := sha256.Sum256(nil)
	if err := awsv4.NewSigner().SignHTTP(callCtx, base, req, hex.EncodeToString(emptyHash[:]), "sts", region, now); err != nil {
		return aws.Credentials{}, time.Time{}, &AWSCredentialError{Kind: AWSFailureInvalid, Operation: "assume_role"}
	}
	client := r.HTTPClient
	if client == nil {
		client = httpclient.SafeClient(awsCallTimeout)
	}
	resp, err := client.Do(req)
	if err != nil {
		return aws.Credentials{}, time.Time{}, &AWSCredentialError{Kind: AWSFailureTransient, Operation: "assume_role"}
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	closeErr := resp.Body.Close()
	if readErr != nil || closeErr != nil {
		return aws.Credentials{}, time.Time{}, &AWSCredentialError{Kind: AWSFailureTransient, Operation: "assume_role", StatusCode: resp.StatusCode, Code: "ResponseReadFailed"}
	}
	if resp.StatusCode/100 != 2 {
		var envelope struct {
			Error struct {
				Code string `xml:"Code"`
			} `xml:"Error"`
		}
		_ = xml.Unmarshal(body, &envelope)
		return aws.Credentials{}, time.Time{}, &AWSCredentialError{Kind: classifyAWSFailure(resp.StatusCode, envelope.Error.Code), Operation: "assume_role", StatusCode: resp.StatusCode, Code: envelope.Error.Code}
	}
	var out struct {
		Result struct {
			Credentials struct {
				AccessKeyID     string    `xml:"AccessKeyId"`
				SecretAccessKey string    `xml:"SecretAccessKey"`
				SessionToken    string    `xml:"SessionToken"`
				Expiration      time.Time `xml:"Expiration"`
			} `xml:"Credentials"`
		} `xml:"AssumeRoleResult"`
	}
	if xml.Unmarshal(body, &out) != nil || out.Result.Credentials.AccessKeyID == "" || out.Result.Credentials.SecretAccessKey == "" || out.Result.Credentials.SessionToken == "" || !out.Result.Credentials.Expiration.After(now.Add(time.Minute)) {
		return aws.Credentials{}, time.Time{}, &AWSCredentialError{Kind: AWSFailureTransient, Operation: "assume_role", StatusCode: resp.StatusCode, Code: "InvalidResponse"}
	}
	return aws.Credentials{AccessKeyID: out.Result.Credentials.AccessKeyID, SecretAccessKey: out.Result.Credentials.SecretAccessKey, SessionToken: out.Result.Credentials.SessionToken, Source: "astronomer-sts-assume-role", CanExpire: true, Expires: out.Result.Credentials.Expiration}, out.Result.Credentials.Expiration, nil
}

func classifyAWSFailure(status int, code string) AWSFailureKind {
	lower := strings.ToLower(code)
	if status == http.StatusTooManyRequests || strings.Contains(lower, "throttl") || strings.Contains(lower, "limitexceeded") {
		return AWSFailureThrottled
	}
	if status == http.StatusUnauthorized || strings.Contains(lower, "invalidclienttoken") || strings.Contains(lower, "signature") || strings.Contains(lower, "expiredtoken") {
		return AWSFailureInvalid
	}
	if status == http.StatusForbidden || strings.Contains(lower, "accessdenied") || strings.Contains(lower, "unauthor") {
		return AWSFailurePermission
	}
	return AWSFailureTransient
}

func safeAWSCode(code string) string {
	if code == "" {
		return "unknown"
	}
	for _, r := range code {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return "unknown"
		}
	}
	return code
}
