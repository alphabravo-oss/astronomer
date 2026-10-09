package main

import (
	"encoding/json"
	"errors"
	"regexp"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

type applicationProfileInputs struct {
	SchemaVersion int                       `json:"schema_version"`
	CatalogDigest string                    `json:"catalog_digest"`
	Profiles      []applicationProfileInput `json:"profiles"`
}
type applicationProfileInput struct {
	Application       string         `json:"application"`
	Profile           string         `json:"profile"`
	Archive           string         `json:"archive"`
	ArchiveDigest     string         `json:"archive_digest"`
	Values            map[string]any `json:"values"`
	Images            []string       `json:"images"`
	KubernetesVersion string         `json:"kubernetes_version"`
	Namespace         string         `json:"namespace"`
}
type applicationProfileOutput struct {
	Input    applicationProfileInput `json:"input"`
	Rendered renderedProfile         `json:"rendered"`
}
type applicationProfilesOutput struct {
	SchemaVersion int                        `json:"schema_version"`
	CatalogDigest string                     `json:"catalog_digest"`
	Profiles      []applicationProfileOutput `json:"profiles"`
}

var applicationProfileName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Application export uses the same offline renderer/classifier/platform checker
// as builtins. The reviewed index must close every supported application/preset.
func renderApplicationProfiles(catalogFile, indexFile, platformDirectory string) (applicationProfilesOutput, error) {
	var empty applicationProfilesOutput
	data, err := boundedFile(catalogFile, 4<<20)
	if err != nil {
		return empty, err
	}
	var rawCatalog map[string]any
	if closedJSON(data, &rawCatalog) != nil || rawCatalog == nil {
		return empty, errors.New("application export requires unambiguous JSON catalog")
	}
	doc, err := catalog.ParseApplicationCatalog(data)
	if err != nil {
		return empty, errors.New("application catalog invalid")
	}
	indexData, err := boundedFile(indexFile, 4<<20)
	var index applicationProfileInputs
	if err != nil || closedJSON(indexData, &index) != nil || index.SchemaVersion != 1 || index.CatalogDigest != sha(data) || len(index.Profiles) == 0 || len(index.Profiles) > distribution.MaximumMembers {
		return empty, errors.New("reviewed application profile scope invalid")
	}
	applications := map[string]catalog.ApplicationCatalogEntry{}
	for _, app := range doc.Applications {
		applications[app.Slug] = app
	}
	seen, closed := map[string]bool{}, map[string]bool{}
	result := applicationProfilesOutput{SchemaVersion: 1, CatalogDigest: sha(data), Profiles: []applicationProfileOutput{}}
	var scopeBytes int64
	for _, profile := range index.Profiles {
		app, exists := applications[profile.Application]
		name := profile.Application + "/" + profile.Profile
		if !exists || seen[name] || !applicationProfileName.MatchString(profile.Profile) || !applicationProfileName.MatchString(profile.Namespace) || profile.KubernetesVersion == "" || profile.Values == nil {
			return empty, errors.New("application profile identity invalid")
		}
		seen[name] = true
		archive, err := boundedFile(profile.Archive, distribution.MaximumArchiveBytes)
		if err != nil || sha(archive) != profile.ArchiveDigest {
			return empty, errors.New("reviewed application chart archive changed")
		}
		rendered, err := renderReviewedChart(archive, profile.Values, profile.Application, profile.Namespace, profile.KubernetesVersion)
		if err != nil {
			return empty, err
		}
		if rendered.Chart != app.Artifact.Chart || rendered.Version != app.Artifact.Version {
			return empty, errors.New("application chart metadata differs from catalog pin")
		}
		declared := map[string]bool{}
		for _, image := range profile.Images {
			if declared[image] {
				return empty, errors.New("duplicate reviewed application image")
			}
			declared[image] = true
		}
		if len(declared) != len(rendered.Images) {
			return empty, errors.New("application image set is not closed")
		}
		for _, image := range rendered.Images {
			if !declared[image] {
				return empty, errors.New("application render contains undeclared image")
			}
			if err := verifyPlatforms(image, platformDirectory); err != nil {
				return empty, err
			}
		}
		closed[profile.Application] = true
		encoded, _ := json.Marshal(applicationProfileOutput{Input: profile, Rendered: rendered})
		scopeBytes += int64(len(archive)) + int64(len(encoded))
		if scopeBytes > distribution.MaximumExpandedBytes {
			return empty, errors.New("application profile scope exceeds archive bounds")
		}
		result.Profiles = append(result.Profiles, applicationProfileOutput{Input: profile, Rendered: rendered})
	}
	if len(closed) != len(applications) {
		return empty, errors.New("application catalog includes unqualified application")
	}
	return result, nil
}
