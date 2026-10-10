package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	digest "github.com/opencontainers/go-digest"
	"k8s.io/apimachinery/pkg/util/validation"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/scanner"
)

// Native wire structs describe the selected Trivy JSON report. Supplier
// inventory/extension nodes below are opaque, recursively duplicate/depth
// checked by jsoncontract, and never interpreted as authority or persisted.
// ImageConfig can contain private application environment defaults.
type databaseNativeReport struct {
	SchemaVersion int
	Trivy         struct {
		Version string
		Server  json.RawMessage
	}
	ReportID                               string
	CreatedAt                              time.Time
	ArtifactID, ArtifactName, ArtifactType string
	Metadata                               struct {
		Size                                                  int64
		OS, Reference, ImageConfig, Layers                    json.RawMessage
		ImageID                                               string
		DiffIDs, RepoTags, RepoDigests                        []string
		RepoURL, Branch, Commit, CommitMsg, Author, Committer string
		Tags                                                  []string
	}
	Results []databaseNativeResult
}
type databaseNativeResult struct {
	Target, Class, Type                                                                                               string
	Packages                                                                                                          json.RawMessage
	Vulnerabilities                                                                                                   []databaseNativeVulnerability
	MisconfSummary, Misconfigurations, Secrets, Licenses, CryptoAssets, CustomResources, ExperimentalModifiedFindings json.RawMessage
}
type databaseNativeVulnerability struct {
	VulnerabilityID, PkgID, PkgName, PkgPath, InstalledVersion, FixedVersion, Status, SeveritySource, PrimaryURL, Fingerprint, Title, Description, Severity string
	VendorIDs, CweIDs, References                                                                                                                           []string
	PkgIdentifier, Layer, DataSource, Custom, VendorSeverity                                                                                                json.RawMessage
	CVSS                                                                                                                                                    map[string]struct {
		V2Vector, V3Vector, V40Vector string
		V2Score, V3Score, V40Score    *float64
	}
	PublishedDate, LastModifiedDate *time.Time
}

func (databaseNativeReport) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<private native database scan report>")
}

type databaseNativeReportTarget struct {
	podUID                                                                              uuid.UUID
	namespace, pod, container, repository, manifestDigest, configDigest, scannerVersion string
}

