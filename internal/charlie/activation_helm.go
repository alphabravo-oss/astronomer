package charlie

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/registry"
	"helm.sh/helm/v4/pkg/release"

	"github.com/alphabravocompany/astronomer-go/internal/helmruntime"
)

type InClusterHelmReleaser struct {
	namespace string
	runtime   helmruntime.Config
}

func NewInClusterHelmReleaser(namespace string, runtime helmruntime.Config) *InClusterHelmReleaser {
	if strings.TrimSpace(namespace) == "" {
		return nil
	}
	runtime.Namespace = namespace
	return &InClusterHelmReleaser{namespace: namespace, runtime: runtime}
}

func (r *InClusterHelmReleaser) helmConfig(ctx context.Context) (*cli.EnvSettings, *action.Configuration, error) {
	if r == nil {
		return nil, nil, fmt.Errorf("Charlie agent Helm installer is unavailable")
	}
	settings := r.runtime.Settings()
	settings.SetNamespace(r.namespace)
	cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)))
	getter, err := helmruntime.RequestClientGetter(ctx, settings.RESTClientGetter())
	if err != nil {
		return nil, nil, err
	}
	if err := cfg.Init(getter, r.namespace, r.runtime.Driver); err != nil {
		return nil, nil, fmt.Errorf("init Charlie agent Helm: %w", err)
	}
	if client, ok := cfg.KubeClient.(*kube.Client); ok {
		client.WaitContext = ctx
	} else {
		return nil, nil, fmt.Errorf("unsupported Charlie Helm wait client")
	}
	return settings, cfg, nil
}

func (r *InClusterHelmReleaser) registryClient(ctx context.Context, spec HelmReleaseSpec) (*registry.Client, func(), error) {
	creds := r.runtime.Settings().RegistryConfig
	if err := os.MkdirAll(filepath.Dir(creds), 0o700); err != nil {
		return nil, nil, fmt.Errorf("Charlie agent registry config: %w", err)
	}
	httpClient, err := helmruntime.HTTPClient(ctx, &http.Client{Transport: registry.NewTransport(false)})
	if err != nil {
		return nil, nil, err
	}
	client, err := registry.NewClient(registry.ClientOptCredentialsFile(creds), registry.ClientOptWriter(io.Discard), registry.ClientOptHTTPClient(httpClient))
	if err != nil {
		httpClient.CloseIdleConnections()
		return nil, nil, fmt.Errorf("Charlie agent registry client: %w", err)
	}
	if strings.TrimSpace(spec.PullSecret) == "" {
		return client, httpClient.CloseIdleConnections, nil
	}
	host := strings.TrimPrefix(strings.TrimPrefix(spec.ChartRef, "oci://"), "https://")
	host, _, _ = strings.Cut(host, "/")
	if err := client.Login(host, registry.LoginOptBasicAuth("charlie", spec.PullSecret)); err != nil {
		httpClient.CloseIdleConnections()
		return nil, nil, fmt.Errorf("Charlie agent registry login: %w", err)
	}
	return client, httpClient.CloseIdleConnections, nil
}

func (r *InClusterHelmReleaser) Apply(ctx context.Context, spec HelmReleaseSpec) error {
	ctx, cancel, err := helmruntime.OperationContext(ctx, 5*time.Minute)
	if err != nil {
		return err
	}
	defer cancel()
	settings, cfg, err := r.helmConfig(ctx)
	if err != nil {
		return err
	}
	client, closeClient, err := r.registryClient(ctx, spec)
	if err != nil {
		return err
	}
	defer closeClient()
	cfg.RegistryClient = client
	hist := action.NewHistory(cfg)
	hist.Max = 1
	_, histErr := hist.Run(agentReleaseName)
	chartRef := spec.ChartRef
	if spec.ChartDigest != "" && !strings.Contains(chartRef, "@") {
		chartRef = strings.TrimSuffix(chartRef, "/") + "@" + spec.ChartDigest
	}
	if histErr != nil {
		install := action.NewInstall(cfg)
		install.ReleaseName = agentReleaseName
		install.Namespace = r.namespace
		install.CreateNamespace = true
		install.WaitStrategy = kube.HookOnlyStrategy
		install.WaitOptions = []kube.WaitOption{kube.WithWaitContext(ctx)}
		install.ServerSideApply = false
		install.RollbackOnFailure = false
		install.Timeout = 5 * time.Minute
		install.SetRegistryClient(client)
		path, err := r.locateChart(ctx, &install.ChartPathOptions, chartRef, settings)
		if err != nil {
			return fmt.Errorf("locate Charlie agent chart: %w", err)
		}
		chart, err := loader.Load(path)
		if err != nil {
			return fmt.Errorf("load Charlie agent chart: %w", err)
		}
		// Keep the SDK control call joined; only its owned transport/waits
		// receive cancellation until the worker has actually finished.
		_, err = helmruntime.RunJoined(ctx, func(control context.Context) (release.Releaser, error) {
			return install.RunWithContext(control, chart, spec.Values)
		})
		return err
	}
	upgrade := action.NewUpgrade(cfg)
	upgrade.Namespace = r.namespace
	upgrade.WaitStrategy = kube.HookOnlyStrategy
	upgrade.WaitOptions = []kube.WaitOption{kube.WithWaitContext(ctx)}
	upgrade.ServerSideApply = "false"
	upgrade.ReuseValues = spec.ReuseValues
	upgrade.Timeout = 5 * time.Minute
	upgrade.SetRegistryClient(client)
	path, err := r.locateChart(ctx, &upgrade.ChartPathOptions, chartRef, settings)
	if err != nil {
		return fmt.Errorf("locate Charlie agent chart: %w", err)
	}
	chart, err := loader.Load(path)
	if err != nil {
		return fmt.Errorf("load Charlie agent chart: %w", err)
	}
	_, err = helmruntime.RunJoined(ctx, func(control context.Context) (release.Releaser, error) {
		return upgrade.RunWithContext(control, agentReleaseName, chart, spec.Values)
	})
	return err
}

func (r *InClusterHelmReleaser) locateChart(ctx context.Context, options *action.ChartPathOptions, chartRef string, settings *cli.EnvSettings) (string, error) {
	if registry.IsOCI(chartRef) {
		return options.LocateChart(chartRef, settings)
	}
	return helmruntime.LocateRepositoryChart(ctx, r.runtime, chartRef, options.RepoURL, options.Version)
}

func (r *InClusterHelmReleaser) Uninstall(ctx context.Context) error {
	ctx, cancel, err := helmruntime.OperationContext(ctx, 5*time.Minute)
	if err != nil {
		return err
	}
	defer cancel()
	_, cfg, err := r.helmConfig(ctx)
	if err != nil {
		return err
	}
	uninstall := action.NewUninstall(cfg)
	uninstall.WaitStrategy = kube.HookOnlyStrategy
	uninstall.WaitOptions = []kube.WaitOption{kube.WithWaitContext(ctx)}
	uninstall.IgnoreNotFound = true
	uninstall.Timeout = 5 * time.Minute
	_, err = uninstall.Run(agentReleaseName)
	if ctx.Err() != nil {
		return errors.Join(err, ctx.Err())
	}
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "not found") {
		return nil
	}
	return err
}
