package databasejob

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOwnedWorkloadRecordsPreserveLegacyAndRequireExactVersionedComparison(t *testing.T) {
	legacy := preparationData()
	legacyBytes := marshalPreparation(t, legacy)
	if bytes.Contains(legacyBytes, []byte(`"workload"`)) {
		t.Fatal("historical schema 1 gained workload fields")
	}
	if decoded, err := DecodePreparation(legacyBytes); err != nil || decoded != legacy {
		t.Fatal("historical schema 1 changed")
	}
	a, b, c, d := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("c", 64), "sha256:"+strings.Repeat("d", 64)
	value := legacy
	value.SchemaVersion, value.Kind = 2, "AstronomerOwnedDatabaseScanPreparation"
	value.Workload = &WorkloadInputSnapshot{ManifestDigest: c, Architecture: "amd64", IndexDigest: b, Members: []WorkloadInputMember{{Name: "oci-layout", Digest: a, Size: 1}, {Name: "index.json", Digest: b, Size: 1}, {Name: "blobs/sha256/" + strings.Repeat("c", 64), Digest: c, Size: 1}, {Name: "blobs/sha256/" + strings.Repeat("d", 64), Digest: d, Size: 1}}}
	payload := marshalPreparation(t, value)
	decoded, err := DecodePreparation(payload)
	if err != nil || !sameWorkloadSnapshot(decoded.Workload, value.Workload) {
		t.Fatal("valid versioned comparison refused")
	}
	var output bytes.Buffer
	clock := time.Date(2026, 10, 10, 2, 0, 0, 0, time.UTC)
	if err := emitMeasurement(&output, value, "final", value.Cache, clock); err != nil {
		t.Fatal(err)
	}
	row, err := DecodeMeasurement(output.Bytes(), value)
	if err != nil || row.Kind != "AstronomerOwnedDatabaseScanMeasurement" {
		t.Fatal("exact versioned observation unavailable")
	}
	decoded.Workload.Members[0].Digest = d
	if !sameWorkloadSnapshot(row.Workload, value.Workload) {
		t.Fatal("decoded caller mutation changed another record")
	}
	for name, mutate := range map[string]func(*Preparation){
		"legacy-with-workload": func(v *Preparation) { v.SchemaVersion, v.Kind = 1, "AstronomerDatabaseScanPreparation" },
		"new-without-workload": func(v *Preparation) { v.Workload = nil },
		"unknown-version":      func(v *Preparation) { v.SchemaVersion = 3 },
		"legacy-kind":          func(v *Preparation) { v.Kind = "AstronomerDatabaseScanPreparation" },
		"changed-platform":     func(v *Preparation) { v.Workload.Architecture = "arm64" },
		"changed-image": func(v *Preparation) {
			v.Workload.ManifestDigest = "sha256:" + strings.Repeat("f", 64)
			v.Workload.Members[2].Name = "blobs/sha256/" + strings.Repeat("f", 64)
			v.Workload.Members[2].Digest = v.Workload.ManifestDigest
		},
		"changed-member": func(v *Preparation) { v.Workload.Members[0].Digest = d },
	} {
		t.Run(name, func(t *testing.T) {
			expected := value
			expected.Workload = cloneWorkloadSnapshot(value.Workload)
			mutate(&expected)
			if _, err := DecodeMeasurement(output.Bytes(), expected); err == nil {
				t.Fatal("foreign versioned observation accepted")
			}
		})
	}
	// Closed recursive decoding applies even to a syntactically valid comparison.
	for name, changed := range map[string][]byte{
		"nested-duplicate": bytes.Replace(payload, []byte(`"architecture":"amd64"`), []byte(`"architecture":"amd64","architecture":"amd64"`), 1),
		"nested-unknown":   bytes.Replace(payload, []byte(`"architecture":"amd64"`), []byte(`"architecture":"amd64","authority":true`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePreparation(changed); err == nil {
				t.Fatal("alternative nested contract accepted")
			}
		})
	}
	// The serializer exposes only comparison identity, never private OCI config.
	encoded, err := json.Marshal(row)
	if err != nil || bytes.Contains(encoded, []byte("PRIVATE_DEFAULT")) {
		t.Fatal("private config escaped observation")
	}
}