// normalizeDatabaseNativeReport is DATA conversion after actual owned runtime
// reads. It cannot accept a feed, grant a witness, choose target identity or
// replace current locked rows. The only timestamp sent to legacy scanned_at is
// the actual owned scanner termination time; native CreatedAt is also checked.
// Java remains unknown until the selected scanner's real behavior is qualified.
func normalizeDatabaseNativeReport(ctx context.Context, payload []byte, target databaseNativeReportTarget, observation databaseScanObservation) (scanner.TrivyVulnerabilityReport, string, error) {
	var empty scanner.TrivyVulnerabilityReport
	if ctx == nil || target.podUID == uuid.Nil || target.container == "" || target.scannerVersion == "" || len(validation.IsDNS1123Label(target.namespace)) != 0 || target.namespace == "" || len(validation.IsDNS1123Subdomain(target.pod)) != 0 || target.pod == "" || len(validation.IsDNS1123Label(target.container)) != 0 || len(payload) == 0 || len(payload) > databaseScanMaximumReportBytes {
		return empty, "", errors.New("owned database native report inputs invalid")
	}
	if err := ctx.Err(); err != nil {
		return empty, "", err
	}
	repository, err := remote.NewRepository(target.repository)
	if err != nil || repository.Reference.Reference != "" {
		return empty, "", errors.New("owned database report repository invalid")
	}
	for _, value := range []string{target.manifestDigest, target.configDigest} {
		selected := digest.Digest(value)
		if selected.Validate() != nil || selected.Algorithm() != digest.SHA256 {
			return empty, "", errors.New("owned database report immutable image invalid")
		}
	}
	var report databaseNativeReport
	if jsoncontract.Decode(payload, &report) != nil || report.SchemaVersion != 2 || report.ArtifactType != "container_image" || len(report.Trivy.Server) != 0 && string(report.Trivy.Server) != "null" || strings.TrimPrefix(report.Trivy.Version, "v") != strings.TrimPrefix(target.scannerVersion, "v") || report.Metadata.ImageID != target.configDigest || len(report.Results) > 4096 {
		return empty, "", errors.New("owned database selected native report contract differs")
	}
	defer clear(report.Metadata.ImageConfig)
	defer clear(report.Metadata.Reference)
	if report.CreatedAt.IsZero() || report.CreatedAt.Location() != time.UTC || report.CreatedAt.Before(observation.scannerStarted) || report.CreatedAt.After(observation.scannerFinished) || observation.scannerFinished.After(time.Now().UTC()) {
		return empty, "", errors.New("owned database native report clock differs from actual termination")
	}
	name := "astronomer-owned-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte("astronomer-owned-image-report-v1\x00"+target.podUID.String()+"\x00"+target.container)).String()
	value := scanner.TrivyVulnerabilityReport{Metadata: scanner.TrivyMetadata{Name: name, Namespace: target.namespace, Labels: map[string]string{
		"trivy-operator.container.name": target.container, "trivy-operator.resource.kind": "Pod", "trivy-operator.resource.name": target.pod, "trivy-operator.resource.namespace": target.namespace,
	}}, Report: scanner.TrivyReport{Scanner: scanner.TrivyScanner{Name: "trivy", Version: report.Trivy.Version}, Registry: scanner.TrivyRegistry{Server: repository.Reference.Registry}, Artifact: scanner.TrivyArtifact{Repository: repository.Reference.Repository, Digest: target.manifestDigest}, UpdateTimestamp: observation.scannerFinished.Format(time.RFC3339Nano)}}
	// Metadata.UID stays empty: a native CLI report has no Kubernetes CRD UID.
	// A native ReportID/ArtifactID is supplier DATA and cannot manufacture one.
	for _, result := range report.Results {
		if result.Class != "os-pkgs" && result.Class != "lang-pkgs" {
			return empty, "", errors.New("owned database native report contains unsupported scan scope")
		}
		for _, opaque := range []json.RawMessage{result.MisconfSummary, result.Misconfigurations, result.Secrets, result.Licenses, result.CryptoAssets, result.CustomResources, result.ExperimentalModifiedFindings} {
			if len(opaque) != 0 && string(opaque) != "null" && string(opaque) != "[]" {
				return empty, "", errors.New("owned database native report contains unselected findings scope")
			}
		}
		if len(result.Vulnerabilities) > 100000-len(value.Report.Vulnerabilities) {
			return empty, "", errors.New("owned database native findings count exceeds bound")
		}
		for _, finding := range result.Vulnerabilities {
			cve, err := databaseNativeFinding(finding)
			if err != nil {
				return empty, "", err
			}
			switch cve.Severity {
			case "CRITICAL":
				value.Report.Summary.CriticalCount++
			case "HIGH":
				value.Report.Summary.HighCount++
			case "MEDIUM":
				value.Report.Summary.MediumCount++
			case "LOW":
				value.Report.Summary.LowCount++
			case "UNKNOWN":
				value.Report.Summary.UnknownCount++
			default:
				return empty, "", errors.New("owned database native finding severity invalid")
			}
			value.Report.Vulnerabilities = append(value.Report.Vulnerabilities, cve)
		}
	}
	if err := ctx.Err(); err != nil {
		return empty, "", err
	}
	return value, digest.FromBytes(payload).String(), nil
}

func databaseNativeFinding(finding databaseNativeVulnerability) (scanner.TrivyCVE, error) {
	if finding.VulnerabilityID == "" || finding.PkgName == "" || len(finding.VulnerabilityID) > 512 || len(finding.PkgName) > 4096 || len(finding.InstalledVersion) > 4096 || len(finding.FixedVersion) > 4096 || len(finding.Title) > 16384 || len(finding.Description) > 262144 || len(finding.PrimaryURL) > 8192 || len(finding.CVSS) > 32 {
		return scanner.TrivyCVE{}, errors.New("owned database native finding fields invalid")
	}
	severity := finding.Severity
	if severity == "" {
		severity = "UNKNOWN"
	}
	value := scanner.TrivyCVE{VulnerabilityID: finding.VulnerabilityID, Resource: finding.PkgName, InstalledVersion: finding.InstalledVersion, FixedVersion: finding.FixedVersion, PrimaryLink: finding.PrimaryURL, Title: finding.Title, Description: finding.Description, Severity: severity}
	var score float64
	present := false
	for _, vendor := range finding.CVSS {
		for _, candidate := range []*float64{vendor.V2Score, vendor.V3Score, vendor.V40Score} {
			if candidate == nil {
				continue
			}
			if math.IsNaN(*candidate) || math.IsInf(*candidate, 0) || *candidate < 0 || *candidate > 10 {
				return scanner.TrivyCVE{}, errors.New("owned database native CVSS score invalid")
			}
			present = true
			score = max(score, *candidate)
		}
	}
	if present {
		value.Score = &score
	}
	return value, nil
}
