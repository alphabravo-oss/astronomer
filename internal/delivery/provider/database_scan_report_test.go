package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	digest "github.com/opencontainers/go-digest"
)

func nativeDatabaseReportFixture(t *testing.T) (databaseNativeReport, databaseNativeReportTarget, databaseScanObservation) {
	t.Helper()
	finished := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	target := databaseNativeReportTarget{podUID: uuid.New(), namespace: "tenant", pod: "running-workload", container: "application", repository: "registry.example/tenant/application", manifestDigest: digest.FromString("manifest").String(), configDigest: digest.FromString("config").String(), scannerVersion: "0.75.0"}
	observation := databaseScanObservation{scannerStarted: finished.Add(-time.Minute), scannerFinished: finished}
	report := databaseNativeReport{SchemaVersion: 2, ArtifactType: "container_image", CreatedAt: finished.Add(-time.Second)}
	report.Trivy.Version = target.scannerVersion
	report.Metadata.ImageID = target.configDigest
	report.Metadata.ImageConfig = json.RawMessage(`{"config":{"Env":["PRIVATE_CONFIG=never-print-this"]}}`)
	report.Results = []databaseNativeResult{{Target: "alpine", Class: "os-pkgs", Type: "alpine", Vulnerabilities: []databaseNativeVulnerability{{VulnerabilityID: "CVE-TEST", PkgName: "test-package", InstalledVersion: "1", FixedVersion: "2", Severity: "HIGH", Description: "a retained positive finding"}, {VulnerabilityID: "CVE-UNKNOWN", PkgName: "other-package", InstalledVersion: "3"}}}}
	return report, target, observation
}
func encodeNativeReport(t *testing.T, report databaseNativeReport) []byte {
	t.Helper()
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestOwnedNativeReportPreservesFindingsAndActualTerminationTime(t *testing.T) {
	native, target, observation := nativeDatabaseReportFixture(t)
	payload := encodeNativeReport(t, native)
	report, hash, err := normalizeDatabaseNativeReport(t.Context(), payload, target, observation)
	if err != nil {
		t.Fatal(err)
	}
	zero := 0.0
	scored := native.Results[0].Vulnerabilities[0]
	scored.CVSS = map[string]struct {
		V2Vector, V3Vector, V40Vector string
		V2Score, V3Score, V40Score    *float64
	}{"vendor": {V3Score: &zero}}
	converted, err := databaseNativeFinding(scored)
	if err != nil || converted.Score == nil || *converted.Score != 0 {
		t.Fatal("real zero CVSS was mistaken for missing score")
	}
	tooHigh := 10.1
	scored.CVSS["vendor"] = struct {
		V2Vector, V3Vector, V40Vector string
		V2Score, V3Score, V40Score    *float64
	}{V3Score: &tooHigh}
	if _, err := databaseNativeFinding(scored); err == nil {
		t.Fatal("out-of-range CVSS accepted")
	}
	if hash != digest.FromBytes(payload).String() || report.Metadata.UID != "" || report.Report.UpdateTimestamp != observation.scannerFinished.Format(time.RFC3339Nano) || report.Report.Summary.HighCount != 1 || report.Report.Summary.UnknownCount != 1 || len(report.Report.Vulnerabilities) != 2 || report.Report.Vulnerabilities[0].Description != "a retained positive finding" || report.Report.Artifact.Digest != target.manifestDigest {
		t.Fatal("actual identity, timestamp or positive findings lost")
	}
	reportAgain, _, err := normalizeDatabaseNativeReport(t.Context(), payload, target, observation)
	if err != nil || reportAgain.Metadata.Name != report.Metadata.Name {
		t.Fatal("same current Pod/container report identity not stable")
	}
	target.podUID = uuid.New()
	different, _, err := normalizeDatabaseNativeReport(t.Context(), payload, target, observation)
	if err != nil || different.Metadata.Name == report.Metadata.Name {
		t.Fatal("changed Pod inherited old report identity")
	}
	for _, value := range []any{native, &native} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%d"} {
			if strings.Contains(fmt.Sprintf(format, value), "never-print-this") {
				t.Fatal("private image defaults leaked through formatting")
			}
		}
	}
}

