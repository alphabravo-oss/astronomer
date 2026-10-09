// Package distribution consumes authenticated, closed release assets. Network
// authority is injected independently of artifact contents.
package distribution

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

const MediaType = "application/vnd.astronomer.builtin-bundles.v2+tar+gzip"
const MaximumArchiveBytes int64 = 128 << 20
const MaximumExpandedBytes int64 = 256 << 20
const MaximumMembers = 4096

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// BOM is authenticated by the OCI subject signature, including profiles,
// recursive chart archives, inventory evidence, and external prerequisites.
// Profiles with unresolved assets remain explicitly unqualified.
type BOM struct {
	SchemaVersion        int            `json:"schema_version"`
	AssetContractVersion int            `json:"asset_contract_version"`
	Release              string         `json:"release"`
	CatalogDigest        string         `json:"catalog_digest"`
	Members              []Member       `json:"members"`
	Profiles             []Profile      `json:"profiles"`
	Charts               []ChartSubject `json:"charts"`
	Assets               []Asset        `json:"assets"`
}
type Member struct {
	Path     string `json:"path"`
	Digest   string `json:"digest"`
	Size     int64  `json:"size"`
	Category string `json:"category"`
}
type Profile struct {
	Name               string   `json:"name"`
	Qualification      string   `json:"qualification"`
	ValuesPath         string   `json:"values_path"`
	InventoryPath      string   `json:"inventory_path"`
	RequiredCategories []string `json:"required_categories"`
	BlockedCategories  []string `json:"blocked_categories"`
	Capabilities       []string `json:"capabilities"`
}
type Image struct {
	Reference string   `json:"reference"`
	Platforms []string `json:"platforms"`
	Workloads []string `json:"workloads"`
}
type Inventory struct {
	SchemaVersion int     `json:"schema_version"`
	Images        []Image `json:"images"`
}

var categories = map[string]bool{"catalog": true, "chart": true, "chart_dependency": true, "values": true, "image_inventory": true, "crd": true, "tool": true, "operator": true, "plugin": true, "database": true, "trust": true, "vulnerability_database": true, "application_catalog": true, "runtime_image": true}

func decodeClosed(data []byte, target any) error { return jsoncontract.Decode(data, target) }

func memberPath(value string) bool {
	return value != "" && len(value) <= 512 && !strings.ContainsAny(value, "\\\x00") && !strings.HasPrefix(value, "/") && path.Clean(value) == value && value != "." && value != ".." && !strings.HasPrefix(value, "../")
}
func contentDigest(data []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(data)) }

func (b BOM) validate(release, catalogDigest string) error {
	if b.SchemaVersion != 1 || b.AssetContractVersion != 1 || b.Assets == nil || b.Release != release || !digestPattern.MatchString(b.CatalogDigest) || b.CatalogDigest != catalogDigest || len(b.Members) == 0 || len(b.Members) >= MaximumMembers || len(b.Profiles) == 0 {
		return errors.New("distribution BOM is not bound to release catalog")
	}
	seen := map[string]bool{}
	var total int64
	for _, member := range b.Members {
		if !memberPath(member.Path) || member.Path == "bom.json" || seen[member.Path] || !digestPattern.MatchString(member.Digest) || member.Size <= 0 || member.Size > categoryLimit(member.Category) || !categories[member.Category] {
			return errors.New("distribution BOM member is invalid")
		}
		seen[member.Path] = true
		total += member.Size
	}
	catalogMember := false
	for _, member := range b.Members {
		if member.Path == "catalog.json" && member.Category == "catalog" {
			catalogMember = true
		}
	}
	if total > MaximumExpandedBytes || !catalogMember {
		return errors.New("distribution BOM exceeds bounds or omits catalog")
	}
	memberKinds := map[string]string{}
	for _, member := range b.Members {
		memberKinds[member.Path] = member.Category
	}
	if err := validateAssetDeclarations(b.Assets, memberKinds); err != nil {
		return err
	}
	profiles := map[string]bool{}
	for _, profile := range b.Profiles {
		if profile.Name == "" || profiles[profile.Name] || !seen[profile.ValuesPath] || !seen[profile.InventoryPath] || len(profile.RequiredCategories) == 0 {
			return errors.New("distribution profile is incomplete")
		}
		if memberKinds[profile.ValuesPath] != "values" || memberKinds[profile.InventoryPath] != "image_inventory" {
			return errors.New("profile metadata category is invalid")
		}
		required := map[string]bool{}
		for _, category := range profile.RequiredCategories {
			if required[category] {
				return errors.New("duplicate profile category")
			}
			required[category] = true
		}
		for _, category := range []string{"catalog", "chart", "values", "image_inventory"} {
			if !required[category] {
				return errors.New("profile omits its mandatory core categories")
			}
		}
		profiles[profile.Name] = true
		if profile.Qualification != "qualified" && profile.Qualification != "unqualified" {
			return errors.New("distribution profile qualification is invalid")
		}
		if profile.Qualification == "qualified" && len(profile.BlockedCategories) > 0 {
			return errors.New("qualified distribution has blocked assets")
		}
		if err := validateCapabilityAssets(profile.Capabilities, b.Assets); err != nil {
			return err
		}
		for _, category := range append(append([]string(nil), profile.RequiredCategories...), profile.BlockedCategories...) {
			if !categories[category] {
				return errors.New("distribution asset category is unknown")
			}
		}
	}
	return nil
}

// ChartSubject binds the archive bytes to the vendor-signed OCI subject used
// by Flux chartRef. A mutable HTTP chart version cannot qualify this contract.
type ChartSubject struct {
	Component     string `json:"component"`
	Repository    string `json:"repository"`
	Digest        string `json:"digest"`
	ArchiveDigest string `json:"archive_digest"`
}

func categoryLimit(category string) int64 {
	switch category {
	case "catalog", "image_inventory", "trust":
		return 4 << 20
	case "values":
		return 16 << 20
	default:
		return MaximumArchiveBytes
	}
}

func validateJSON(data []byte) error { var document any; return jsoncontract.Decode(data, &document) }

// DecodeClosedDocument shares the strict single-interpretation JSON contract with preparation tooling.
func DecodeClosedDocument(data []byte, target any) error { return decodeClosed(data, target) }
