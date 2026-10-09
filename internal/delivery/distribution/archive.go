package distribution

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
)

// ReadArchive is invoked only after subject authentication. Nothing is written
// to disk: all member names/types/sizes/hashes must match the signed closed BOM.
func ReadArchive(payload []byte, release, catalogDigest, profileName string) (builtinbundles.Catalog, BOM, error) {
	var empty builtinbundles.Catalog
	if int64(len(payload)) > MaximumArchiveBytes {
		return empty, BOM{}, errors.New("distribution archive exceeds bounds")
	}
	compressedInput := bytes.NewReader(payload)
	compressed, err := gzip.NewReader(compressedInput)
	if err != nil {
		return empty, BOM{}, errors.New("distribution archive compression is invalid")
	}
	defer compressed.Close()
	compressed.Multistream(false)
	expanded, err := io.ReadAll(io.LimitReader(compressed, MaximumExpandedBytes+1))
	if err != nil || int64(len(expanded)) > MaximumExpandedBytes || compressedInput.Len() != 0 {
		return empty, BOM{}, errors.New("distribution archive compression exceeds bounds or has trailing data")
	}
	raw := bytes.NewReader(expanded)
	reader := tar.NewReader(raw)
	members := map[string][]byte{}
	var total int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return empty, BOM{}, errors.New("distribution archive is invalid")
		}
		if !memberPath(header.Name) || header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > MaximumArchiveBytes || len(members) >= MaximumMembers {
			return empty, BOM{}, errors.New("distribution archive member is unsafe")
		}
		if _, exists := members[header.Name]; exists {
			return empty, BOM{}, errors.New("distribution archive member is duplicated")
		}
		total += header.Size
		if total > MaximumExpandedBytes {
			return empty, BOM{}, errors.New("distribution expanded archive exceeds bounds")
		}
		data, err := io.ReadAll(io.LimitReader(reader, header.Size+1))
		if err != nil || int64(len(data)) != header.Size {
			return empty, BOM{}, errors.New("distribution archive member size mismatch")
		}
		members[header.Name] = data
	}
	if trailing, _ := io.ReadAll(raw); len(bytes.Trim(trailing, "\x00")) != 0 {
		return empty, BOM{}, errors.New("distribution archive has trailing payload")
	}
	var bom BOM
	if len(members["bom.json"]) > 1<<20 || decodeClosed(members["bom.json"], &bom) != nil {
		return empty, BOM{}, errors.New("distribution signed BOM is unavailable")
	}
	if err := bom.validate(release, catalogDigest); err != nil {
		return empty, BOM{}, err
	}
	if len(members) != len(bom.Members)+1 {
		return empty, BOM{}, errors.New("distribution archive member set differs from BOM")
	}
	for _, member := range bom.Members {
		data, found := members[member.Path]
		if !found || int64(len(data)) != member.Size || contentDigest(data) != member.Digest {
			return empty, BOM{}, errors.New("distribution archive member digest mismatch")
		}
		if member.Category == "chart" || member.Category == "chart_dependency" {
			if err := ValidateChartArchive(data); err != nil {
				return empty, BOM{}, err
			}
		}
	}
	if contentDigest(members["catalog.json"]) != catalogDigest {
		return empty, BOM{}, errors.New("distribution catalog digest mismatch")
	}
	if validateJSON(members["catalog.json"]) != nil {
		return empty, BOM{}, errors.New("distribution catalog has ambiguous JSON")
	}
	catalog, err := builtinbundles.Parse(members["catalog.json"])
	if err != nil || catalog.Release != release {
		return empty, BOM{}, errors.New("distribution catalog is invalid")
	}
	if err := validateChartSubjects(bom, catalog); err != nil {
		return empty, bom, err
	}
	if err := validateProfile(bom, members, catalog, profileName); err != nil {
		return empty, bom, err
	}
	return catalog, bom, nil
}

func validateProfile(bom BOM, members map[string][]byte, catalog builtinbundles.Catalog, name string) error {
	for _, profile := range bom.Profiles {
		if profile.Name != name {
			continue
		}
		if profile.Qualification != "qualified" {
			category := "image_inventory"
			if len(profile.BlockedCategories) > 0 {
				category = profile.BlockedCategories[0]
			}
			return blocked(category, "distribution profile has unqualified assets")
		}
		found := map[string]bool{}
		for _, member := range bom.Members {
			found[member.Category] = true
		}
		for _, category := range profile.RequiredCategories {
			if !found[category] {
				return blocked(category, "distribution profile omits required asset category")
			}
		}
		var inventory Inventory
		if decodeClosed(members[profile.InventoryPath], &inventory) != nil || inventory.SchemaVersion != 1 {
			return errors.New("distribution image inventory is invalid")
		}
		images := map[string]bool{}
		for _, image := range inventory.Images {
			if len(image.Workloads) == 0 || len(image.Platforms) != 2 || image.Platforms[0] != "linux/amd64" || image.Platforms[1] != "linux/arm64" || images[image.Reference] {
				return errors.New("distribution image platform/workload inventory is incomplete")
			}
			_, digest, ok := bytes.Cut([]byte(image.Reference), []byte("@"))
			if !ok || !digestPattern.Match(digest) {
				return errors.New("distribution image is mutable")
			}
			images[image.Reference] = true
		}
		var values map[string]map[string]any
		if decodeClosed(members[profile.ValuesPath], &values) != nil || len(values) != len(catalog.Components) {
			return errors.New("distribution values profiles are incomplete")
		}
		for _, component := range catalog.Components {
			expected, _ := json.Marshal(component.Values)
			actual, _ := json.Marshal(values[component.Slug])
			if !bytes.Equal(expected, actual) {
				return errors.New("distribution values profile differs from catalog")
			}
			for _, image := range component.Images {
				if !images[image] {
					return errors.New("distribution inventory omits component image")
				}
			}
			chartFound := false
			for _, member := range bom.Members {
				if member.Category == "chart" && member.Digest == component.Source.ChartDigest {
					chartFound = true
				}
			}
			if !chartFound {
				return errors.New("distribution omits component chart archive")
			}
		}
		return nil
	}
	return errors.New("distribution profile is unavailable")
}

func validateChartSubjects(bom BOM, catalog builtinbundles.Catalog) error {
	if len(bom.Charts) != len(catalog.Components) {
		return errors.New("distribution lacks signed child chart subjects")
	}
	seen := map[string]bool{}
	for _, subject := range bom.Charts {
		if seen[subject.Component] || !digestPattern.MatchString(subject.Digest) || !digestPattern.MatchString(subject.ArchiveDigest) {
			return errors.New("distribution child chart subject is invalid")
		}
		seen[subject.Component] = true
		if _, err := builtinbundles.NormalizeOCISourceURL("oci://" + subject.Repository); err != nil {
			return err
		}
		found := false
		for _, component := range catalog.Components {
			if component.Slug == subject.Component && component.Source.ChartDigest == subject.ArchiveDigest {
				found = true
			}
		}
		if !found {
			return errors.New("distribution child chart does not bind original archive")
		}
	}
	return nil
}
