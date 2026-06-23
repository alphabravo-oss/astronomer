package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// HelmHandler processes Helm operations received through the tunnel.
type HelmHandler struct {
	settings *cli.EnvSettings
	log      *slog.Logger
}

// NewHelmHandler creates a new Helm operations handler.
//
// The Helm SDK derives its cache/config/data dirs from XDG env vars and HOME.
// The agent runs as a non-root user inside a read-only-root container with
// HOME=/, so without these overrides the SDK tries to write to /.cache/helm/*
// on the first chart fetch and crashes with "permission denied". Force every
// helm-managed dir to /tmp; values caller-provided via env still win.
func NewHelmHandler(log *slog.Logger) *HelmHandler {
	setIfEmpty := func(k, v string) {
		if os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
	setIfEmpty("HELM_CACHE_HOME", "/tmp/helm/cache")
	setIfEmpty("HELM_CONFIG_HOME", "/tmp/helm/config")
	setIfEmpty("HELM_DATA_HOME", "/tmp/helm/data")
	setIfEmpty("HELM_REGISTRY_CONFIG", "/tmp/helm/config/registry/config.json")
	setIfEmpty("HELM_REPOSITORY_CONFIG", "/tmp/helm/config/repositories.yaml")
	setIfEmpty("HELM_REPOSITORY_CACHE", "/tmp/helm/cache/repository")
	setIfEmpty("XDG_CACHE_HOME", "/tmp/.cache")
	setIfEmpty("XDG_CONFIG_HOME", "/tmp/.config")
	setIfEmpty("XDG_DATA_HOME", "/tmp/.local/share")

	return &HelmHandler{
		settings: cli.New(),
		log:      log,
	}
}

// actionConfig creates a new Helm action configuration for the given namespace.
func (h *HelmHandler) actionConfig(namespace string) (*action.Configuration, error) {
	cfg := new(action.Configuration)
	logFunc := func(format string, v ...interface{}) {
		h.log.Debug(fmt.Sprintf(format, v...), "component", "helm")
	}
	if err := cfg.Init(h.settings.RESTClientGetter(), namespace, os.Getenv("HELM_DRIVER"), logFunc); err != nil {
		return nil, fmt.Errorf("init helm config: %w", err)
	}
	return cfg, nil
}

// helmResult constructs a HELM_RESULT response message.
// defaultHelmReadyTimeout bounds how long an install/upgrade waits for
// workloads to become Ready before giving up. Matches the helm CLI default.
const defaultHelmReadyTimeout = 5 * time.Minute

// helmReadyTimeout returns the wait timeout for an install/upgrade, honoring
// the caller-provided seconds and falling back to the helm CLI default.
func helmReadyTimeout(seconds int) time.Duration {
	if seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return defaultHelmReadyTimeout
}

func helmResult(streamID string, releaseName, namespace, status string, revision int, err error) *protocol.Message {
	result := protocol.HelmResultPayload{
		Success:     err == nil,
		ReleaseName: releaseName,
		Namespace:   namespace,
		Status:      status,
		Revision:    revision,
	}
	if err != nil {
		result.Error = err.Error()
	}
	payload, _ := json.Marshal(result)
	return &protocol.Message{
		Type:      protocol.MsgHelmResult,
		StreamID:  streamID,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

func decodeHelmRequest(msg *protocol.Message) (*protocol.HelmRequestPayload, error) {
	var req protocol.HelmRequestPayload
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return nil, fmt.Errorf("unmarshal helm request: %w", err)
	}
	return &req, nil
}

// locateChart resolves and loads a chart from the request parameters.
//
// OCI charts are resolved via a registry client wired into the install
// action's ChartPathOptions; the helm SDK requires this to be set whenever
// the chart name has the oci:// prefix (otherwise LocateChart returns
// "missing registry client").
func (h *HelmHandler) locateChart(req *protocol.HelmRequestPayload) (string, error) {
	// If a direct chart URL is provided, use it.
	if req.ChartURL != "" {
		return req.ChartURL, nil
	}

	// Compose the lookup name: an OCI repo URL + chart name yields a single
	// ref like oci://ghcr.io/argoproj/argo-helm/argo-cd. Traditional helm
	// repos use the chart-name-only form and rely on RepoURL/index.yaml.
	chartName := req.ChartName
	repoURL := strings.TrimSpace(req.RepoURL)
	isOCI := strings.HasPrefix(strings.ToLower(repoURL), "oci://") ||
		strings.HasPrefix(strings.ToLower(chartName), "oci://")
	if isOCI && !strings.HasPrefix(strings.ToLower(chartName), "oci://") && repoURL != "" {
		chartName = strings.TrimRight(repoURL, "/") + "/" + strings.TrimLeft(req.ChartName, "/")
	}

	// Pass a non-nil *action.Configuration. Helm v3.20+ derefs cfg fields on
	// some chart-resolution paths, so action.NewInstall(nil) panics with a
	// nil pointer in LocateChart. We don't need a fully-initialized
	// configuration to locate a chart — an empty struct is enough.
	install := action.NewInstall(&action.Configuration{})
	if isOCI {
		rc, err := registry.NewClient()
		if err != nil {
			return "", fmt.Errorf("init OCI registry client: %w", err)
		}
		install.SetRegistryClient(rc)
	}
	// For traditional helm repos, set RepoURL so the SDK fetches the index
	// itself and resolves chartName against it. Without this, LocateChart
	// requires either a "repo_name/chart" prefix (we don't register repos
	// in helm config) or a fully-qualified chart URL.
	if repoURL != "" && !isOCI {
		install.RepoURL = repoURL
	}
	if req.Version != "" {
		install.Version = req.Version
	}
	cp, err := install.LocateChart(chartName, h.settings)
	if err != nil {
		return "", fmt.Errorf("locate chart %s: %w", chartName, err)
	}
	return cp, nil
}

// HandleInstall processes HELM_INSTALL messages.
func (h *HelmHandler) HandleInstall(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	req, err := decodeHelmRequest(msg)
	if err != nil {
		return helmResult(msg.StreamID, "", "", "", 0, err), nil
	}

	h.log.Info("helm install", "release", req.ReleaseName, "namespace", req.Namespace)

	cfg, err := h.actionConfig(req.Namespace)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	install := action.NewInstall(cfg)
	install.ReleaseName = req.ReleaseName
	install.Namespace = req.Namespace
	install.CreateNamespace = true
	if req.Version != "" {
		install.Version = req.Version
	}
	// Block until the release's workloads are actually Ready so the
	// reported "deployed" status implies readiness rather than merely
	// "manifests applied". Without Wait, helm returns "deployed" the moment
	// the objects are created, which the server treats as Ready — a false
	// positive while pods are still rolling out.
	install.Wait = true
	install.Timeout = helmReadyTimeout(req.Timeout)

	chartPath, err := h.locateChart(req)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0,
			fmt.Errorf("load chart: %w", err)), nil
	}

	rel, err := install.RunWithContext(ctx, chart, req.Values)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	return helmResult(msg.StreamID, rel.Name, rel.Namespace, rel.Info.Status.String(), rel.Version, nil), nil
}

