package helmruntime

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/helmpath"
	repo "helm.sh/helm/v4/pkg/repo/v1"
)

// The operator's repository file is a credential/TLS snapshot, independently
// of destination permission. Direct URL ownership follows Helm's cached-index
// first-match rule; a URL alone never invents a repository credential grant.
func chartRepositoryOwner(ctx context.Context, settings *cli.EnvSettings, name, repositoryURL string) (*repo.Entry, error) {
	file, err := repo.LoadFile(settings.RepositoryConfig)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if file == nil {
		file = repo.NewFile()
	}
	if repositoryURL != "" {
		for _, entry := range file.Repositories {
			if entry != nil && equalRepositoryURL(entry.URL, repositoryURL) {
				copy := *entry
				return &copy, nil
			}
		}
		return nil, nil
	}
	value, err := url.Parse(name)
	if err != nil {
		return nil, err
	}
	if value.IsAbs() && value.Host != "" && value.Path != "" {
		for _, entry := range file.Repositories {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if entry == nil {
				return nil, errors.New("Helm repository entry is invalid")
			}
			index, err := repo.LoadIndexFile(filepath.Join(settings.RepositoryCache, helmpath.CacheIndexFile(entry.Name)))
			if err != nil {
				return nil, err
			}
			for _, versions := range index.Entries {
				for _, chart := range versions {
					if chart == nil {
						continue
					}
					for _, candidate := range chart.URLs {
						if equalRepositoryURL(name, candidate) {
							copy := *entry
							return &copy, nil
						}
					}
				}
			}
		}
		return nil, nil
	}
	named, _, found := strings.Cut(value.Path, "/")
	if !found {
		return nil, errors.New("Helm chart requires a named repository")
	}
	for _, entry := range file.Repositories {
		if entry != nil && entry.Name == named && entry.URL != "" {
			copy := *entry
			return &copy, nil
		}
	}
	return nil, errors.New("Helm named repository was not found")
}

func equalRepositoryURL(left, right string) bool {
	a, aerr := url.Parse(left)
	b, berr := url.Parse(right)
	return aerr == nil && berr == nil && strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host) &&
		a.Path == b.Path && a.RawPath == b.RawPath && a.RawQuery == b.RawQuery && a.Fragment == b.Fragment && a.User == nil && b.User == nil
}

func sameRepositoryOrigin(left, right string) bool {
	a, aerr := url.Parse(left)
	b, berr := url.Parse(right)
	return aerr == nil && berr == nil && a.Scheme != "" && strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host) && a.User == nil && b.User == nil
}
