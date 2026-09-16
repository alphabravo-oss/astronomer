package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type ExtensionRecordResponse struct {
	ID                  uuid.UUID         `json:"id"`
	Name                string            `json:"name"`
	DisplayName         string            `json:"display_name"`
	Version             string            `json:"version"`
	Source              string            `json:"source"`
	Checksum            string            `json:"checksum"`
	Enabled             bool              `json:"enabled"`
	CompatibilityStatus string            `json:"compatibility_status"`
	Manifest            ExtensionManifest `json:"manifest"`
	InstalledAt         string            `json:"installed_at"`
	UpdatedAt           string            `json:"updated_at"`
}

type ExtensionListResponse struct {
	Items          []ExtensionRecordResponse `json:"items"`
	SampleManifest ExtensionManifest         `json:"sample_manifest"`
}

func extensionChecksum(manifest ExtensionManifest) string {
	raw, _ := json.Marshal(manifest)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func extensionRecordResponse(row sqlc.UIExtension) ExtensionRecordResponse {
	var manifest ExtensionManifest
	_ = json.Unmarshal(row.Manifest, &manifest)
	return ExtensionRecordResponse{
		ID:                  row.ID,
		Name:                row.Name,
		DisplayName:         row.DisplayName,
		Version:             row.Version,
		Source:              row.Source,
		Checksum:            row.Checksum,
		Enabled:             row.Enabled,
		CompatibilityStatus: row.CompatibilityStatus,
		Manifest:            manifest,
		InstalledAt:         row.InstalledAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:           row.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func extensionDisplayName(manifest ExtensionManifest) string {
	if manifest.DisplayName != "" {
		return manifest.DisplayName
	}
	return manifest.Name
}

func sanitizeExtensionSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "manual"
	}
	if len(source) > 128 {
		return source[:128]
	}
	return source
}