// HandleUpgrade processes HELM_UPGRADE messages.
func (h *HelmHandler) HandleUpgrade(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	req, err := decodeHelmRequest(msg)
	if err != nil {
		return helmResult(msg.StreamID, "", "", "", 0, err), nil
	}

	h.log.Info("helm upgrade", "release", req.ReleaseName, "namespace", req.Namespace)

	cfg, err := h.actionConfig(req.Namespace)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	upgrade := action.NewUpgrade(cfg)
	upgrade.Namespace = req.Namespace
	if req.Version != "" {
		upgrade.Version = req.Version
	}
	// See HandleInstall: block until workloads are Ready so "deployed"
	// implies readiness instead of just "manifests applied".
	upgrade.Wait = true
	upgrade.Timeout = helmReadyTimeout(req.Timeout)

	chartPath, err := h.locateChart(req)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0,
			fmt.Errorf("load chart: %w", err)), nil
	}

	rel, err := upgrade.RunWithContext(ctx, req.ReleaseName, chart, req.Values)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	return helmResult(msg.StreamID, rel.Name, rel.Namespace, rel.Info.Status.String(), rel.Version, nil), nil
}

// HandleUninstall processes HELM_UNINSTALL messages.
func (h *HelmHandler) HandleUninstall(_ context.Context, msg *protocol.Message) (*protocol.Message, error) {
	req, err := decodeHelmRequest(msg)
	if err != nil {
		return helmResult(msg.StreamID, "", "", "", 0, err), nil
	}

	h.log.Info("helm uninstall", "release", req.ReleaseName, "namespace", req.Namespace)

	cfg, err := h.actionConfig(req.Namespace)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	uninstall := action.NewUninstall(cfg)
	if req.Timeout > 0 {
		uninstall.Timeout = time.Duration(req.Timeout) * time.Second
	}

	resp, err := uninstall.Run(req.ReleaseName)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	status := ""
	if resp.Release != nil {
		status = resp.Release.Info.Status.String()
	}

	return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, status, 0, nil), nil
}

// HandleRollback processes HELM_ROLLBACK messages.
func (h *HelmHandler) HandleRollback(_ context.Context, msg *protocol.Message) (*protocol.Message, error) {
	req, err := decodeHelmRequest(msg)
	if err != nil {
		return helmResult(msg.StreamID, "", "", "", 0, err), nil
	}

	h.log.Info("helm rollback", "release", req.ReleaseName, "namespace", req.Namespace, "revision", req.Revision)

	cfg, err := h.actionConfig(req.Namespace)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	rollback := action.NewRollback(cfg)
	rollback.Version = req.Revision
	if req.Timeout > 0 {
		rollback.Timeout = time.Duration(req.Timeout) * time.Second
	}

	err = rollback.Run(req.ReleaseName)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", req.Revision, err), nil
	}

	return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "rolled-back", req.Revision, nil), nil
}

// HandleStatus processes HELM_STATUS messages.
func (h *HelmHandler) HandleStatus(_ context.Context, msg *protocol.Message) (*protocol.Message, error) {
	req, err := decodeHelmRequest(msg)
	if err != nil {
		return helmResult(msg.StreamID, "", "", "", 0, err), nil
	}

	h.log.Info("helm status", "release", req.ReleaseName, "namespace", req.Namespace)

	cfg, err := h.actionConfig(req.Namespace)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	status := action.NewStatus(cfg)
	rel, err := status.Run(req.ReleaseName)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	return helmResult(msg.StreamID, rel.Name, rel.Namespace, rel.Info.Status.String(), rel.Version, nil), nil
}
