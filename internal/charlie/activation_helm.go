package charlie

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"
)

type InClusterHelmReleaser struct {
	namespace string
}

func NewInClusterHelmReleaser(namespace string) *InClusterHelmReleaser {
	if strings.TrimSpace(namespace) == "" {
		return nil
	}
	return &InClusterHelmReleaser{namespace: namespace}
}

func (r *InClusterHelmReleaser) helmConfig() (*cli.EnvSettings, *action.Configuration, error) {
	if r == nil {
		return nil, nil, fmt.Errorf("Charlie agent Helm installer is unavailable")
	}
	for key, value := range map[string]string{
		"HELM_CACHE_HOME": "/tmp/helm/cache", "HELM_CONFIG_HOME": "/tmp/helm/config", "HELM_DATA_HOME": "/tmp/helm/data",
		"HELM_REGISTRY_CONFIG": "/tmp/helm/config/registry/config.json",
	} {
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
	settings := cli.New()
	settings.SetNamespace(r.namespace)
	cfg := new(action.Configuration)
	if err := cfg.Init(settings.RESTClientGetter(), r.namespace, os.Getenv("HELM_DRIVER"), func(string, ...interface{}) {}); err != nil {
		return nil, nil, fmt.Errorf("init Charlie agent Helm: %w", err)
	}
	return settings, cfg, nil
}

func (r *InClusterHelmReleaser) registryClient(spec HelmReleaseSpec) (*registry.Client, error) {
	creds := os.Getenv("HELM_REGISTRY_CONFIG")
	if creds == "" {
		creds = "/tmp/helm/config/registry/config.json"
	}
	if err := os.MkdirAll(filepath.Dir(creds), 0o700); err != nil {
		return nil, fmt.Errorf("Charlie agent registry config: %w", err)
	}
	client, err := registry.NewClient(registry.ClientOptCredentialsFile(creds), registry.ClientOptWriter(io.Discard))
	if err != nil {
		return nil, fmt.Errorf("Charlie agent registry client: %w", err)
	}
	if strings.TrimSpace(spec.PullSecret) == "" {
		return client, nil
	}
	host := strings.TrimPrefix(strings.TrimPrefix(spec.ChartRef, "oci://"), "https://")
	host, _, _ = strings.Cut(host, "/")
	if err := client.Login(host, registry.LoginOptBasicAuth("charlie", spec.PullSecret)); err != nil {
		return nil, fmt.Errorf("Charlie agent registry login: %w", err)
	}
	return client, nil
}

func (r *InClusterHelmReleaser) Apply(ctx context.Context, spec HelmReleaseSpec) error {
	settings, cfg, err := r.helmConfig()
	if err != nil {
		return err
	}
	client, err := r.registryClient(spec)
	if err != nil {
		return err
	}
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
		install.Wait = false
		install.Atomic = false
		install.SetRegistryClient(client)
		path, err := install.LocateChart(chartRef, settings)
		if err != nil {
			return fmt.Errorf("locate Charlie agent chart: %w", err)
		}
		chart, err := loader.Load(path)
		if err != nil {
			return fmt.Errorf("load Charlie agent chart: %w", err)
		}
		_, err = install.RunWithContext(ctx, chart, spec.Values)
		return err
	}
	upgrade := action.NewUpgrade(cfg)
	upgrade.Namespace = r.namespace
	upgrade.Wait = false
	upgrade.ReuseValues = spec.ReuseValues
	upgrade.SetRegistryClient(client)
	path, err := upgrade.LocateChart(chartRef, settings)
	if err != nil {
		return fmt.Errorf("locate Charlie agent chart: %w", err)
	}
	chart, err := loader.Load(path)
	if err != nil {
		return fmt.Errorf("load Charlie agent chart: %w", err)
	}
	_, err = upgrade.RunWithContext(ctx, agentReleaseName, chart, spec.Values)
	return err
}

func (r *InClusterHelmReleaser) Uninstall(context.Context) error {
	_, cfg, err := r.helmConfig()
	if err != nil {
		return err
	}
	uninstall := action.NewUninstall(cfg)
	uninstall.Wait = false
	uninstall.IgnoreNotFound = true
	_, err = uninstall.Run(agentReleaseName)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "not found") {
		return nil
	}
	return err
}
