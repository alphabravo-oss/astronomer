package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"helm.sh/helm/v4/pkg/action"
	helmchart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/registry"
	releasei "helm.sh/helm/v4/pkg/release"
	"helm.sh/helm/v4/pkg/storage/driver"

	"github.com/alphabravocompany/astronomer-go/internal/helmruntime"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// HelmRuntimeConfig is the typed Helm SDK process contract accepted by the
// agent composition root.
type HelmRuntimeConfig = helmruntime.Config

// HelmHandler processes Helm operations received through the tunnel.
type HelmHandler struct {
	settings *cli.EnvSettings
	driver   string
	log      *slog.Logger
	runtime  helmruntime.Config
}

// NewHelmHandler creates a new Helm operations handler.
//
// The caller captures Helm's process contract at the configuration boundary.
// Writable /tmp defaults keep the non-root, read-only-root agent viable.
func NewHelmHandler(log *slog.Logger, runtime helmruntime.Config) *HelmHandler {
	return &HelmHandler{
		settings: runtime.Settings(),
		driver:   runtime.Driver,
		log:      log,
		runtime:  runtime,
	}
}

// actionConfig creates a new Helm action configuration for the given namespace.
func (h *HelmHandler) actionConfig(ctx context.Context, namespace string) (*action.Configuration, error) {
	cfg := action.NewConfiguration(action.ConfigurationSetLogger(h.log.With("component", "helm").Handler()))
	settings := helmSettingsForNamespace(h.settings, namespace)
	getter, err := helmruntime.RequestClientGetter(ctx, settings.RESTClientGetter())
	if err != nil {
		return nil, err
	}
	if err = cfg.Init(getter, namespace, h.driver); err != nil {
		return nil, fmt.Errorf("init helm config: %w", err)
	}
	if client, ok := cfg.KubeClient.(*kube.Client); ok {
		client.WaitContext = ctx
	} else {
		return nil, fmt.Errorf("unsupported Helm Kubernetes wait client")
	}
	return cfg, nil
}

// helmSettingsForNamespace returns a request-local settings copy. Helm's
// action configuration uses the namespace argument for release storage, while
// its Kubernetes client gets the default namespace from EnvSettings. Without
// setting both, namespace-implicit chart resources such as hooks are created
// in the agent pod's own namespace instead of the requested release namespace.
func helmSettingsForNamespace(base *cli.EnvSettings, namespace string) *cli.EnvSettings {
	settings := cli.New()
	settings.KubeConfig = base.KubeConfig
	settings.KubeContext = base.KubeContext
	settings.KubeToken = base.KubeToken
	settings.KubeAsUser = base.KubeAsUser
	settings.KubeAsGroups = append([]string(nil), base.KubeAsGroups...)
	settings.KubeAPIServer = base.KubeAPIServer
	settings.KubeCaFile = base.KubeCaFile
	settings.KubeInsecureSkipTLSVerify = base.KubeInsecureSkipTLSVerify
	settings.KubeTLSServerName = base.KubeTLSServerName
	settings.Debug = base.Debug
	settings.RegistryConfig = base.RegistryConfig
	settings.RepositoryConfig = base.RepositoryConfig
	settings.RepositoryCache = base.RepositoryCache
	settings.ContentCache = base.ContentCache
	settings.ColorMode = base.ColorMode
	settings.PluginsDirectory = base.PluginsDirectory
	settings.MaxHistory = base.MaxHistory
	settings.BurstLimit = base.BurstLimit
	settings.QPS = base.QPS
	settings.SetNamespace(namespace)
	return settings
}

// helmResult constructs a HELM_RESULT response message.
// defaultHelmReadyTimeout bounds how long an install/upgrade waits for
// workloads to become Ready before giving up. Matches the helm CLI default.
const defaultHelmReadyTimeout = 5 * time.Minute

// helmReadyTimeout returns the wait timeout for an install/upgrade, honoring
// the caller-provided seconds and falling back to the helm CLI default.
func helmReadyTimeout(seconds int) time.Duration {
	if seconds > 0 {
		if int64(seconds) > int64(time.Duration(1<<63-1)/time.Second) {
			return 0 // The operation owner rejects unrepresentable arithmetic.
		}
		return time.Duration(seconds) * time.Second
	}
	return defaultHelmReadyTimeout
}

