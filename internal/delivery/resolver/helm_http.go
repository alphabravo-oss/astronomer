package resolver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	"helm.sh/helm/v3/pkg/repo"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

type helmHTTPResolver struct{}

func (helmHTTPResolver) Resolve(ctx context.Context, request Request, client *http.Client) (Result, error) {
	if strings.TrimSpace(request.Chart) == "" || len(request.Chart) > model.MaxNameLength || strings.ContainsAny(request.Chart, "\\\r\n\x00") {
		return Result{}, invalid("Helm chart name is invalid")
	}
	version, err := semver.StrictNewVersion(request.RequestedRevision)
	if err != nil {
		return Result{}, invalid("Helm chart revision must be an exact semantic version")
	}
	base, err := url.Parse(request.Source.URL)
	if err != nil {
		return Result{}, invalid("Helm repository URL is invalid")
	}
	indexURL := *base
	indexURL.Path = strings.TrimSuffix(indexURL.Path, "/") + "/index.yaml"
	indexBytes, err := fetchBounded(ctx, client, indexURL.String(), request, request.Limits.withDefaults().MaxIndexBytes)
	if err != nil {
		return Result{}, err
	}
	defer clearBytes(indexBytes)
	var index repo.IndexFile
	if err := yaml.UnmarshalStrict(indexBytes, &index); err != nil {
		return Result{}, &Error{Code: CodeInvalidRequest, Message: "Helm repository index is invalid"}
	}
	if index.APIVersion != repo.APIVersionV1 || len(index.Entries) == 0 {
		return Result{}, &Error{Code: CodeInvalidRequest, Message: "Helm repository index has no supported entries"}
	}
	entry, err := index.Get(request.Chart, version.String())
	if err != nil || entry == nil || entry.Metadata == nil || entry.Version != version.String() || len(entry.URLs) == 0 {
		return Result{}, &Error{Code: CodeNotFound, Message: "Helm chart version was not found"}
	}
	chartURL, err := repo.ResolveReferenceURL(request.Source.URL, entry.URLs[0])
	if err != nil {
		return Result{}, &Error{Code: CodeInvalidRequest, Message: "Helm chart URL is invalid"}
	}
	if err := validateFetchURL(ctx, request.NetworkPolicy, chartURL); err != nil {
		return Result{}, err
	}
	chartBytes, err := fetchBounded(ctx, client, chartURL, request, request.Limits.withDefaults().MaxHelmChartBytes)
	if err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(chartBytes)
	actual := model.Digest("sha256:" + hex.EncodeToString(sum[:]))
	expectedText := strings.ToLower(strings.TrimSpace(entry.Digest))
	if !strings.HasPrefix(expectedText, "sha256:") {
		expectedText = "sha256:" + expectedText
	}
	expected, err := model.ParseDigest(expectedText)
	if err != nil || expected != actual {
		clearBytes(chartBytes)
		return Result{}, &Error{Code: CodeDigestMismatch, Message: "Helm chart digest does not match the signed repository index"}
	}
	result := Result{
		Revision:            model.ImmutableRevision{Kind: model.RevisionHelmChart, Value: version.String(), ArtifactDigest: actual},
		verificationPayload: chartBytes,
	}
	if !request.Source.Trust.AllowUnsigned {
		signatureURL := chartURL + ".sig"
		if err := validateFetchURL(ctx, request.NetworkPolicy, signatureURL); err != nil {
			clearBytes(chartBytes)
			return Result{}, err
		}
		result.verificationSignature, err = fetchBounded(ctx, client, signatureURL, request, 1<<20)
		if err != nil {
			clearBytes(chartBytes)
			return Result{}, &Error{Code: CodeVerification, Message: "Helm chart detached signature is unavailable"}
		}
		if request.Source.Trust.Provider == model.SignatureCosignKeyless {
			certificateURL := chartURL + ".pem"
			if err := validateFetchURL(ctx, request.NetworkPolicy, certificateURL); err != nil {
				clearBytes(chartBytes)
				clearBytes(result.verificationSignature)
				return Result{}, err
			}
			result.verificationCertificate, err = fetchBounded(ctx, client, certificateURL, request, 1<<20)
			if err != nil {
				clearBytes(chartBytes)
				clearBytes(result.verificationSignature)
				return Result{}, &Error{Code: CodeVerification, Message: "Helm chart signing certificate is unavailable"}
			}
		}
	}
	return result, nil
}

func fetchBounded(ctx context.Context, client *http.Client, rawURL string, request Request, maximum int64) ([]byte, error) {
	if maximum <= 0 {
		return nil, invalid("source fetch limit is invalid")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, invalid("source fetch URL is invalid")
	}
	httpRequest.Header.Set("Accept", "application/octet-stream, application/x-yaml, text/yaml;q=0.9")
	httpRequest.Header.Set("User-Agent", "astronomer-delivery-resolver/1.0")
	if err := applyHTTPAuth(httpRequest, request.Source.AuthMode, request.Credential); err != nil {
		return nil, err
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return nil, sanitize(err, CodeUpstreamTemporary, true)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, &Error{Code: CodeAuthentication, Message: "source authentication failed"}
	}
	if response.StatusCode == http.StatusNotFound {
		return nil, &Error{Code: CodeNotFound, Message: "source artifact was not found"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retryable := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return nil, &Error{Code: CodeUpstreamTemporary, Message: "source returned an unsuccessful status", Retryable: retryable}
	}
	if response.ContentLength > maximum {
		return nil, &Error{Code: CodeLimitExceeded, Message: "source artifact exceeds the configured size limit"}
	}
	buffer := bytes.NewBuffer(make([]byte, 0, minInt64(maximum, 1<<20)))
	written, err := io.CopyN(buffer, response.Body, maximum+1)
	if err != nil && err != io.EOF {
		return nil, &Error{Code: CodeUpstreamTemporary, Message: "source response could not be read", Retryable: true}
	}
	if written > maximum {
		return nil, &Error{Code: CodeLimitExceeded, Message: "source artifact exceeds the configured size limit"}
	}
	return buffer.Bytes(), nil
}

func applyHTTPAuth(target *http.Request, mode model.AuthMode, credential *CredentialMaterial) error {
	switch mode {
	case model.AuthNone, model.AuthWorkloadIdentity:
		return nil
	case model.AuthBasic:
		if credential == nil || len(credential.Username) == 0 || len(credential.Password) == 0 {
			return &Error{Code: CodeAuthentication, Message: "source basic credential is unavailable"}
		}
		target.SetBasicAuth(string(credential.Username), string(credential.Password))
		return nil
	case model.AuthBearer:
		if credential == nil || len(credential.Token) == 0 {
			return &Error{Code: CodeAuthentication, Message: "source bearer credential is unavailable"}
		}
		target.Header.Set("Authorization", "Bearer "+string(credential.Token))
		return nil
	default:
		return invalid("source authentication mode is unsupported for HTTP")
	}
}

func minInt64(left, right int64) int {
	if left < right {
		return int(left)
	}
	return int(right)
}
