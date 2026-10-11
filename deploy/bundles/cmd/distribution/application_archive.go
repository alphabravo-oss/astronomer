package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

type applicationTransferDocument struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Release       string `json:"release"`
	CatalogDigest string `json:"catalog_digest"`
	ArchiveDigest string `json:"archive_digest"`
	BOMDigest     string `json:"bom_digest"`
	PublisherMode string `json:"publisher_mode"`
}

func verifyApplicationArchive(archiveFile, document, evidence, trustDirectory, provider, keyRef, identity, issuer string) error {
	// Authentication precedes even opening the untrusted archive. The evidence
	// is interpreted only by the canonical verifier with caller-pinned trust.
	data, err := authenticateDetachedDocument(document, evidence, trustDirectory, provider, keyRef, identity, issuer)
	if err != nil {
		return errors.New("application archive publisher authentication failed")
	}
	var transfer applicationTransferDocument
	if len(data) > 1<<20 || closedJSON(data, &transfer) != nil || transfer.SchemaVersion != 1 || transfer.Kind != "application_catalog_transfer" || !regexp.MustCompile(`^v[1-9][0-9]*\.[0-9]+\.[0-9]+$`).MatchString(transfer.Release) || (transfer.PublisherMode != "production" && transfer.PublisherMode != "qa") {
		return errors.New("application transfer contract invalid")
	}
	payload, err := boundedFile(archiveFile, distribution.MaximumArchiveBytes)
	if err != nil || sha(payload) != transfer.ArchiveDigest {
		return errors.New("application archive identity changed")
	}
	members, bom, err := distribution.ReadClosedArchive(payload, transfer.Release, transfer.CatalogDigest)
	if err != nil || sha(members["bom.json"]) != transfer.BOMDigest {
		return errors.New("application archive closed BOM invalid")
	}
	doc, err := catalog.ParseApplicationCatalog(members["catalog.json"])
	if err != nil {
		return errors.New("application archive catalog invalid")
	}
	apps := map[string]catalog.ApplicationCatalogEntry{}
	for _, app := range doc.Applications {
		apps[app.Slug] = app
	}
	closed := map[string]bool{}
	for _, profile := range bom.Profiles {
		if profile.Qualification != "qualified" || len(profile.BlockedCategories) > 0 {
			return errors.New("application archive profile unqualified")
		}
		var rendered applicationProfileOutput
		if closedJSON(members["profiles/"+profile.Name+".render.json"], &rendered) != nil {
			return errors.New("application render binding missing")
		}
		app, exists := apps[rendered.Input.Application]
		if !exists || profile.Name != rendered.Input.Application+"-"+rendered.Input.Profile || rendered.Rendered.Chart != app.Artifact.Chart || rendered.Rendered.Version != app.Artifact.Version {
			return errors.New("application render identity invalid")
		}
		var values map[string]any
		if closedJSON(members[profile.ValuesPath], &values) != nil || values == nil {
			return errors.New("application values profile invalid")
		}
		actual, _ := json.Marshal(values)
		expected, _ := json.Marshal(rendered.Rendered.Values)
		if !bytes.Equal(actual, expected) {
			return errors.New("application values profile changed")
		}
		var inventory distribution.Inventory
		if closedJSON(members[profile.InventoryPath], &inventory) != nil || inventory.SchemaVersion != 1 || len(inventory.Images) != len(rendered.Rendered.Images) {
			return errors.New("application inventory profile invalid")
		}
		seen := map[string]bool{}
		for _, image := range inventory.Images {
			if seen[image.Reference] || len(image.Platforms) != 2 || image.Platforms[0] != "linux/amd64" || image.Platforms[1] != "linux/arm64" || len(image.Workloads) != 1 || image.Workloads[0] != rendered.Input.Application {
				return errors.New("application image platform/scope incomplete")
			}
			if pinned, err := pinnedImage(image.Reference); err != nil || pinned != image.Reference {
				return errors.New("application inventory mutable image")
			}
			seen[image.Reference] = true
		}
		for _, image := range rendered.Rendered.Images {
			if !seen[image] {
				return errors.New("application inventory image missing")
			}
		}
		chart := members["charts/"+profile.Name+".tgz"]
		if len(chart) == 0 || sha(chart) != rendered.Input.ArchiveDigest {
			return errors.New("application chart profile changed")
		}
		requalified, err := renderReviewedChart(chart, values, rendered.Input.Application, rendered.Input.Namespace, rendered.Input.KubernetesVersion)
		if err != nil || requalified.RenderDigest != rendered.Rendered.RenderDigest {
			return errors.New("application import render differs from qualified profile")
		}
		actualImages, _ := json.Marshal(requalified.Images)
		expectedImages, _ := json.Marshal(rendered.Rendered.Images)
		if !bytes.Equal(actualImages, expectedImages) {
			return errors.New("application import image classification differs from qualified profile")
		}
		closed[rendered.Input.Application] = true
	}
	if len(closed) != len(apps) {
		return errors.New("application archive missing qualified application")
	}
	return nil
}
