package resolver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"helm.sh/helm/v3/pkg/registry"
	"oras.land/oras-go/v2/registry/remote"
	remoteauth "oras.land/oras-go/v2/registry/remote/auth"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

var ociName = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._-]|/)[a-z0-9]+)*$`)

const (
	cosignSignatureAnnotation          = "dev.cosignproject.cosign/signature"
	cosignCertificateAnnotation        = "dev.sigstore.cosign/certificate"
	cosignBundleAnnotation             = "dev.sigstore.cosign/bundle"
	cosignSimpleSigningMediaType       = "application/vnd.dev.cosign.simplesigning.v1+json"
	maxCosignManifestBytes       int64 = 1 << 20
	maxCosignPayloadBytes        int64 = 1 << 20
	maxCosignAnnotationBytes           = 1 << 20
	maxCosignSignatures                = 32
)

type ociResolver struct {
	helmChart bool
}

func (r ociResolver) Resolve(ctx context.Context, request Request, client *http.Client) (Result, error) {
	base := strings.TrimSuffix(strings.TrimPrefix(request.Source.URL, "oci://"), "/")
	if base == "" || strings.ContainsAny(base, "@?#\r\n\x00") {
		return Result{}, invalid("OCI source URL is invalid")
	}
	if r.helmChart {
		chart := strings.Trim(request.Chart, "/")
		if !ociName.MatchString(chart) || strings.Contains(chart, "..") {
			return Result{}, invalid("OCI Helm chart name is invalid")
		}
		base += "/" + chart
		if _, err := semver.StrictNewVersion(request.RequestedRevision); err != nil {
			return Result{}, invalid("OCI Helm revision must be an exact semantic version")
		}
	}
	options := []registry.ClientOption{
		registry.ClientOptHTTPClient(client),
		registry.ClientOptWriter(io.Discard),
		registry.ClientOptEnableCache(false),
	}
	switch request.Source.AuthMode {
	case model.AuthNone, model.AuthWorkloadIdentity:
	case model.AuthBasic:
		if request.Credential == nil || len(request.Credential.Username) == 0 || len(request.Credential.Password) == 0 {
			return Result{}, &Error{Code: CodeAuthentication, Message: "OCI basic credential is unavailable"}
		}
		options = append(options, registry.ClientOptBasicAuth(string(request.Credential.Username), string(request.Credential.Password)))
	case model.AuthBearer:
		if request.Credential == nil || len(request.Credential.Token) == 0 {
			return Result{}, &Error{Code: CodeAuthentication, Message: "OCI bearer credential is unavailable"}
		}
		options = append(options, registry.ClientOptBasicAuth("oauth2accesstoken", string(request.Credential.Token)))
	default:
		return Result{}, invalid("OCI authentication mode is unsupported")
	}
	clientRegistry, err := registry.NewClient(options...)
	if err != nil {
		return Result{}, invalid("OCI client configuration is invalid")
	}
	requested := strings.TrimSpace(request.RequestedRevision)
	ref := base + ":" + requested
	if strings.HasPrefix(requested, "sha256:") {
		ref = base + "@" + requested
	}
	descriptor, err := clientRegistry.Resolve(ref)
	if err != nil {
		return Result{}, &Error{Code: CodeUpstreamTemporary, Message: "OCI reference resolution failed", Retryable: true}
	}
	digest, err := model.ParseDigest(descriptor.Digest.String())
	if err != nil {
		return Result{}, &Error{Code: CodeDigestMismatch, Message: "OCI registry returned an unsupported digest"}
	}
	maximum := request.Limits.withDefaults().MaxArtifactBytes
	if r.helmChart {
		maximum = request.Limits.withDefaults().MaxHelmChartBytes
	}
	if descriptor.Size < 0 || descriptor.Size > maximum {
		return Result{}, &Error{Code: CodeLimitExceeded, Message: "OCI manifest exceeds the configured size limit"}
	}
	revision := model.ImmutableRevision{Kind: model.RevisionOCIDigest, Value: digest.String(), ArtifactDigest: digest}
	if r.helmChart {
		revision.Kind = model.RevisionHelmChart
		revision.Value = requested
	}
	result := Result{Revision: revision}
	if !request.Source.Trust.AllowUnsigned {
		result.verificationEvidence, err = fetchCosignEvidence(ctx, base, digest.String(), request, client)
		if err != nil {
			return Result{}, err
		}
	}
	return result, nil
}

func fetchCosignEvidence(ctx context.Context, repositoryName, subjectDigest string, request Request, client *http.Client) ([]SignatureEvidence, error) {
	repository, err := remote.NewRepository(repositoryName)
	if err != nil {
		return nil, &Error{Code: CodeVerification, Message: "OCI signature repository is invalid"}
	}
	credential := remoteauth.EmptyCredential
	switch request.Source.AuthMode {
	case model.AuthNone, model.AuthWorkloadIdentity:
	case model.AuthBasic:
		if request.Credential == nil || len(request.Credential.Username) == 0 || len(request.Credential.Password) == 0 {
			return nil, &Error{Code: CodeAuthentication, Message: "OCI basic credential is unavailable"}
		}
		credential.Username = string(request.Credential.Username)
		credential.Password = string(request.Credential.Password)
	case model.AuthBearer:
		if request.Credential == nil || len(request.Credential.Token) == 0 {
			return nil, &Error{Code: CodeAuthentication, Message: "OCI bearer credential is unavailable"}
		}
		credential.AccessToken = string(request.Credential.Token)
	default:
		return nil, invalid("OCI authentication mode is unsupported")
	}
	repository.Client = &remoteauth.Client{
		Client:     client,
		Credential: remoteauth.StaticCredential(repository.Reference.Registry, credential),
	}
	repository.MaxMetadataBytes = maxCosignManifestBytes

	digestParts := strings.SplitN(subjectDigest, ":", 2)
	if len(digestParts) != 2 || digestParts[0] != "sha256" || len(digestParts[1]) != 64 {
		return nil, &Error{Code: CodeDigestMismatch, Message: "OCI signature subject digest is invalid"}
	}
	signatureTag := digestParts[0] + "-" + digestParts[1] + ".sig"
	manifestDescriptor, manifestReader, err := repository.FetchReference(ctx, signatureTag)
	if err != nil {
		return nil, &Error{Code: CodeVerification, Message: "OCI signature manifest is unavailable"}
	}
	manifestBytes, readErr := readBoundedAndClose(manifestReader, manifestDescriptor.Size, maxCosignManifestBytes)
	if readErr != nil {
		return nil, readErr
	}
	defer clearBytes(manifestBytes)
	if manifestDescriptor.Digest != digest.FromBytes(manifestBytes) {
		return nil, &Error{Code: CodeDigestMismatch, Message: "OCI signature manifest digest does not match"}
	}
	var manifest ocispec.Manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	if err := decoder.Decode(&manifest); err != nil || len(manifest.Layers) == 0 || len(manifest.Layers) > maxCosignSignatures {
		return nil, &Error{Code: CodeVerification, Message: "OCI signature manifest is invalid"}
	}

	evidence := make([]SignatureEvidence, 0, len(manifest.Layers))
	for _, layer := range manifest.Layers {
		if layer.MediaType != cosignSimpleSigningMediaType || layer.Size <= 0 || layer.Size > maxCosignPayloadBytes {
			continue
		}
		signatureText := strings.TrimSpace(layer.Annotations[cosignSignatureAnnotation])
		if signatureText == "" || len(signatureText) > maxCosignAnnotationBytes {
			continue
		}
		decodedSignature, decodeErr := base64.StdEncoding.DecodeString(signatureText)
		if decodeErr != nil || len(decodedSignature) == 0 || len(decodedSignature) > maxCosignAnnotationBytes {
			clearBytes(decodedSignature)
			continue
		}
		clearBytes(decodedSignature)
		certificate := []byte(layer.Annotations[cosignCertificateAnnotation])
		bundle := []byte(layer.Annotations[cosignBundleAnnotation])
		if len(certificate) > maxCosignAnnotationBytes || len(bundle) > maxCosignAnnotationBytes {
			clearBytes(certificate)
			clearBytes(bundle)
			continue
		}
		if request.Source.Trust.Provider == model.SignatureCosignKeyless && (len(certificate) == 0 || len(bundle) == 0) {
			clearBytes(certificate)
			clearBytes(bundle)
			continue
		}
		payloadReader, fetchErr := repository.Fetch(ctx, layer)
		if fetchErr != nil {
			clearBytes(certificate)
			clearBytes(bundle)
			continue
		}
		payload, payloadErr := readBoundedAndClose(payloadReader, layer.Size, maxCosignPayloadBytes)
		if payloadErr != nil || layer.Digest != digest.FromBytes(payload) || !simpleSigningClaimsDigest(payload, subjectDigest) {
			clearBytes(payload)
			clearBytes(certificate)
			clearBytes(bundle)
			continue
		}
		evidence = append(evidence, SignatureEvidence{
			Payload: payload, Signature: []byte(signatureText), Certificate: certificate, Bundle: bundle,
		})
	}
	if len(evidence) == 0 {
		return nil, &Error{Code: CodeVerification, Message: "OCI signature evidence is unavailable"}
	}
	return evidence, nil
}

func readBoundedAndClose(reader io.ReadCloser, declaredSize, maximum int64) ([]byte, error) {
	if reader == nil {
		return nil, &Error{Code: CodeVerification, Message: "OCI signature response is unavailable"}
	}
	defer func() { _ = reader.Close() }()
	if declaredSize <= 0 || declaredSize > maximum {
		return nil, &Error{Code: CodeLimitExceeded, Message: "OCI signature evidence exceeds the configured size limit"}
	}
	var buffer bytes.Buffer
	buffer.Grow(int(minInt64(declaredSize, 1<<20)))
	written, err := io.CopyN(&buffer, reader, maximum+1)
	if err != nil && err != io.EOF {
		return nil, &Error{Code: CodeUpstreamTemporary, Message: "OCI signature response could not be read", Retryable: true}
	}
	if written > maximum || written != declaredSize {
		clearBytes(buffer.Bytes())
		return nil, &Error{Code: CodeLimitExceeded, Message: "OCI signature evidence size is invalid"}
	}
	return buffer.Bytes(), nil
}

func simpleSigningClaimsDigest(payload []byte, expected string) bool {
	var claims struct {
		Critical struct {
			Type  string `json:"type"`
			Image struct {
				Digest string `json:"Docker-manifest-digest"`
			} `json:"image"`
		} `json:"critical"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return false
	}
	return claims.Critical.Type == "cosign container image signature" && claims.Critical.Image.Digest == expected
}