func helmOperationContext(parent context.Context, seconds int) (context.Context, context.CancelFunc, error) {
	timeout := helmReadyTimeout(seconds)
	if timeout == 0 {
		return nil, nil, errors.New("Helm timeout cannot be represented")
	}
	return helmruntime.OperationContext(parent, timeout)
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
func (h *HelmHandler) locateChart(ctx context.Context, req *protocol.HelmRequestPayload) (string, error) {
	// If a direct chart URL is provided, use it.
	if req.ChartURL != "" {
		if helmruntime.HTTPRepositoryReference(req.ChartURL) {
			return helmruntime.LocateRepositoryChart(ctx, h.runtime, req.ChartURL, "", req.Version)
		}
		return req.ChartURL, nil
	}

	// Compose the lookup name: an OCI repo URL + chart name yields a single
	// ref like oci://ghcr.io/example/platform/chart. Traditional Helm
	// repos use the chart-name-only form and rely on RepoURL/index.yaml.
	chartName := req.ChartName
	repoURL := strings.TrimSpace(req.RepoURL)
	isOCI := strings.HasPrefix(strings.ToLower(repoURL), "oci://") ||
		strings.HasPrefix(strings.ToLower(chartName), "oci://")
	if isOCI && !strings.HasPrefix(strings.ToLower(chartName), "oci://") && repoURL != "" {
		chartName = strings.TrimRight(repoURL, "/") + "/" + strings.TrimLeft(req.ChartName, "/")
	}
	if !isOCI {
		return helmruntime.LocateRepositoryChart(ctx, h.runtime, chartName, repoURL, req.Version)
	}

	// Pass a non-nil *action.Configuration. The SDK dereferences cfg fields on
	// some chart-resolution paths, so action.NewInstall(nil) panics with a
	// nil pointer in LocateChart. We don't need a fully-initialized
	// configuration to locate a chart — an empty struct is enough.
	install := action.NewInstall(&action.Configuration{})
	if isOCI {
		httpClient, err := helmruntime.HTTPClient(ctx, &http.Client{Transport: registry.NewTransport(false)})
		if err != nil {
			return "", err
		}
		defer httpClient.CloseIdleConnections()
		rc, err := registry.NewClient(registry.ClientOptHTTPClient(httpClient), registry.ClientOptCredentialsFile(h.settings.RegistryConfig))
		if err != nil {
			return "", fmt.Errorf("init OCI registry client: %w", err)
		}
		install.SetRegistryClient(rc)
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

// helmUpgradeUsesStoredChart is true when the caller omitted chart coordinates
// and the upgrade should load the chart from the last release (overlay
// --reuse-values of an already-installed chart such as astronomer).
func helmUpgradeUsesStoredChart(req *protocol.HelmRequestPayload) bool {
	if req == nil {
		return false
	}
	return strings.TrimSpace(req.ChartName) == "" && strings.TrimSpace(req.ChartURL) == "" && strings.TrimSpace(req.RepoURL) == ""
}

func (h *HelmHandler) loadUpgradeChart(ctx context.Context, cfg *action.Configuration, req *protocol.HelmRequestPayload) (*helmchart.Chart, error) {
	if helmUpgradeUsesStoredChart(req) {
		if cfg == nil || cfg.Releases == nil {
			return nil, fmt.Errorf("helm storage is not configured")
		}
		last, err := cfg.Releases.Last(req.ReleaseName)
		if err != nil {
			return nil, fmt.Errorf("load last release chart: %w", err)
		}
		release, err := helmruntime.ReleaseV1(last)
		if err != nil {
			return nil, err
		}
		if release.Chart == nil {
			return nil, fmt.Errorf("helm release %q has no chart to reuse", req.ReleaseName)
		}
		return release.Chart, nil
	}
	chartPath, err := h.locateChart(ctx, req)
	if err != nil {
		return nil, err
	}
	chrt, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("load chart: %w", err)
	}
	return chrt, nil
}

// HandleInstall processes HELM_INSTALL messages.
func (h *HelmHandler) HandleInstall(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	req, err := decodeHelmRequest(msg)
	if err != nil {
		return helmResult(msg.StreamID, "", "", "", 0, err), nil
	}

	ctx, cancel, err := helmOperationContext(ctx, req.Timeout)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}
	defer cancel()

	h.log.Info("helm install", "release", req.ReleaseName, "namespace", req.Namespace)

	cfg, err := h.actionConfig(ctx, req.Namespace)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	install := action.NewInstall(cfg)
	install.Description = req.Description
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
	install.WaitStrategy = kube.LegacyStrategy
	install.WaitOptions = []kube.WaitOption{kube.WithWaitContext(ctx)}
	install.ServerSideApply = false
	install.Timeout = helmReadyTimeout(req.Timeout)

	chartPath, err := h.locateChart(ctx, req)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0,
			fmt.Errorf("load chart: %w", err)), nil
	}

	// The action must finish its worker before this owner is released. Helm4
	// returns early for a canceled control context, so only bound transport
	// and waits observe owner cancellation; the control context stays joined.
	rel, err := helmruntime.RunJoined(ctx, func(control context.Context) (releasei.Releaser, error) {
		return install.RunWithContext(control, chart, req.Values)
	})
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	return helmResultFromRelease(msg.StreamID, req, rel), nil
}

