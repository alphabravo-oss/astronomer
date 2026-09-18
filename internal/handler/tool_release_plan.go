package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"sigs.k8s.io/yaml"
)

type toolRelease struct {
	ReleaseName        string `json:"releaseName"`
	Namespace          string `json:"namespace"`
	ChartName          string `json:"chartName"`
	RepoURL            string `json:"repoUrl"`
	Version            string `json:"version"`
	ValuesYAML         string `json:"valuesYaml,omitempty"`
	State              string `json:"state"`
	PreviousRevision   int    `json:"previousRevision,omitempty"`
	PreviousValuesYAML string `json:"previousValuesYaml,omitempty"`
	PreviousPreset     string `json:"previousPreset,omitempty"`
	ExpectedRevision   int    `json:"expectedRevision,omitempty"`
	Revision           int    `json:"revision,omitempty"`
	Error              string `json:"error,omitempty"`
	OperationMarker    string `json:"operationMarker,omitempty"`
	RollbackRevision   int    `json:"rollbackRevision,omitempty"`
}

func buildToolReleasePlan(tool sqlc.ClusterTool, releaseName, valuesYAML string) ([]toolRelease, error) {
	charts, err := parseToolCharts(tool.Charts)
	if err != nil {
		return nil, fmt.Errorf("invalid tool charts: %w", err)
	}
	if len(charts) == 0 {
		return nil, errors.New("tool has no charts configured")
	}
	slices.SortStableFunc(charts, func(a, b toolChart) int { return a.Order - b.Order })
	if len(charts) > 1 && releaseName != "" && releaseName != tool.Slug {
		return nil, errors.New("multi-release tools use their declared release names")
	}
	var values map[string]any
	if strings.TrimSpace(valuesYAML) != "" {
		if err := yaml.Unmarshal([]byte(valuesYAML), &values); err != nil {
			return nil, fmt.Errorf("invalid tool values: %w", err)
		}
	}
	plan := make([]toolRelease, 0, len(charts))
	seen := map[string]bool{}
	for _, chart := range charts {
		name := chart.ReleaseName
		if name == "" {
			name = releaseName
		}
		if name == "" {
			name = tool.Slug
		}
		namespace := chartNamespace(tool, chart)
		version := chart.Version
		if version == "" {
			version = tool.VersionConstraint
		}
		if name == "" || namespace == "" || chart.ChartName == "" || chart.RepoURL == "" || version == "" {
			return nil, errors.New("tool release requires name, namespace, chart, repository, and pinned version")
		}
		key := namespace + "/" + name
		if seen[key] {
			return nil, fmt.Errorf("duplicate tool release %s", key)
		}
		seen[key] = true
		releaseValues := valuesYAML
		if chart.ValuesKey != "" {
			selected := values[chart.ValuesKey]
			if selected == nil {
				selected = map[string]any{}
			}
			if _, ok := selected.(map[string]any); !ok {
				return nil, fmt.Errorf("values %q must be an object", chart.ValuesKey)
			}
			raw, err := yaml.Marshal(selected)
			if err != nil {
				return nil, err
			}
			releaseValues = string(raw)
		}
		plan = append(plan, toolRelease{ReleaseName: name, Namespace: namespace, ChartName: chart.ChartName, RepoURL: chart.RepoURL, Version: version, ValuesYAML: releaseValues, State: "pending"})
	}
	return plan, nil
}

func (env toolOperationEnvelope) release(index int) toolReleaseExecution {
	r := env.Releases[index]
	return toolReleaseExecution{ClusterID: env.ClusterID, ToolSlug: env.ToolSlug, Preset: env.Preset, ReleaseName: r.ReleaseName, Namespace: r.Namespace, ChartName: r.ChartName, RepoURL: r.RepoURL, Version: r.Version, ValuesYAML: r.ValuesYAML}
}

func validateToolReleaseEnvelope(env toolOperationEnvelope) error {
	if len(env.Releases) == 0 {
		return errors.New("tool operation has no release plan")
	}
	seen := map[string]bool{}
	for _, release := range env.Releases {
		key := release.Namespace + "/" + release.ReleaseName
		if release.ReleaseName == "" || release.Namespace == "" || seen[key] {
			return fmt.Errorf("invalid or duplicate tool release %q", key)
		}
		seen[key] = true
	}
	return nil
}

func toolPlanAudit(env toolOperationEnvelope) any {
	// Audit identities and coordinates, never values that may contain credentials.
	releases := make([]map[string]string, 0, len(env.Releases))
	for _, r := range env.Releases {
		releases = append(releases, map[string]string{"release_name": r.ReleaseName, "namespace": r.Namespace, "chart": r.ChartName, "version": r.Version})
	}
	return releases
}

func resetToolPlan(raw json.RawMessage) (toolOperationEnvelope, error) {
	var env toolOperationEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return env, err
	}
	if err := validateToolReleaseEnvelope(env); err != nil {
		return env, err
	}
	for i := range env.Releases {
		env.Releases[i].State = "pending"
		env.Releases[i].Error = ""
		env.Releases[i].ExpectedRevision = 0
	}
	return env, nil
}

func toolOperationIntentEqual(left, right json.RawMessage) bool {
	var a, b toolOperationEnvelope
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	for _, env := range []*toolOperationEnvelope{&a, &b} {
		for i := range env.Releases {
			r := &env.Releases[i]
			r.State = ""
			r.PreviousRevision = 0
			r.PreviousValuesYAML = ""
			r.PreviousPreset = ""
			r.ExpectedRevision = 0
			r.Revision = 0
			r.Error = ""
			r.OperationMarker = ""
		}
	}
	return reflect.DeepEqual(a, b)
}
