package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"sigs.k8s.io/yaml"
)

// Keep packaged dependencies in the embedded repository. Argo fetches this
// archive without network access, so omitting chart/charts would make the
// declared argo-cd dependency impossible to render offline.
//
//go:embed chart/Chart.yaml chart/Chart.lock chart/DEPENDENCIES.md chart/README.md chart/values.yaml chart/values.schema.json chart/templates/* chart/charts/* chart/licenses/*
var chartFS embed.FS

type chartMetadata struct {
	APIVersion  string `yaml:"apiVersion"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Type        string `yaml:"type"`
	Version     string `yaml:"version"`
	AppVersion  string `yaml:"appVersion"`
}

// HelmChartRepo is an in-memory single-chart Helm repository.
type HelmChartRepo struct {
	name        string
	version     string
	archiveName string
	indexYAML   []byte
	archiveTGZ  []byte
}

func (r *HelmChartRepo) Name() string        { return r.name }
func (r *HelmChartRepo) Version() string     { return r.version }
func (r *HelmChartRepo) ArchiveName() string { return r.archiveName }
func (r *HelmChartRepo) IndexYAML() []byte   { return append([]byte(nil), r.indexYAML...) }
func (r *HelmChartRepo) ArchiveTGZ() []byte  { return append([]byte(nil), r.archiveTGZ...) }

var (
	astronomerRepoOnce sync.Once
	astronomerRepo     *HelmChartRepo
	astronomerRepoErr  error
)

// AstronomerChartRepo returns a packaged in-memory Helm repo for the embedded
// Astronomer chart under deploy/chart.
func AstronomerChartRepo() (*HelmChartRepo, error) {
	astronomerRepoOnce.Do(func() {
		astronomerRepo, astronomerRepoErr = buildAstronomerChartRepo()
	})
	return astronomerRepo, astronomerRepoErr
}

// AstronomerDefaultValuesShape returns a fresh copy of the chart's declared
// values tree. Self-management uses it as a closed path/type vocabulary: Helm
// release values containing unknown paths are not safe to persist in Argo.
func AstronomerDefaultValuesShape() (map[string]any, error) {
	raw, err := chartFS.ReadFile("chart/values.yaml")
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	if err := yaml.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("parse embedded default values: %w", err)
	}
	dependency, err := chartFS.ReadFile("chart/charts/argo-cd-9.5.21.tgz")
	if err != nil {
		return nil, fmt.Errorf("read pinned argo-cd values shape: %w", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(dependency))
	if err != nil {
		return nil, fmt.Errorf("open pinned argo-cd chart: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	argoDefaults := map[string]any{}
	for {
		hdr, nextErr := tr.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, fmt.Errorf("read pinned argo-cd chart: %w", nextErr)
		}
		if hdr.Name != "argo-cd/values.yaml" {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read pinned argo-cd values: %w", err)
		}
		if err := yaml.Unmarshal(data, &argoDefaults); err != nil {
			return nil, fmt.Errorf("parse pinned argo-cd values: %w", err)
		}
		break
	}
	if len(argoDefaults) == 0 {
		return nil, fmt.Errorf("pinned argo-cd chart has no values.yaml")
	}
	if parentArgo, ok := values["argo-cd"].(map[string]any); ok {
		mergeValueShape(argoDefaults, parentArgo)
	}
	values["argo-cd"] = argoDefaults
	return values, nil
}

func mergeValueShape(destination, overlay map[string]any) {
	for key, value := range overlay {
		if nested, ok := value.(map[string]any); ok {
			if current, ok := destination[key].(map[string]any); ok {
				mergeValueShape(current, nested)
				continue
			}
		}
		destination[key] = value
	}
}

func buildAstronomerChartRepo() (*HelmChartRepo, error) {
	metaBytes, err := chartFS.ReadFile("chart/Chart.yaml")
	if err != nil {
		return nil, err
	}
	var meta chartMetadata
	if err := yaml.Unmarshal(metaBytes, &meta); err != nil {
		return nil, fmt.Errorf("parse chart metadata: %w", err)
	}
	if strings.TrimSpace(meta.Name) == "" || strings.TrimSpace(meta.Version) == "" {
		return nil, fmt.Errorf("embedded chart is missing name/version")
	}

	archiveTGZ, err := packageEmbeddedChart(meta.Name)
	if err != nil {
		return nil, err
	}
	archiveName := fmt.Sprintf("%s-%s.tgz", meta.Name, meta.Version)
	indexYAML, err := buildIndexYAML(meta, archiveName, archiveTGZ)
	if err != nil {
		return nil, err
	}

	return &HelmChartRepo{
		name:        meta.Name,
		version:     meta.Version,
		archiveName: archiveName,
		indexYAML:   indexYAML,
		archiveTGZ:  archiveTGZ,
	}, nil
}

func packageEmbeddedChart(rootName string) ([]byte, error) {
	var files []string
	if err := fs.WalkDir(chartFS, "chart", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, p)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(files)

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	for _, name := range files {
		data, err := chartFS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		info, err := fs.Stat(chartFS, name)
		if err != nil {
			return nil, err
		}
		archivePath := strings.TrimPrefix(path.Clean(name), "chart")
		archivePath = path.Join(rootName, strings.TrimPrefix(archivePath, "/"))
		hdr := &tar.Header{
			Name:    archivePath,
			Mode:    0o644,
			Size:    int64(len(data)),
			ModTime: info.ModTime(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := io.Copy(tw, bytes.NewReader(data)); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gzw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildIndexYAML(meta chartMetadata, archiveName string, archive []byte) ([]byte, error) {
	digest := sha256.Sum256(archive)
	index := map[string]any{
		"apiVersion": "v1",
		"generated":  time.Now().UTC().Format(time.RFC3339),
		"entries": map[string]any{
			meta.Name: []map[string]any{{
				"apiVersion":  meta.APIVersion,
				"name":        meta.Name,
				"description": meta.Description,
				"type":        meta.Type,
				"version":     meta.Version,
				"appVersion":  meta.AppVersion,
				"created":     time.Now().UTC().Format(time.RFC3339),
				"digest":      hex.EncodeToString(digest[:]),
				"urls":        []string{archiveName},
			}},
		},
	}
	return yaml.Marshal(index)
}
