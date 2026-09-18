package catalog

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ErrRepositoryCredentialEncryptionUnavailable prevents new secret material
// from being persisted in the legacy plaintext shape when encryption wiring is
// absent. Legacy rows remain readable through ResolveAuthConfig for migration.
var ErrRepositoryCredentialEncryptionUnavailable = errors.New("repository credential encryption is unavailable")

// ValidationError marks an operator-correctable repository input error. HTTP
// adapters can map it to a 400 without teaching domain code about transport.
type ValidationError struct{ err error }

func (e ValidationError) Error() string { return e.err.Error() }
func (e ValidationError) Unwrap() error { return e.err }

// IsValidationError reports whether err was caused by repository input rather
// than persistence or credential-envelope infrastructure.
func IsValidationError(err error) bool {
	var validation ValidationError
	return errors.As(err, &validation)
}

// RepositoryService owns repository write preparation: URL safety, repository
// kind and auth inference, secret-sentinel preservation, and credential
// envelope construction. It deliberately has no HTTP or transaction concerns.
type RepositoryService interface {
	PrepareCreate(RepositoryCreateInput, Encryptor) (sqlc.CreateHelmRepositoryParams, error)
	PrepareUpdate(sqlc.HelmRepository, RepositoryUpdateInput, Encryptor, Decryptor) (sqlc.UpdateHelmRepositoryParams, error)
}

type repositoryService struct{}

// NewRepositoryService returns the catalog-domain write service. It is
// stateless, so handlers and non-HTTP callers can share identical policy.
func NewRepositoryService() RepositoryService { return repositoryService{} }

// RepositoryCreateInput is transport-neutral operator intent for a new chart
// repository.
type RepositoryCreateInput struct {
	Name        string
	URL         string
	RepoType    string
	Description string
	IsDefault   bool
	AuthType    string
	AuthConfig  json.RawMessage
	Enabled     *bool
}

// RepositoryUpdateInput represents a partial repository update. Nil fields
// mean preserve the stored value.
type RepositoryUpdateInput struct {
	ID                  string
	Name                *string
	URL                 *string
	RepoType            *string
	Description         *string
	IsDefault           *bool
	AuthType            *string
	AuthConfig          *json.RawMessage
	Enabled             *bool
	RedactedSecretValue string
}

func (repositoryService) PrepareCreate(input RepositoryCreateInput, encryptor Encryptor) (sqlc.CreateHelmRepositoryParams, error) {
	cleanURL, err := ValidateRepositoryURL(input.URL)
	if err != nil {
		return sqlc.CreateHelmRepositoryParams{}, ValidationError{err: err}
	}
	authConfig := input.AuthConfig
	if authConfig == nil {
		authConfig = json.RawMessage(`{}`)
	}
	repoType := strings.TrimSpace(input.RepoType)
	if repoType == "" && IsOCIURL(cleanURL) {
		repoType = "oci"
	}
	if strings.EqualFold(repoType, "git") {
		repoType = "git"
	}
	authType := strings.TrimSpace(input.AuthType)
	if authType == "" {
		authType = InferAuthType(authConfig)
	}
	sealed, publicConfig, err := sealRepositoryAuthConfig(authConfig, encryptor)
	if err != nil {
		return sqlc.CreateHelmRepositoryParams{}, err
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	return sqlc.CreateHelmRepositoryParams{
		Name:                input.Name,
		Url:                 cleanURL,
		RepoType:            repoType,
		Description:         input.Description,
		IsDefault:           input.IsDefault,
		AuthType:            authType,
		AuthConfig:          publicConfig,
		AuthConfigEncrypted: sealed,
		Enabled:             enabled,
	}, nil
}

func (repositoryService) PrepareUpdate(existing sqlc.HelmRepository, input RepositoryUpdateInput, encryptor Encryptor, decryptor Decryptor) (sqlc.UpdateHelmRepositoryParams, error) {
	existingConfig, err := ResolveAuthConfig(existing, decryptor)
	if err != nil {
		return sqlc.UpdateHelmRepositoryParams{}, err
	}
	authConfig := existingConfig
	if input.AuthConfig != nil {
		authConfig = MergeAuthConfigPreservingRedactedSecrets(existingConfig, *input.AuthConfig, input.RedactedSecretValue)
	}
	authType := valueOr(input.AuthType, existing.AuthType)
	if authType == "" {
		authType = InferAuthType(authConfig)
	}
	cleanURL, err := ValidateRepositoryURL(valueOr(input.URL, existing.Url))
	if err != nil {
		return sqlc.UpdateHelmRepositoryParams{}, ValidationError{err: err}
	}
	sealed, publicConfig, err := sealRepositoryAuthConfig(authConfig, encryptor)
	if err != nil {
		return sqlc.UpdateHelmRepositoryParams{}, err
	}
	return sqlc.UpdateHelmRepositoryParams{
		ID:                  existing.ID,
		Name:                valueOr(input.Name, existing.Name),
		Url:                 cleanURL,
		RepoType:            valueOr(input.RepoType, existing.RepoType),
		Description:         valueOr(input.Description, existing.Description),
		IsDefault:           valueOr(input.IsDefault, existing.IsDefault),
		AuthType:            authType,
		AuthConfig:          publicConfig,
		AuthConfigEncrypted: sealed,
		Enabled:             valueOr(input.Enabled, existing.Enabled),
	}, nil
}

// ValidateRepositoryURL refuses credential-bearing or ambiguous repository
// addresses so credentials can only enter the encrypted auth-config envelope.
func ValidateRepositoryURL(raw string) (string, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "", errors.New("repository URL is required")
	}
	if strings.HasPrefix(clean, "git@") && !strings.ContainsAny(clean, "?#") {
		return clean, nil
	}
	parsed, err := url.Parse(clean)
	if err != nil {
		return "", errors.New("repository URL is invalid")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("repository URL must not contain credentials, query parameters, or fragments; use auth_config")
	}
	return clean, nil
}

func sealRepositoryAuthConfig(authConfig json.RawMessage, encryptor Encryptor) (string, json.RawMessage, error) {
	if encryptor == nil && HasAuthConfigSecret(authConfig) {
		return "", nil, ErrRepositoryCredentialEncryptionUnavailable
	}
	return SealAuthConfig(authConfig, encryptor)
}

// MergeAuthConfigPreservingRedactedSecrets keeps the stored secret value when
// a UI sends its redaction sentinel or an empty secret field during an edit.
func MergeAuthConfigPreservingRedactedSecrets(existing, incoming json.RawMessage, sentinel string) json.RawMessage {
	if len(incoming) == 0 {
		return existing
	}
	var in map[string]any
	if err := json.Unmarshal(incoming, &in); err != nil || in == nil {
		return existing
	}
	var stored map[string]any
	_ = json.Unmarshal(existing, &stored)
	if stored == nil {
		stored = map[string]any{}
	}
	for _, key := range AuthConfigSecretKeys {
		value, ok := in[key]
		if !ok {
			continue
		}
		text, isString := value.(string)
		if !isString || (text != sentinel && text != "") {
			continue
		}
		if previous, found := stored[key]; found {
			in[key] = previous
		} else {
			delete(in, key)
		}
	}
	result, err := json.Marshal(in)
	if err != nil {
		return existing
	}
	return result
}

func valueOr[T any](value *T, fallback T) T {
	if value == nil {
		return fallback
	}
	return *value
}
