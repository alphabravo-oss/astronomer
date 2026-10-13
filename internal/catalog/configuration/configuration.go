// Package configuration defines catalog-owned, immutable version configuration.
package configuration

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	semver "github.com/Masterminds/semver/v3"
)

type Preset struct {
	ID          string         `json:"id"`
	Label       string         `json:"label"`
	Description string         `json:"description"`
	Values      map[string]any `json:"values"`
}
type Configuration struct {
	Schema        json.RawMessage `json:"schema"`
	Presets       []Preset        `json:"presets"`
	DefaultPreset string          `json:"defaultPreset"`
	Namespace     string          `json:"namespace"`
}
type Version struct {
	UnavailableReason string `json:"unavailableReason,omitempty"`
	UpgradeFrom       string `json:"upgradeFrom,omitempty"`
	Version           string `json:"version"`
	Available         bool   `json:"available"`
	Recommended       bool   `json:"recommended"`
	Deprecated        bool   `json:"deprecated"`
	Revoked           bool   `json:"revoked"`
	SupportTier       string `json:"supportTier"`
	Artifact          struct {
		Repository string `json:"repository"`
		Name       string `json:"name"`
		Version    string `json:"version"`
		Digest     string `json:"digest"`
	} `json:"artifact"`
	Compatibility struct {
		Kubernetes    string   `json:"kubernetes"`
		Architectures []string `json:"architectures,omitempty"`
	} `json:"compatibility"`
	Configuration *Configuration `json:"configuration,omitempty"`
}
type Artifact struct {
	Repository string    `json:"repository"`
	Chart      string    `json:"chart"`
	Version    string    `json:"version"`
	Versions   []Version `json:"versions"`
}

var chartDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func Parse(raw json.RawMessage) (Artifact, error) {
	var artifact Artifact
	if err := json.Unmarshal(raw, &artifact); err != nil {
		return artifact, err
	}
	if len(artifact.Versions) > 100 {
		return artifact, errors.New("too many catalog versions")
	}
	seen := map[string]bool{}
	for _, v := range artifact.Versions {
		if seen[v.Version] || v.Version == "" || v.Artifact.Version != v.Version || v.Artifact.Repository != artifact.Repository || v.Artifact.Name != artifact.Chart || !chartDigestPattern.MatchString(v.Artifact.Digest) {
			return artifact, errors.New("invalid catalog version identity")
		}
		seen[v.Version] = true
	}
	return artifact, nil
}
func (a Artifact) Find(version string) *Version {
	for i := range a.Versions {
		if a.Versions[i].Version == version {
			return &a.Versions[i]
		}
	}
	return nil
}
func (v Version) Validate(digest string) error {
	if v.Revoked || !v.Available {
		return errors.New("this catalog version is unavailable or revoked")
	}
	// Helm HTTP indexes store a bare SHA-256 hex digest; OCI-style catalog
	// identities include the algorithm. Compare the same validated identity.
	if !strings.Contains(digest, ":") {
		digest = "sha256:" + digest
	}
	if !chartDigestPattern.MatchString(digest) || v.Artifact.Digest != digest {
		return errors.New("the chart digest differs from the reviewed catalog version")
	}
	return nil
}

// Eligible returns a presentation copy; it never changes the published record.
func (v Version) Eligible(kubernetes string) Version {
	if !v.Available || v.Revoked {
		v.UnavailableReason = "Unavailable or revoked by the catalog"
		return v
	}
	if v.Compatibility.Kubernetes == "" {
		return v
	}
	constraint, err := semver.NewConstraint(v.Compatibility.Kubernetes)
	current, versionErr := semver.NewVersion(kubernetes)
	if err != nil || versionErr != nil || !constraint.Check(current) {
		v.Available = false
		v.UnavailableReason = "Requires Kubernetes " + v.Compatibility.Kubernetes
	}
	return v
}
func (v Version) CheckUpgrade(from string) error {
	if v.UpgradeFrom == "" {
		return nil
	}
	constraint, err := semver.NewConstraint(v.UpgradeFrom)
	current, versionErr := semver.NewVersion(from)
	if err != nil || versionErr != nil || !constraint.Check(current) {
		return errors.New("the installed chart version does not satisfy the catalog upgrade constraint " + v.UpgradeFrom)
	}
	return nil
}
