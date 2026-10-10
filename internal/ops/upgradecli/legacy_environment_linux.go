//go:build linux

package upgradecli

import (
	"errors"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LegacyEnvironment captures only the named upgrade wrapper inputs. It is not
// a trust policy, credential capture, kubeconfig merger or source of observed
// namespace/revision/database identities. The executable supplies lookup once;
// this leaf package never reads ambient environment, HOME or PATH itself.
// External/replace/proof settings remain captured for the future corresponding
// concrete adapters; retaining their strings does not authorize those modes.
type LegacyEnvironment struct {
	Release, Namespace, ChartReference, ReleaseRepository   string
	BackupRoot, EvidenceDirectory, ReleaseArtifactDirectory string
	Context, APIServer                                      string
	Timeout                                                 time.Duration
	HealthPort                                              uint16
	MinimumReadyNodes                                       uint32
	MinimumBackupFreeKiB                                    uint64
	ExternalBackupConfirmed                                 bool
	ExternalSchemaVersion, DecryptProofWebhookID            string
	ReplaceDatabase                                         bool
}

var positiveLegacyInteger = regexp.MustCompile(`^[1-9][0-9]*$`)
var legacyHealthPort = regexp.MustCompile(`^[1-9][0-9]{0,4}$`)

// CaptureLegacyEnvironment preserves the shell's ${NAME:-default} rule: unset
// and empty are equivalent, without trimming operator input. Relative public
// and private roots resolve against the caller's independently captured cwd.
// No directory is created and no command runs. Returned errors contain only
// setting names, never values, credentials, endpoints or private paths.
func CaptureLegacyEnvironment(lookup func(string) (string, bool), directory string) (LegacyEnvironment, error) {
	var empty LegacyEnvironment
	if lookup == nil || !filepath.IsAbs(directory) {
		return empty, errors.New("explicit operator lookup and absolute initial directory required")
	}
	get := func(name, fallback string) string {
		value, _ := lookup(name)
		if value == "" {
			return fallback
		}
		return value
	}
	env := LegacyEnvironment{Release: get("RELEASE", "astronomer"), Namespace: get("NAMESPACE", "astronomer"), ChartReference: get("CHART_REF", "oci://ghcr.io/alphabravo-oss/charts/astronomer"), ReleaseRepository: get("RELEASE_REPO", "alphabravo-oss/astronomer"), Context: get("KUBE_CONTEXT", ""), APIServer: get("EXPECTED_KUBE_API_SERVER", ""), ReleaseArtifactDirectory: get("RELEASE_ARTIFACT_DIR", ""), ExternalSchemaVersion: get("EXTERNAL_DB_SCHEMA_VERSION", ""), DecryptProofWebhookID: get("RC_DECRYPT_PROOF_WEBHOOK_ID", ""), ExternalBackupConfirmed: get("EXTERNAL_DB_BACKUP_CONFIRMED", "") == "1", ReplaceDatabase: get("RC_REPLACE_DATABASE_FROM_BACKUP", "") == "1"}
	if env.Context == "" || strings.ContainsAny(env.Context, "\t\r\n") {
		return empty, errors.New("KUBE_CONTEXT requires explicit safe context")
	}
	endpoint, err := url.Parse(env.APIServer)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || strings.ContainsAny(env.APIServer, "\t\r\n ") {
		return empty, errors.New("EXPECTED_KUBE_API_SERVER requires safe explicit HTTPS identity")
	}
	// Existing concrete process/Plan bounds remain independent admission checks.
	// Parse without clipping; values beyond their bounds fail there, not silently
	// converted to a different requested timeout.
	env.Timeout, err = time.ParseDuration(get("TIMEOUT", "15m"))
	if err != nil || env.Timeout <= 0 {
		return empty, errors.New("TIMEOUT requires positive duration")
	}
	rawPort := get("HEALTH_PORT", "18080")
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || !legacyHealthPort.MatchString(rawPort) || port == 0 {
		return empty, errors.New("HEALTH_PORT requires port1-65535")
	}
	env.HealthPort = uint16(port)
	rawNodes := get("MIN_READY_NODES", "1")
	nodes, err := strconv.ParseUint(rawNodes, 10, 32)
	if err != nil || !positiveLegacyInteger.MatchString(rawNodes) {
		return empty, errors.New("MIN_READY_NODES requires representable positive count")
	}
	env.MinimumReadyNodes = uint32(nodes)
	rawFree := get("MIN_BACKUP_FREE_KIB", "1048576")
	free, err := strconv.ParseUint(rawFree, 10, 64)
	if err != nil || !positiveLegacyInteger.MatchString(rawFree) {
		return empty, errors.New("MIN_BACKUP_FREE_KIB requires representable positive count")
	}
	env.MinimumBackupFreeKiB = free
	resolve := func(path string) string {
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(directory, path)
	}
	env.BackupRoot = resolve(get("BACKUP_ROOT", "./astronomer-upgrade-backups"))
	env.EvidenceDirectory = resolve(get("SANITIZED_EVIDENCE_DIR", "./astronomer-upgrade-evidence"))
	if env.ReleaseArtifactDirectory != "" {
		env.ReleaseArtifactDirectory = resolve(env.ReleaseArtifactDirectory)
	}
	return env, nil
}

// MatchesObservedIdentity checks operator selection against the independently
// read initial identity. It deliberately cannot fill missing UID/revision fields,
// grant replacement or authorize a database. Tool/private/auth bootstrap remains
// explicit and must use the same captured selection for its actual observations.
func (e LegacyEnvironment) MatchesObservedIdentity(options InvocationOptions) error {
	id := options.Plan.Identity
	if id.Context != e.Context || id.APIServer != e.APIServer || id.Release != e.Release || id.Namespace != e.Namespace || id.NamespaceUID == "" || id.ReleaseUID == "" || options.Fence.Identity != id {
		return errors.New("captured operator selection differs from independent observed identity")
	}
	return nil
}
