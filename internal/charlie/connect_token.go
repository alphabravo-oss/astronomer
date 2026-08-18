package charlie

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	ConnectTokenSchema = "charlie.connect/v1"
	ConnectTokenPrefix = "charlie.connect.v1."
)

func EncodeConnectToken(endpoint, signingPublicKey string, packageJSON []byte) (string, error) {
	normalized, err := NormalizeCharlieEndpoint(endpoint)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(signingPublicKey) == "" || len(packageJSON) == 0 {
		return "", errors.New("connect token material is invalid")
	}
	payload, err := json.Marshal(connectTokenDocument{
		Schema: ConnectTokenSchema, Endpoint: normalized, SigningPublicKey: strings.TrimSpace(signingPublicKey),
		Package: append(json.RawMessage(nil), packageJSON...),
	})
	if err != nil {
		return "", err
	}
	return ConnectTokenPrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

type connectTokenDocument struct {
	Schema           string          `json:"schema"`
	Endpoint         string          `json:"endpoint"`
	SigningPublicKey string          `json:"signing_public_key"`
	Package          json.RawMessage `json:"package"`
}

type connectPackageIdentity struct {
	DeploymentID string `json:"deployment_id"`
	Route        struct {
		RouteID string `json:"route_id"`
	} `json:"route"`
	Signing struct {
		KeyID           string `json:"key_id"`
		PublicKeySHA256 string `json:"public_key_sha256"`
	} `json:"signing"`
	Central struct {
		BaseURL string `json:"base_url"`
	} `json:"central"`
}

func ParseConnectToken(endpoint, token string) ([]byte, OnboardingConfirmation, error) {
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, token)
	if !strings.HasPrefix(compact, ConnectTokenPrefix) {
		return nil, OnboardingConfirmation{}, errors.New("connect token prefix is invalid")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(compact, ConnectTokenPrefix))
	if err != nil || len(raw) == 0 {
		return nil, OnboardingConfirmation{}, errors.New("connect token encoding is invalid")
	}
	var document connectTokenDocument
	if err := json.Unmarshal(raw, &document); err != nil || document.Schema != ConnectTokenSchema || len(document.Package) == 0 {
		return nil, OnboardingConfirmation{}, errors.New("connect token document is invalid")
	}
	wanted, err := NormalizeCharlieEndpoint(endpoint)
	if err != nil {
		return nil, OnboardingConfirmation{}, err
	}
	got, err := NormalizeCharlieEndpoint(document.Endpoint)
	if err != nil || got != wanted {
		return nil, OnboardingConfirmation{}, errors.New("connect token endpoint does not match")
	}
	var identity connectPackageIdentity
	if err := json.Unmarshal(document.Package, &identity); err != nil {
		return nil, OnboardingConfirmation{}, errors.New("connect token package is invalid")
	}
	packageEndpoint, err := NormalizeCharlieEndpoint(identity.Central.BaseURL)
	if err != nil || packageEndpoint != wanted {
		return nil, OnboardingConfirmation{}, errors.New("onboarding package endpoint does not match")
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(document.SigningPublicKey))
	if err != nil || len(publicKey) != 32 {
		return nil, OnboardingConfirmation{}, errors.New("connect token signing public key is invalid")
	}
	fingerprint := sha256.Sum256(publicKey)
	return append([]byte(nil), document.Package...), OnboardingConfirmation{
		SigningPublicKeyBase64:      strings.TrimSpace(document.SigningPublicKey),
		ConfirmedSigningKeyID:       identity.Signing.KeyID,
		ConfirmedSigningFingerprint: hex.EncodeToString(fingerprint[:]),
		ExpectedDeploymentID:        identity.DeploymentID,
		ExpectedRouteID:             identity.Route.RouteID,
	}, nil
}

func NormalizeCharlieEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("Charlie endpoint must be an https origin")
	}
	parsed.Fragment = ""
	parsed.RawQuery = ""
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	parsed.Path = strings.TrimSuffix(parsed.Path, "/charlie/v1")
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	if parsed.Path != "" {
		return "", fmt.Errorf("Charlie endpoint must not include a path")
	}
	return strings.TrimSuffix(parsed.String(), "/"), nil
}
