//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

// legacyBootstrapNames includes only explicitly named operator selections and
// established authentication/plugin client inputs. Capturing these does not
// grant signing policy, tool selection, cluster ownership or database authority.
var legacyBootstrapNames = []string{
	"RELEASE", "NAMESPACE", "CHART_REF", "RELEASE_REPO", "BACKUP_ROOT", "TIMEOUT", "HEALTH_PORT", "MIN_READY_NODES", "MIN_BACKUP_FREE_KIB", "KUBE_CONTEXT", "EXPECTED_KUBE_API_SERVER", "RELEASE_ARTIFACT_DIR", "SANITIZED_EVIDENCE_DIR", "EXTERNAL_DB_BACKUP_CONFIRMED", "EXTERNAL_DB_SCHEMA_VERSION", "RC_DECRYPT_PROOF_WEBHOOK_ID", "RC_REPLACE_DATABASE_FROM_BACKUP", "KUBECONFIG", "UPGRADE_TOOLCHAIN_KIT", "UPGRADE_MAX_BACKUP_BYTES", "UPGRADE_DISPOSABLE_LEASE_FILE",
	"PATH", "HOME", "LANG", "LC_ALL", "GH_TOKEN", "GITHUB_TOKEN", "GH_HOST", "GH_CONFIG_DIR",
	"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy",
	"HELM_CONFIG_HOME", "HELM_CACHE_HOME", "HELM_DATA_HOME", "HELM_REPOSITORY_CONFIG", "HELM_REPOSITORY_CACHE", "HELM_REGISTRY_CONFIG", "HELM_PLUGINS",
	"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN",
	"GOOGLE_APPLICATION_CREDENTIALS", "CLOUDSDK_CONFIG", "AZURE_CONFIG_DIR", "AZURE_CLIENT_ID", "AZURE_TENANT_ID", "AZURE_CLIENT_SECRET", "SSH_AUTH_SOCK", "SSL_CERT_FILE", "SSL_CERT_DIR",
}

var legacyChildNames = []string{
	"PATH", "HOME", "LANG", "LC_ALL", "GH_TOKEN", "GITHUB_TOKEN", "GH_HOST", "GH_CONFIG_DIR",
	"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy",
	"HELM_CONFIG_HOME", "HELM_CACHE_HOME", "HELM_DATA_HOME", "HELM_REPOSITORY_CONFIG", "HELM_REPOSITORY_CACHE", "HELM_REGISTRY_CONFIG", "HELM_PLUGINS",
	"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN",
	"GOOGLE_APPLICATION_CREDENTIALS", "CLOUDSDK_CONFIG", "AZURE_CONFIG_DIR", "AZURE_CLIENT_ID", "AZURE_TENANT_ID", "AZURE_CLIENT_SECRET", "SSH_AUTH_SOCK", "SSL_CERT_FILE", "SSL_CERT_DIR",
}

// FreezeLegacyLookup reads every declared input once and returns a detached
// lookup. No unlisted environment variable is inherited. It intentionally does
// not expose a map for logging/serialization. Errors contain no setting values.
func FreezeLegacyLookup(lookup func(string) (string, bool)) (func(string) (string, bool), error) {
	if lookup == nil {
		return nil, errors.New("explicit named operator lookup required")
	}
	type value struct {
		raw string
		set bool
	}
	captured := make(map[string]value, len(legacyBootstrapNames))
	for _, name := range legacyBootstrapNames {
		raw, set := lookup(name)
		if len(raw) > 64<<10 || strings.ContainsAny(raw, "\x00\r\n") {
			return nil, errors.New("named operator input exceeds bounded single-line contract")
		}
		captured[name] = value{raw, set}
	}
	return func(name string) (string, bool) { v, ok := captured[name]; return v.raw, ok && v.set }, nil
}

// CaptureLegacyChildEnvironment preserves named operator credentials for pinned
// native tools and kubeconfig auth plugins. All values are stored in exclusive
// private files, never diagnostics, argv, public evidence or literal Env fields.
// PATH/HOME serve the explicit operator auth/plugin contract; no Tool or trusted
// root is selected from them. The process core joins each child and Commands
// additionally binds these initial credential-file identities across all steps.
func CaptureLegacyChildEnvironment(ctx context.Context, lookup func(string) (string, bool), directory, initialDirectory, kubeconfig string) ([]process.Environment, error) {
	if ctx == nil || lookup == nil || !filepath.IsAbs(initialDirectory) || !filepath.IsAbs(kubeconfig) {
		return nil, errors.New("explicit private operator environment scope required")
	}
	if err := privateBootstrapDirectory(directory); err != nil {
		return nil, err
	}
	bindings := make([]process.Environment, 0, len(legacyChildNames)+1)
	for _, name := range append(append([]string(nil), legacyChildNames...), "KUBECONFIG") {
		raw, set := lookup(name)
		if name == "KUBECONFIG" {
			raw, set = kubeconfig, true
		}
		if !set {
			continue
		}
		if len(raw) > 64<<10 || strings.ContainsAny(raw, "\x00\r\n") {
			return nil, errors.New("bounded single-line operator child input required")
		}
		if name == "PATH" {
			parts := strings.Split(raw, ":")
			for i, p := range parts {
				if !filepath.IsAbs(p) {
					parts[i] = filepath.Join(initialDirectory, p)
				}
			}
			raw = strings.Join(parts, ":")
			if len(raw) > 64<<10 {
				return nil, errors.New("resolved operator plugin path exceeds bound")
			}
		}
		file := fmt.Sprintf("environment-%03d", len(bindings)+1)
		if err := writeBootstrapBytes(ctx, directory, file, []byte(raw), 0600); err != nil {
			return nil, err
		}
		bindings = append(bindings, process.Environment{Name: name, SecretFile: filepath.Join(directory, file)})
	}
	return bindings, ctx.Err()
}
