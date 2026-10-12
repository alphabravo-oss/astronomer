package helmruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/downloader"
	"helm.sh/helm/v4/pkg/getter"
	"helm.sh/helm/v4/pkg/helmpath"
	repo "helm.sh/helm/v4/pkg/repo/v1"
)

// LocateRepositoryChart retains Helm's local path, repository index/version,
// and content-cache behavior while replacing only HTTP transfer ownership.
// OCI callers use their separately owned registry client. This adapter accepts
// no request-supplied credentials, TLS settings, or destination authority.
func LocateRepositoryChart(ctx context.Context, runtime Config, name, repositoryURL, version string) (string, error) {
	if ctx == nil {
		return "", errors.New("Helm chart context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name, repositoryURL, version = strings.TrimSpace(name), strings.TrimSpace(repositoryURL), strings.TrimSpace(version)
	if repositoryURL == "" {
		if _, err := os.Stat(name); err == nil {
			return filepath.Abs(name)
		}
		if filepath.IsAbs(name) || strings.HasPrefix(name, ".") {
			return "", errors.New("Helm chart path was not found")
		}
	}
	settings := runtime.Settings()
	owner, err := chartRepositoryOwner(ctx, settings, name, repositoryURL)
	if err != nil {
		return "", chartLookupError(ctx, err)
	}
	transfer := repositoryGetter{ctx: ctx, runtime: runtime, owner: owner}
	providers := getter.Providers{{Schemes: []string{"http", "https"}, New: func(...getter.Option) (getter.Getter, error) { return &transfer, nil }}}
	if repositoryURL != "" {
		name, err = chartFromRepositoryIndex(ctx, settings, providers, name, repositoryURL, version)
		if err != nil {
			return "", chartLookupError(ctx, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// The getter's operator-owned entry snapshot supplies auth/TLS, not opaque
	// getter options generated from potentially changing repository files.
	download := downloader.ChartDownloader{
		Out: io.Discard, Getters: providers, RepositoryConfig: settings.RepositoryConfig,
		RepositoryCache: settings.RepositoryCache, ContentCache: settings.ContentCache,
	}
	path, _, err := download.DownloadToCache(name, version)
	if err != nil {
		return "", chartLookupError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

func chartFromRepositoryIndex(ctx context.Context, settings *cli.EnvSettings, providers getter.Providers, name, repositoryURL, version string) (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	entry := &repo.Entry{Name: "astronomer-owned-" + hex.EncodeToString(nonce[:]), URL: repositoryURL}
	collection, err := repo.NewChartRepository(entry, providers)
	if err != nil {
		return "", err
	}
	// Helm's standard FindChartInRepoURL chooses ambient cache paths. Explicit
	// CachePath keeps the same index parser/selection inside injected settings.
	collection.CachePath = settings.RepositoryCache
	defer func() {
		_ = os.Remove(filepath.Join(collection.CachePath, helmpath.CacheChartsFile(entry.Name)))
		_ = os.Remove(filepath.Join(collection.CachePath, helmpath.CacheIndexFile(entry.Name)))
	}()
	path, err := collection.DownloadIndexFile()
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	index, err := repo.LoadIndexFile(path)
	if err != nil {
		return "", err
	}
	chart, err := index.Get(name, version)
	if err != nil || chart == nil || len(chart.URLs) == 0 {
		return "", errors.New("Helm chart version was not found")
	}
	return repo.ResolveReferenceURL(repositoryURL, chart.URLs[0])
}

func chartLookupError(ctx context.Context, err error) error {
	if ownerErr := ctx.Err(); ownerErr != nil {
		return ownerErr
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return errors.New("Helm chart lookup failed")
}
