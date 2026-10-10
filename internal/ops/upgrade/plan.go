// Package upgrade owns the state of a signed release upgrade. The shell
// entrypoint remains authoritative until its adapter and failure parity pass.
package upgrade

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
	digest "github.com/opencontainers/go-digest"
	"oras.land/oras-go/v2/registry"
)

type Mode string

const (
	Preview Mode = "dry_run"
	Execute Mode = "upgrade"
)

type Stage string

const (
	Verify          Stage = "verify"
	Backup          Stage = "backup"
	DryRun          Stage = "dry_run"
	ReplaceDatabase Stage = "replace_database"
	Apply           Stage = "apply"
	Observe         Stage = "observe"
	Rollback        Stage = "rollback"
	Cleanup         Stage = "cleanup"
)

// Identity is re-read by the adapter before mutation, rollback and cleanup.
// Matching an API URL alone is insufficient to own a recreated namespace.
type Identity struct {
	Context      string
	APIServer    string
	Namespace    string
	NamespaceUID string
	Release      string
	ReleaseUID   string
}

type DisposableLease struct {
	NamespaceUID        string
	DatabasePodUID      string
	DatabaseName        string
	RestoreDatabaseName string
	Owner               string
}

type Plan struct {
	Target         string
	Previous       string
	Mode           Mode
	Identity       Identity
	Timeout        time.Duration
	CleanupTimeout time.Duration
	Replace        *DisposableLease
}

var stableV1 = regexp.MustCompile(`^v1\.[0-9]+\.[0-9]+$`)

func (p Plan) Validate() error {
	if !stableV1.MatchString(p.Target) || !stableV1.MatchString(p.Previous) || (p.Mode != Preview && p.Mode != Execute) {
		return errors.New("exact stable v1 target, previous release and mode required")
	}
	target, targetErr := semver.NewVersion(p.Target)
	previous, previousErr := semver.NewVersion(p.Previous)
	if targetErr != nil || previousErr != nil || !target.GreaterThan(previous) {
		return errors.New("target must be newer than installed release")
	}
	endpoint, err := url.Parse(p.Identity.APIServer)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("explicit safe HTTPS Kubernetes API identity required")
	}
	for _, field := range []string{p.Identity.Context, p.Identity.Namespace, p.Identity.NamespaceUID, p.Identity.Release, p.Identity.ReleaseUID} {
		if field == "" || strings.ContainsAny(field, "\t\r\n") {
			return errors.New("explicit immutable release and namespace identities required")
		}
	}
	if p.Timeout <= 0 || p.Timeout > time.Hour || p.CleanupTimeout <= 0 || p.CleanupTimeout > 5*time.Minute {
		return errors.New("bounded operation and cleanup deadlines required")
	}
	if p.Replace != nil {
		lease := p.Replace
		if p.Mode != Execute || lease.NamespaceUID != p.Identity.NamespaceUID || lease.DatabasePodUID == "" || lease.DatabaseName == "" || lease.RestoreDatabaseName == "" || lease.DatabaseName == lease.RestoreDatabaseName || lease.Owner == "" {
			return errors.New("explicit disposable database replacement lease required")
		}
	}
	return nil
}

// RuntimeBindings resolves chart roles against exact source references supplied
// by the byte-verified target chart. It does not infer a PostgreSQL/Valkey major
// from current local defaults or replace historical target identities.
func RuntimeBindings(manifest releasecontract.Manifest, chartSources map[string]string) (map[string]string, error) {
	roles := []string{"utilities.busybox", "postgres", "redis", "dex", "managementRestoreDrill.sidecar", "managementLogging"}
	if len(chartSources) != len(roles) {
		return nil, errors.New("exact target chart runtime role contract required")
	}
	result := make(map[string]string, len(roles))
	for _, role := range roles {
		source := chartSources[role]
		if source == "" {
			return nil, errors.New("target chart runtime role absent")
		}
		count := 0
		for _, image := range manifest.Astronomer.RuntimeImages {
			if image.SourceReference != source {
				continue
			}
			count++
			parsed, err := registry.ParseReference(image.Reference)
			if err != nil || parsed.Registry+"/"+parsed.Repository+"@"+parsed.Reference != image.Reference {
				return nil, errors.New("runtime role is not a canonical OCI digest reference")
			}
			content, err := digest.Parse(parsed.Reference)
			if err != nil || content.Algorithm() != digest.SHA256 || content.Validate() != nil {
				return nil, errors.New("runtime role is not immutable")
			}
			result[role] = image.Reference
		}
		if count != 1 {
			return nil, errors.New("target runtime source must occur exactly once")
		}
	}
	return result, nil
}