// HandleUpgrade processes HELM_UPGRADE messages.
func (h *HelmHandler) HandleUpgrade(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	req, err := decodeHelmRequest(msg)
	if err != nil {
		return helmResult(msg.StreamID, "", "", "", 0, err), nil
	}

	ctx, cancel, err := helmOperationContext(ctx, req.Timeout)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}
	defer cancel()

	h.log.Info("helm upgrade", "release", req.ReleaseName, "namespace", req.Namespace)

	cfg, err := h.actionConfig(ctx, req.Namespace)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	upgrade := action.NewUpgrade(cfg)
	upgrade.Description = req.Description
	upgrade.Namespace = req.Namespace
	upgrade.ReuseValues = req.ReuseValues
	if req.Version != "" {
		upgrade.Version = req.Version
	}
	// See HandleInstall: block until workloads are Ready so "deployed"
	// implies readiness instead of just "manifests applied".
	upgrade.WaitStrategy = kube.LegacyStrategy
	upgrade.WaitOptions = []kube.WaitOption{kube.WithWaitContext(ctx)}
	upgrade.ServerSideApply = "false"
	upgrade.Timeout = helmReadyTimeout(req.Timeout)

	chrt, err := h.loadUpgradeChart(ctx, cfg, req)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	rel, err := helmruntime.RunJoined(ctx, func(control context.Context) (releasei.Releaser, error) {
		return upgrade.RunWithContext(control, req.ReleaseName, chrt, req.Values)
	})
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	return helmResultFromRelease(msg.StreamID, req, rel), nil
}

// HandleUninstall processes HELM_UNINSTALL messages.
func (h *HelmHandler) HandleUninstall(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	req, err := decodeHelmRequest(msg)
	if err != nil {
		return helmResult(msg.StreamID, "", "", "", 0, err), nil
	}

	ctx, cancel, err := helmOperationContext(ctx, req.Timeout)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}
	defer cancel()

	h.log.Info("helm uninstall", "release", req.ReleaseName, "namespace", req.Namespace)

	cfg, err := h.actionConfig(ctx, req.Namespace)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	uninstall := action.NewUninstall(cfg)
	uninstall.WaitStrategy = kube.HookOnlyStrategy
	uninstall.WaitOptions = []kube.WaitOption{kube.WithWaitContext(ctx)}
	if req.Timeout > 0 {
		uninstall.Timeout = helmReadyTimeout(req.Timeout)
	}

	resp, err := uninstall.Run(req.ReleaseName)
	err = errors.Join(err, ctx.Err())
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	status := ""
	if resp.Release != nil {
		release, err := helmruntime.ReleaseV1(resp.Release)
		if err != nil {
			return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
		}
		status = release.Info.Status.String()
	}

	return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, status, 0, nil), nil
}

// HandleRollback processes HELM_ROLLBACK messages.
func (h *HelmHandler) HandleRollback(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	req, err := decodeHelmRequest(msg)
	if err != nil {
		return helmResult(msg.StreamID, "", "", "", 0, err), nil
	}

	ctx, cancel, err := helmOperationContext(ctx, req.Timeout)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}
	defer cancel()

	h.log.Info("helm rollback", "release", req.ReleaseName, "namespace", req.Namespace, "revision", req.Revision)

	cfg, err := h.actionConfig(ctx, req.Namespace)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	// Tag the revision at its first durable write, before Kubernetes changes.
	// This closes the crash window between a successful rollback and the
	// management plane recording its checkpoint.
	if req.Description != "" {
		cfg.Releases.Driver = &helmOperationDriver{Driver: cfg.Releases.Driver, description: req.Description}
	}
	rollback := action.NewRollback(cfg)
	rollback.Version = req.Revision
	rollback.WaitStrategy = kube.LegacyStrategy
	rollback.WaitOptions = []kube.WaitOption{kube.WithWaitContext(ctx)}
	rollback.ServerSideApply = "false"
	rollback.Timeout = helmReadyTimeout(req.Timeout)

	err = rollback.Run(req.ReleaseName)
	err = errors.Join(err, ctx.Err())
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", req.Revision, err), nil
	}

	current, err := cfg.Releases.Last(req.ReleaseName)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}
	return helmResultFromRelease(msg.StreamID, req, current), nil
}

