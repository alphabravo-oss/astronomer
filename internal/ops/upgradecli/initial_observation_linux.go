//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

// InitialInstallation contains only independently observed initial bindings.
// Operator selections never supply namespace, Helm revision or database UIDs.
type InitialInstallation struct {
	Identity                              upgrade.Identity
	Revision                              uint64
	CurrentVersion                        string
	DatabasePod, DatabasePodUID, Database string
	DatabaseState                         DatabaseSchemaState
	ExternalDatabase                      bool
	CloudNativePG                         bool
	cnpg                                  *CloudNativePGSource
	DatabaseValuesSHA256                  [32]byte
}

// InitialObserver owns the initial private kubeconfig bytes across every
// read-only command. It uses already authority-bound tools through Commands;
// it does not select a tool, read ambient credentials, or infer initial UIDs.
type InitialObserver struct {
	commands      *Commands
	helm, kubectl Tool
	selection     LegacyEnvironment
	kubeconfig    string
	identity      os.FileInfo
	hash          [32]byte
	execute       func(context.Context, Tool, []string) ([]byte, error)
}

const initialDatabaseTemplate = `{{range .items}}{{.metadata.name}}{{"\t"}}{{.metadata.uid}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{index .metadata.labels "app.kubernetes.io/instance"}}{{"\t"}}{{index .metadata.labels "app.kubernetes.io/component"}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}{{"\n"}}{{end}}`

func NewInitialObserver(commands *Commands, helm, kubectl Tool, selection LegacyEnvironment, kubeconfig string) (*InitialObserver, error) {
	endpoint, endpointErr := url.Parse(selection.APIServer)
	if commands == nil || helm.Kind != Helm || kubectl.Kind != Kubectl || !filepath.IsAbs(kubeconfig) || !kubeName.MatchString(selection.Release) || !kubeName.MatchString(selection.Namespace) || selection.Context == "" || strings.ContainsAny(selection.Context, "\t\r\n") || endpointErr != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || strings.ContainsAny(selection.APIServer, "\t\r\n ") {
		return nil, errors.New("explicit initial selection and authority-bound tools required")
	}
	identity, hash, err := privateBoundInput(kubeconfig)
	if err != nil {
		return nil, err
	}
	observer := &InitialObserver{commands: commands, helm: helm, kubectl: kubectl, selection: selection, kubeconfig: kubeconfig, identity: identity, hash: hash}
	observer.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		raw, _, err := commands.Execute(ctx, tool, args)
		return raw, err
	}
	return observer, nil
}

