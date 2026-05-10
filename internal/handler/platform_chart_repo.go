package handler

import (
	"net/http"

	deployassets "github.com/alphabravocompany/astronomer-go/deploy"
)

// PlatformChartRepoHandler serves the embedded Astronomer chart as a tiny
// in-process Helm repository so local ArgoCD can reconcile the management
// plane from a stable in-cluster source.
type PlatformChartRepoHandler struct {
	repo *deployassets.HelmChartRepo
}

func NewPlatformChartRepoHandler() (*PlatformChartRepoHandler, error) {
	repo, err := deployassets.AstronomerChartRepo()
	if err != nil {
		return nil, err
	}
	return &PlatformChartRepoHandler{repo: repo}, nil
}

func (h *PlatformChartRepoHandler) ServeIndex(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil {
		http.Error(w, "chart repo unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(h.repo.IndexYAML())
}

func (h *PlatformChartRepoHandler) ServeArchive(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil {
		http.Error(w, "chart repo unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+h.repo.ArchiveName()+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(h.repo.ArchiveTGZ())
}

func (h *PlatformChartRepoHandler) ArchiveName() string {
	if h == nil || h.repo == nil {
		return ""
	}
	return h.repo.ArchiveName()
}