type helmOperationDriver struct {
	driver.Driver
	description string
}

func (d *helmOperationDriver) Create(key string, value releasei.Releaser) error {
	rel, err := helmruntime.ReleaseV1(value)
	if err != nil {
		return err
	}
	if rel.Info != nil {
		rel.Info.Description = d.description
	}
	return d.Driver.Create(key, rel)
}

// HandleStatus processes HELM_STATUS messages.
func (h *HelmHandler) HandleStatus(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	req, err := decodeHelmRequest(msg)
	if err != nil {
		return helmResult(msg.StreamID, "", "", "", 0, err), nil
	}

	ctx, cancel, err := helmOperationContext(ctx, req.Timeout)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}
	defer cancel()

	h.log.Info("helm status", "release", req.ReleaseName, "namespace", req.Namespace)

	cfg, err := h.actionConfig(ctx, req.Namespace)
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	status := action.NewStatus(cfg)
	rel, err := status.Run(req.ReleaseName)
	err = errors.Join(err, ctx.Err())
	if err != nil {
		return helmResult(msg.StreamID, req.ReleaseName, req.Namespace, "", 0, err), nil
	}

	return helmResultFromRelease(msg.StreamID, req, rel), nil
}

// HandleHistory processes HELM_HISTORY messages (DIR-12).
func (h *HelmHandler) HandleHistory(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	req, err := decodeHelmRequest(msg)
	if err != nil {
		return helmHistoryResult(msg.StreamID, "", "", nil, err), nil
	}

	ctx, cancel, err := helmOperationContext(ctx, req.Timeout)
	if err != nil {
		return helmHistoryResult(msg.StreamID, req.ReleaseName, req.Namespace, nil, err), nil
	}
	defer cancel()

	h.log.Info("helm history", "release", req.ReleaseName, "namespace", req.Namespace)

	cfg, err := h.actionConfig(ctx, req.Namespace)
	if err != nil {
		return helmHistoryResult(msg.StreamID, req.ReleaseName, req.Namespace, nil, err), nil
	}

	hist := action.NewHistory(cfg)
	hist.Max = 256
	rels, err := hist.Run(req.ReleaseName)
	err = errors.Join(err, ctx.Err())
	if err != nil {
		return helmHistoryResult(msg.StreamID, req.ReleaseName, req.Namespace, nil, err), nil
	}
	revs := make([]protocol.HelmRevision, 0, len(rels))
	for _, value := range rels {
		rel, err := helmruntime.ReleaseV1(value)
		if errors.Is(err, helmruntime.ErrReleaseMetadataUnavailable) {
			continue // Preserve the existing missing-info history rule.
		}
		if err != nil {
			return helmHistoryResult(msg.StreamID, req.ReleaseName, req.Namespace, nil, err), nil
		}
		entry := protocol.HelmRevision{
			Revision:    rel.Version,
			Updated:     rel.Info.LastDeployed.UTC().Format(time.RFC3339),
			Status:      rel.Info.Status.String(),
			Description: rel.Info.Description,
		}
		if rel.Chart != nil && rel.Chart.Metadata != nil {
			entry.Chart = rel.Chart.Metadata.Name + "-" + rel.Chart.Metadata.Version
			entry.AppVersion = rel.Chart.Metadata.AppVersion
		}
		revs = append(revs, entry)
	}
	return helmHistoryResult(msg.StreamID, req.ReleaseName, req.Namespace, revs, nil), nil
}

func helmHistoryResult(streamID, releaseName, namespace string, revs []protocol.HelmRevision, err error) *protocol.Message {
	result := protocol.HelmResultPayload{
		Success:     err == nil,
		ReleaseName: releaseName,
		Namespace:   namespace,
		Revisions:   revs,
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

func helmResultFromRelease(streamID string, req *protocol.HelmRequestPayload, value releasei.Releaser) *protocol.Message {
	rel, err := helmruntime.ReleaseV1(value)
	if err != nil {
		return helmResult(streamID, req.ReleaseName, req.Namespace, "", 0, err)
	}
	return helmResult(streamID, rel.Name, rel.Namespace, rel.Info.Status.String(), rel.Version, nil)
}
