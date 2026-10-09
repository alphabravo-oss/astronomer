// The distribution producer renders each exact reviewed profile without a
// Kubernetes client. It refuses unresolved images instead of warning and
// producing an incomplete offline inventory.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

type output struct {
	Catalog       builtinbundles.Catalog `json:"catalog"`
	Images        map[string][]string    `json:"images"`
	RenderDigests map[string]string      `json:"render_digests"`
	Dependencies  map[string][]string    `json:"dependencies"`
}

func main() {
	catalogPath := flag.String("catalog", "", "catalog JSON")
	charts := flag.String("charts", "", "directory containing <slug>.tgz")
	directory := flag.String("output-directory", "", "new OCI layout directory (preparation only)")
	repository := flag.String("repository", "", "immutable distribution repository identity without tag")
	assets := flag.String("asset-index", "", "closed external prerequisite asset index")
	platforms := flag.String("platform-evidence", "", "directory of captured digest-bound OCI indexes and child manifests")
	verifyLayout := flag.String("verify-layout", "", "validate prepared OCI subject/catalog closure without authentication or publication")
	flag.Parse()
	if *verifyLayout != "" {
		if err := validateLayout(*verifyLayout); err != nil {
			fmt.Fprintln(os.Stderr, "distribution layout blocked:", err)
			os.Exit(1)
		}
		return
	}
	result, err := produce(*catalogPath, *charts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "distribution profile blocked:", err)
		os.Exit(1)
	}
	if *directory != "" {
		index, err := buildLayout(result, *charts, *directory, *repository, *assets, *platforms)
		if err != nil {
			fmt.Fprintln(os.Stderr, "distribution profile blocked:", err)
			os.Exit(1)
		}
		if err := json.NewEncoder(os.Stdout).Encode(index); err != nil {
			os.Exit(1)
		}
		return
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		os.Exit(1)
	}
}
func produce(catalogPath, charts string) (output, error) {
	data, err := boundedFile(catalogPath, 4<<20)
	if err != nil || len(data) > 4<<20 {
		return output{}, errors.New("catalog input is unavailable or exceeds bounds")
	}
	catalog, err := builtinbundles.Parse(data)
	if err != nil {
		return output{}, err
	}
	result := output{Catalog: catalog, Images: map[string][]string{}, RenderDigests: map[string]string{}, Dependencies: map[string][]string{}}
	for index := range result.Catalog.Components {
		component := &result.Catalog.Components[index]
		archive := filepath.Join(charts, component.Slug+".tgz")
		compressed, err := boundedFile(archive, distribution.MaximumArchiveBytes)
		if err != nil || int64(len(compressed)) > distribution.MaximumArchiveBytes {
			return output{}, errors.New("chart archive is unavailable or exceeds bounds")
		}
		if fmt.Sprintf("sha256:%x", sha256.Sum256(compressed)) != component.Source.ChartDigest {
			return output{}, errors.New("chart archive digest differs from reviewed catalog")
		}
		if err := distribution.ValidateChartArchive(compressed); err != nil {
			return output{}, err
		}
		chart, err := loader.LoadArchive(bytes.NewReader(compressed))
		if err != nil {
			return output{}, errors.New("chart archive cannot be loaded")
		}
		if err := action.CheckDependencies(chart, chart.Metadata.Dependencies); err != nil {
			return output{}, errors.New("recursive chart dependencies are incomplete")
		}
		values, err := chartutil.CoalesceValues(chart, component.Values)
		if err != nil {
			return output{}, errors.New("chart values cannot be coalesced")
		}
		capabilities := chartutil.DefaultCapabilities.Copy()
		capabilities.KubeVersion.Version = component.Requirements.KubernetesMinimum
		renderValues, err := chartutil.ToRenderValues(chart, values, chartutil.ReleaseOptions{Name: component.ReleaseName, Namespace: component.TargetNamespace, Revision: 1, IsInstall: true}, capabilities)
		if err != nil {
			return output{}, errors.New("chart values cannot be rendered")
		}
		rendered, err := engine.Render(chart, renderValues)
		if err != nil {
			return output{}, errors.New("chart profile render failed")
		}
		imageSet := map[string]bool{}
		paths := make([]string, 0, len(rendered))
		for name := range rendered {
			paths = append(paths, name)
		}
		sort.Strings(paths)
		var renderedBytes bytes.Buffer
		for _, name := range paths {
			body := rendered[name]
			if len(body) > 16<<20 || renderedBytes.Len()+len(body) > 64<<20 {
				return output{}, errors.New("chart render exceeds bounds")
			}
			renderedBytes.WriteString(name + "\n" + body + "\n")
			if err := collectRenderedImages(body, imageSet); err != nil {
				return output{}, err
			}
		}
		// Values expose images for generated jobs/sidecars that static templates
		// cannot emit before the operator observes workloads. All are classified.
		if err := collectValueImages(map[string]any(values), imageSet); err != nil {
			return output{}, err
		}
		for _, declared := range component.Images {
			if !imageSet[declared] {
				return output{}, errors.New("declared component image is not classified by render or effective values")
			}
		}
		component.Values = map[string]any(values)
		component.Images = sortedImages(imageSet)
		result.Images[component.Slug] = component.Images
		result.RenderDigests[component.Slug] = fmt.Sprintf("sha256:%x", sha256.Sum256(renderedBytes.Bytes()))
		for _, dependency := range chart.Metadata.Dependencies {
			result.Dependencies[component.Slug] = append(result.Dependencies[component.Slug], dependency.Name+"@"+dependency.Version)
		}
		sort.Strings(result.Dependencies[component.Slug])
	}
	return result, nil
}
func pinnedImage(raw string) (string, error) {
	repository, digest, ok := strings.Cut(raw, "@")
	if !ok || len(digest) != 71 || !strings.HasPrefix(digest, "sha256:") {
		return "", errors.New("render or values contain a mutable image")
	}
	for _, character := range digest[7:] {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return "", errors.New("image digest is invalid")
		}
	}
	lastSlash := strings.LastIndex(repository, "/")
	if lastSlash < 0 {
		return "", errors.New("image registry is unspecified")
	}
	if colon := strings.LastIndex(repository, ":"); colon > lastSlash {
		repository = repository[:colon]
	}
	return repository + "@" + digest, nil
}
func collectRenderedImages(body string, images map[string]bool) error {
	decoder := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(body), 4096)
	for {
		var document any
		err := decoder.Decode(&document)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errors.New("rendered chart YAML is invalid")
		}
		if err = walkImageFields(document, images); err != nil {
			return err
		}
	}
}
func walkImageFields(value any, images map[string]bool) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "image" {
				if raw, ok := child.(string); ok {
					image, err := pinnedImage(raw)
					if err != nil {
						return err
					}
					images[image] = true
				}
			}
			if err := walkImageFields(child, images); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := walkImageFields(child, images); err != nil {
				return err
			}
		}
	}
	return nil
}
func collectValueImages(value any, images map[string]bool) error {
	if err := walkImageFields(value, images); err != nil {
		return err
	}
	switch typed := value.(type) {
	case map[string]any:
		repository, _ := typed["repository"].(string)
		if repository != "" {
			registry, _ := typed["registry"].(string)
			if registry != "" {
				repository = registry + "/" + repository
			}
			digest, _ := typed["digest"].(string)
			if digest == "" {
				digest, _ = typed["sha"].(string)
			}
			tag, _ := typed["tag"].(string)
			if digest != "" || tag != "" {
				raw := repository
				if digest != "" {
					raw += "@" + digest
				} else {
					raw += ":" + tag
				}
				image, err := pinnedImage(raw)
				if err != nil {
					return err
				}
				images[image] = true
			}
		}
		// Trivy's database subjects are OCI assets, separate from scanner images.
		for _, key := range []string{"dbRepository", "javaDbRepository"} {
			if raw, ok := typed[key].(string); ok && raw != "" && !strings.Contains(raw, "@sha256:") {
				return errors.New("vulnerability database subject is mutable; profile is unqualified")
			}
		}
		for _, child := range typed {
			if err := collectValueImages(child, images); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := collectValueImages(child, images); err != nil {
				return err
			}
		}
	}
	return nil
}
func sortedImages(images map[string]bool) []string {
	result := make([]string, 0, len(images))
	for image := range images {
		result = append(result, image)
	}
	sort.Strings(result)
	return result
}