func (o *InitialObserver) read(ctx context.Context, tool Tool, args []string) ([]byte, error) {
	if ctx == nil || o == nil || o.execute == nil {
		return nil, errors.New("explicit initial observation context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, hash, err := privateBoundInput(o.kubeconfig)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(o.identity, before) || o.hash != hash {
		return nil, errors.New("initial kubeconfig identity changed")
	}
	raw, err := o.execute(ctx, tool, args)
	if err != nil {
		return nil, err
	}
	after, hash, err := privateBoundInput(o.kubeconfig)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(o.identity, after) || o.hash != hash {
		return nil, errors.New("initial kubeconfig changed during observation")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}

func (o *InitialObserver) kube(ctx context.Context, args ...string) ([]byte, error) {
	base := []string{"--kubeconfig", o.kubeconfig, "--context", o.selection.Context}
	return o.read(ctx, o.kubectl, append(base, args...))
}

type initialHelmMetadata struct {
	Name         string            `json:"name"`
	Namespace    string            `json:"namespace"`
	Revision     uint64            `json:"revision"`
	Status       string            `json:"status"`
	Chart        string            `json:"chart"`
	Version      string            `json:"version"`
	AppVersion   string            `json:"appVersion"`
	DeployedAt   string            `json:"deployedAt"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Dependencies []json.RawMessage `json:"dependencies,omitempty"`
	ApplyMethod  string            `json:"applyMethod,omitempty"`
}

// Observe performs only bounded joined reads. A complete original Helm Secret
// and namespace are re-read at the end; a failed/partial observation returns no
// binding. Bundled database observation is not a backup or mutation lease.
// External/replacement adapters retain their separate explicit admission.
func (o *InitialObserver) Observe(ctx context.Context, target string) (InitialInstallation, error) {
	var empty InitialInstallation
	if ctx == nil || o == nil || !legacyStableTarget.MatchString(target) {
		return empty, errors.New("exact initial target/context required")
	}
	server, err := o.kube(ctx, "config", "view", "--minify", "--raw", "--output", "jsonpath={.clusters[0].cluster.server}")
	if err != nil {
		return empty, err
	}
	if string(server) != o.selection.APIServer {
		return empty, errors.New("selected Kubernetes API differs")
	}
	namespace, err := o.kube(ctx, "get", "namespace", o.selection.Namespace, "--output", "go-template="+namespaceTemplate)
	if err != nil {
		return empty, err
	}
	fields := strings.Split(string(namespace), "\t")
	if len(fields) != 3 || fields[0] == "" || !equalFields(namespace, fields[0], o.selection.Namespace, "live") {
		return empty, errors.New("initial namespace identity/lifecycle differs")
	}
	raw, err := o.read(ctx, o.helm, []string{"--kubeconfig", o.kubeconfig, "--kube-context", o.selection.Context, "get", "metadata", o.selection.Release, "--namespace", o.selection.Namespace, "--output", "json"})
	if err != nil {
		return empty, err
	}
	if len(raw) > 64<<10 {
		return empty, errors.New("initial Helm metadata exceeds bound")
	}
	var metadata initialHelmMetadata
	if jsoncontract.Decode(raw, &metadata) != nil || metadata.Name != o.selection.Release || metadata.Namespace != o.selection.Namespace || metadata.Chart != "astronomer" || metadata.Status != "deployed" || metadata.Revision == 0 || !legacyStableTarget.MatchString("v"+metadata.Version) || semver.Compare("v"+metadata.Version, target) >= 0 {
		return empty, errors.New("initial deployed release or forward target differs")
	}
	secret := "sh.helm.release.v1." + o.selection.Release + ".v" + strconv.FormatUint(metadata.Revision, 10)
	if !kubeName.MatchString(secret) {
		return empty, errors.New("original Helm revision name exceeds bound")
	}
	revision, err := o.kube(ctx, "get", "secret", secret, "--namespace", o.selection.Namespace, "--output", "go-template="+releaseTemplate)
	if err != nil {
		return empty, err
	}
	fields = strings.Split(string(revision), "\t")
	if len(fields) != 8 || fields[0] == "" || !equalFields(revision, fields[0], secret, o.selection.Namespace, "helm.sh/release.v1", "helm", o.selection.Release, strconv.FormatUint(metadata.Revision, 10), "live") {
		return empty, errors.New("original Helm revision identity/lifecycle differs")
	}
	identity := upgrade.Identity{Context: o.selection.Context, APIServer: o.selection.APIServer, Namespace: o.selection.Namespace, Release: o.selection.Release, NamespaceUID: strings.Split(string(namespace), "\t")[0], ReleaseUID: fields[0]}
	permission, err := o.kube(ctx, "auth", "can-i", "get", "secrets", "--namespace", o.selection.Namespace)
	if err != nil {
		return empty, err
	}
	if string(permission) != "yes\n" {
		return empty, errors.New("current Kubernetes identity cannot back up release Secrets")
	}
	backend, valuesHash, err := observeHelmDatabaseBackend(ctx, o.read, o.helm, o.kubeconfig, identity)
	if err != nil {
		return empty, err
	}
	if backend == "cloudNativePG" {
		return o.observeCNPG(ctx, identity, metadata, valuesHash)
	}
	pods, err := o.kube(ctx, "get", "pods", "--namespace", o.selection.Namespace, "--selector", "app.kubernetes.io/instance="+o.selection.Release+",app.kubernetes.io/component=postgres", "--output", "go-template="+initialDatabaseTemplate)
	if err != nil {
		return empty, err
	}
	rows, err := readinessRows(pods)
	if err != nil || len(rows) > 1 {
		return empty, errors.New("initial database inventory must be complete and unambiguous")
	}
	if len(rows) == 0 {
		if backend != "external" {
			return empty, errors.New("declared bundled database is absent; external confirmation cannot change installed backend")
		}
		schema, schemaErr := strconv.ParseUint(o.selection.ExternalSchemaVersion, 10, 32)
		if !o.selection.ExternalBackupConfirmed || schemaErr != nil || schema == 0 || strconv.FormatUint(schema, 10) != o.selection.ExternalSchemaVersion || o.selection.ReplaceDatabase || o.selection.DecryptProofWebhookID != "" {
			return empty, errors.New("external backup confirmation and exact positive schema required; bundled-only RC controls unavailable")
		}
		fence, err := NewOwnershipFence(o.commands, o.kubectl, FenceOptions{Identity: identity, Kubeconfig: o.kubeconfig, Revision: metadata.Revision})
		if err != nil {
			return empty, err
		}
		fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) { return o.read(ctx, tool, args) }
		if err := fence.Fence(ctx, identity, nil); err != nil {
			return empty, err
		}
		finalPods, err := o.kube(ctx, "get", "pods", "--namespace", o.selection.Namespace, "--selector", "app.kubernetes.io/instance="+o.selection.Release+",app.kubernetes.io/component=postgres", "--output", "go-template="+initialDatabaseTemplate)
		if err != nil || string(finalPods) != string(pods) {
			return empty, errors.New("initial external database selection changed")
		}
		if err := recheckHelmDatabaseBackend(ctx, o.read, o.helm, o.kubeconfig, identity, backend, valuesHash); err != nil {
			return empty, err
		}
		return InitialInstallation{Identity: identity, Revision: metadata.Revision, CurrentVersion: "v" + metadata.Version, DatabaseState: DatabaseSchemaState{Version: uint32(schema)}, ExternalDatabase: true, DatabaseValuesSHA256: valuesHash}, ctx.Err()
	}
	if backend != "bundled" {
		return empty, errors.New("declared external database has an unexpected bundled pod; selection ambiguous")
	}
	fields = strings.Split(rows[0], "\t")
	if len(fields) != 6 || !kubeName.MatchString(fields[0]) || fields[1] == "" || !equalFields([]byte(rows[0]), fields[0], fields[1], o.selection.Namespace, o.selection.Release, "postgres", "live") {
		return empty, errors.New("initial bundled database pod ownership differs")
	}
	pod, podUID := fields[0], fields[1]
	state, err := o.kube(ctx, "exec", "--namespace", o.selection.Namespace, pod, "--", "sh", "-ec", databaseStateScript)
	if err != nil {
		return empty, err
	}
	stateFields := strings.Split(strings.TrimSuffix(string(state), "\n"), "|")
	if len(stateFields) != 5 || !databaseName.MatchString(stateFields[0]) {
		return empty, errors.New("initial database state contract differs")
	}
	parsed, err := parseDatabaseState(state, stateFields[0], 1, ^uint32(0))
	if err != nil {
		return empty, err
	}
	fence, err := NewOwnershipFence(o.commands, o.kubectl, FenceOptions{Identity: identity, Kubeconfig: o.kubeconfig, Revision: metadata.Revision, DatabasePod: pod})
	if err != nil {
		return empty, err
	}
	fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) { return o.read(ctx, tool, args) }
	if err := fence.Fence(ctx, identity, nil); err != nil {
		return empty, err
	}
	finalPods, err := o.kube(ctx, "get", "pods", "--namespace", o.selection.Namespace, "--selector", "app.kubernetes.io/instance="+o.selection.Release+",app.kubernetes.io/component=postgres", "--output", "go-template="+initialDatabaseTemplate)
	if err != nil {
		return empty, err
	}
	if string(finalPods) != string(pods) {
		return empty, errors.New("initial database pod changed")
	}
	finalState, err := o.kube(ctx, "exec", "--namespace", o.selection.Namespace, pod, "--", "sh", "-ec", databaseStateScript)
	if err != nil {
		return empty, err
	}
	if string(finalState) != string(state) {
		return empty, errors.New("initial database/schema changed during observation")
	}
	if err := recheckHelmDatabaseBackend(ctx, o.read, o.helm, o.kubeconfig, identity, backend, valuesHash); err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return InitialInstallation{Identity: identity, Revision: metadata.Revision, CurrentVersion: "v" + metadata.Version, DatabasePod: pod, DatabasePodUID: podUID, Database: stateFields[0], DatabaseState: parsed, DatabaseValuesSHA256: valuesHash}, nil
}

func (i InitialInstallation) fenceOptions(kubeconfig string) FenceOptions {
	pod := i.DatabasePod
	if i.CloudNativePG {
		pod = ""
	}
	return FenceOptions{Identity: i.Identity, Kubeconfig: kubeconfig, Revision: i.Revision, DatabasePod: pod}
}

// Full values come from the exact original deployed release through the owned
// command capture, not from environment or missing-pod inference. Only the
// backend selectors are interpreted; all duplicate keys/depth/trailing input
// remain subject to the canonical reader. Hashing the entire bounded values
// document retains all observed configuration across the initial read stages.
func observeHelmDatabaseBackend(ctx context.Context, read func(context.Context, Tool, []string) ([]byte, error), helm Tool, kubeconfig string, id upgrade.Identity) (string, [32]byte, error) {
	var empty [32]byte
	if ctx == nil || read == nil || helm.Kind != Helm || !filepath.IsAbs(kubeconfig) {
		return "", empty, errors.New("explicit owned Helm backend observation required")
	}
	if err := ctx.Err(); err != nil {
		return "", empty, err
	}
	args := []string{"--kubeconfig", kubeconfig, "--kube-context", id.Context, "get", "values", id.Release, "--namespace", id.Namespace, "--all", "--output", "json"}
	raw, err := read(ctx, helm, args)
	if err != nil {
		return "", empty, err
	}
	mode, err := parseHelmDatabaseBackend(raw)
	if err != nil {
		return "", empty, err
	}
	if err := ctx.Err(); err != nil {
		return "", empty, err
	}
	return mode, sha256.Sum256(raw), nil
}

func parseHelmDatabaseBackend(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return "", errors.New("initial Helm values exceed bounded backend contract")
	}
	var values map[string]json.RawMessage
	if jsoncontract.Decode(raw, &values) != nil || values == nil {
		return "", errors.New("initial Helm values document invalid")
	}
	var postgres map[string]json.RawMessage
	if jsoncontract.Decode(values["postgres"], &postgres) != nil || postgres == nil {
		return "", errors.New("installed chart postgres backend values missing")
	}
	mode := ""
	if value, exists := postgres["mode"]; exists {
		if jsoncontract.Decode(value, &mode) != nil {
			return "", errors.New("installed postgres mode invalid")
		}
	}
	if mode != "" {
		switch mode {
		case "bundled", "cloudNativePG", "external":
			return mode, nil
		default:
			return "", errors.New("installed postgres backend unsupported")
		}
	}
	var bundled map[string]json.RawMessage
	if jsoncontract.Decode(postgres["bundled"], &bundled) != nil || bundled == nil {
		return "", errors.New("installed legacy postgres selector missing")
	}
	enabled := false
	if value, exists := bundled["enabled"]; !exists || jsoncontract.Decode(value, &enabled) != nil {
		return "", errors.New("installed legacy postgres selector invalid")
	}
	if enabled {
		return "bundled", nil
	}
	return "external", nil
}

func recheckHelmDatabaseBackend(ctx context.Context, read func(context.Context, Tool, []string) ([]byte, error), helm Tool, kubeconfig string, id upgrade.Identity, mode string, hash [32]byte) error {
	current, currentHash, err := observeHelmDatabaseBackend(ctx, read, helm, kubeconfig, id)
	if err != nil {
		return err
	}
	if current != mode || currentHash != hash {
		return errors.New("initial Helm backend/configuration changed")
	}
	return ctx.Err()
}
