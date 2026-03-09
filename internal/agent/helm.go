package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/repo"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// HelmHandler processes Helm operations received through the tunnel.
type HelmHandler struct {
	settings *cli.EnvSettings
	log      *slog.Logger
}

// NewHelmHandler creates a new Helm operations handler.
func NewHelmHandler(log *slog.Logger) *HelmHandler {
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
func (h *HelmHandler) locateChart(req *protocol.HelmRequestPayload) (string, error) {
	// If a direct chart URL is provided, use it.
	if req.ChartURL != "" {
		return req.ChartURL, nil
	}

	// If a repo URL is provided, add a temporary entry and resolve.
	if req.RepoURL != "" {
		entry := &repo.Entry{
			Name: "astronomer-tmp",
			URL:  req.RepoURL,
		}
		cr, err := repo.NewChartRepository(entry, getter.All(h.settings))
		if err != nil {
			return "", fmt.Errorf("create chart repo: %w", err)
		}
		if _, err := cr.DownloadIndexFile(); err != nil {
			return "", fmt.Errorf("download repo index: %w", err)
		}
	}

	cp, err := action.NewInstall(nil).ChartPathOptions.LocateChart(req.ChartName, h.settings)
	if err != nil {
		return "", fmt.Errorf("locate chart %s: %w", req.ChartName, err)
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
	if req.Timeout > 0 {
		install.Timeout = time.Duration(req.Timeout) * time.Second
	}

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
	if req.Timeout > 0 {
		upgrade.Timeout = time.Duration(req.Timeout) * time.Second
	}

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