func TestOwnedNativeReportRejectsChangedWireIdentityAndClock(t *testing.T) {
	cases := []struct {
		name   string
		change func(*databaseNativeReport, *databaseNativeReportTarget, *databaseScanObservation)
	}{
		{"schema", func(r *databaseNativeReport, _ *databaseNativeReportTarget, _ *databaseScanObservation) {
			r.SchemaVersion = 1
		}},
		{"scanner", func(r *databaseNativeReport, _ *databaseNativeReportTarget, _ *databaseScanObservation) {
			r.Trivy.Version = "0.74.0"
		}},
		{"server", func(r *databaseNativeReport, _ *databaseNativeReportTarget, _ *databaseScanObservation) {
			r.Trivy.Server = json.RawMessage(`{"Version":"0.75.0"}`)
		}},
		{"configured-image-is-not-runtime", func(r *databaseNativeReport, _ *databaseNativeReportTarget, _ *databaseScanObservation) {
			r.Metadata.ImageID = digest.FromString("other config").String()
		}},
		{"artifact-type", func(r *databaseNativeReport, _ *databaseNativeReportTarget, _ *databaseScanObservation) {
			r.ArtifactType = "filesystem"
		}},
		{"before-actual-start", func(r *databaseNativeReport, _ *databaseNativeReportTarget, o *databaseScanObservation) {
			r.CreatedAt = o.scannerStarted.Add(-time.Second)
		}},
		{"after-actual-finish", func(r *databaseNativeReport, _ *databaseNativeReportTarget, o *databaseScanObservation) {
			r.CreatedAt = o.scannerFinished.Add(time.Second)
		}},
		{"missing-created-clock", func(r *databaseNativeReport, _ *databaseNativeReportTarget, _ *databaseScanObservation) {
			r.CreatedAt = time.Time{}
		}},
		{"offset-clock", func(r *databaseNativeReport, _ *databaseNativeReportTarget, _ *databaseScanObservation) {
			r.CreatedAt = r.CreatedAt.In(time.FixedZone("offset", 3600))
		}},
		{"unknown-findings-scope", func(r *databaseNativeReport, _ *databaseNativeReportTarget, _ *databaseScanObservation) {
			r.Results[0].Class = "config"
		}},
		{"unselected-secret-findings", func(r *databaseNativeReport, _ *databaseNativeReportTarget, _ *databaseScanObservation) {
			r.Results[0].Secrets = json.RawMessage(`[{"Severity":"HIGH"}]`)
		}},
		{"missing-pkg", func(r *databaseNativeReport, _ *databaseNativeReportTarget, _ *databaseScanObservation) {
			r.Results[0].Vulnerabilities[0].PkgName = ""
		}},
		{"unknown-severity", func(r *databaseNativeReport, _ *databaseNativeReportTarget, _ *databaseScanObservation) {
			r.Results[0].Vulnerabilities[0].Severity = "arbitrary"
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			native, target, observation := nativeDatabaseReportFixture(t)
			test.change(&native, &target, &observation)
			if _, _, err := normalizeDatabaseNativeReport(t.Context(), encodeNativeReport(t, native), target, observation); err == nil {
				t.Fatal("changed native identity/clock/scope accepted")
			}
		})
	}
}

func TestOwnedNativeReportClosedJSONPrivacyAndCancellation(t *testing.T) {
	native, target, observation := nativeDatabaseReportFixture(t)
	original := string(encodeNativeReport(t, native))
	for name, payload := range map[string]string{
		"top duplicate":           strings.Replace(original, `"SchemaVersion":2`, `"SchemaVersion":2,"SchemaVersion":2`, 1),
		"nested duplicate":        strings.Replace(original, `"Version":"0.75.0"`, `"Version":"0.75.0","Version":"0.75.0"`, 1),
		"unknown":                 strings.Replace(original, `"SchemaVersion":2`, `"SchemaVersion":2,"AllowWitness":true`, 1),
		"trailing":                original + "{}",
		"opaque nested duplicate": strings.Replace(original, `"Env":["PRIVATE_CONFIG=never-print-this"]`, `"Env":["PRIVATE_CONFIG=never-print-this"],"Env":[]`, 1),
		"oversize":                strings.Repeat(" ", databaseScanMaximumReportBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := normalizeDatabaseNativeReport(t.Context(), []byte(payload), target, observation); err == nil || strings.Contains(err.Error(), "never-print-this") {
				t.Fatal("invalid/private native report handling")
			}
		})
	}
	canceled, stop := context.WithCancel(t.Context())
	stop()
	if _, _, err := normalizeDatabaseNativeReport(canceled, []byte(original), target, observation); !errors.Is(err, context.Canceled) {
		t.Fatal("actual cancellation identity lost")
	}
}

func TestOwnedNativeReportCreatedAtWithinTerminationPrecisionInterval(t *testing.T) {
	for _, offset := range []time.Duration{0, time.Nanosecond, 900 * time.Millisecond, time.Second - time.Nanosecond} {
		native, target, observation := nativeDatabaseReportFixture(t)
		native.CreatedAt = observation.scannerFinished.Add(offset)
		if _, _, err := normalizeDatabaseNativeReport(t.Context(), encodeNativeReport(t, native), target, observation); err != nil {
			t.Fatal("structurally consistent fractional CreatedAt rejected", err)
		}
	}
	for _, offset := range []time.Duration{time.Second, time.Second + time.Nanosecond} {
		native, target, observation := nativeDatabaseReportFixture(t)
		native.CreatedAt = observation.scannerFinished.Add(offset)
		if _, _, err := normalizeDatabaseNativeReport(t.Context(), encodeNativeReport(t, native), target, observation); err == nil {
			t.Fatal("CreatedAt outside actual finish interval accepted")
		}
	}
}
