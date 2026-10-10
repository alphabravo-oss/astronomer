package databasejob

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

func preparationData() Preparation {
	clock := time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)
	digest := "sha256:" + strings.Repeat("a", 64)
	return Preparation{SchemaVersion: 1, Kind: "AstronomerDatabaseScanPreparation", ClaimID: uuid.MustParse("857e0a17-a815-49b8-b62c-52e404b00667"), ScannerDigest: digest, Cache: distribution.DatabaseCacheSnapshot{FeedDigest: digest, Main: distribution.DatabaseCacheContent{Role: "trivy-database", File: "trivy.db", Digest: digest, Size: 10}, Java: distribution.DatabaseCacheContent{Role: "trivy-java-database", File: "trivy-java.db", Digest: digest, Size: 20}}, MainMetadata: distribution.DatabaseUpdateMetadata{Digest: digest, Size: 10, Schema: 2, UpdatedAt: clock, NextUpdate: clock.Add(time.Hour)}, JavaMetadata: distribution.DatabaseUpdateMetadata{Digest: digest, Size: 20, Schema: 1, UpdatedAt: clock, NextUpdate: clock.Add(time.Hour)}}
}
func marshalPreparation(t *testing.T, value Preparation) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestDatabaseJobPreparationRejectsAlternativeContracts(t *testing.T) {
	original := preparationData()
	body := marshalPreparation(t, original)
	decoded, err := DecodePreparation(body)
	if err != nil || decoded != original {
		t.Fatalf("comparison DATA: %v", err)
	}
	cases := map[string][]byte{
		"null": []byte("null"), "array": []byte("[]"), "empty": nil,
		"oversized": bytes.Repeat([]byte(" "), MaximumRecordBytes+1),
		"trailing":  append(append([]byte(nil), body...), []byte(" {}")...),
		"duplicate": bytes.Replace(body, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1),
		"unknown":   bytes.Replace(body, []byte(`"kind":`), []byte(`"secret":"not-accepted","kind":`), 1),
	}
	// Metadata uses its canonical lowercase JSON contract.
	cases["nested-duplicate"] = bytes.Replace(body, []byte(`"schema":2`), []byte(`"schema":2,"schema":2`), 1)
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePreparation(bad); err == nil {
				t.Fatal("invalid contract accepted")
			}
		})
	}
	mutations := map[string]func(*Preparation){
		"claim-v1": func(v *Preparation) { v.ClaimID = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8") },
		"scanner":  func(v *Preparation) { v.ScannerDigest = "latest" },
		"feed":     func(v *Preparation) { v.Cache.FeedDigest = "" },
		"role":     func(v *Preparation) { v.Cache.Main.Role = "trivy-java-database" },
		"path":     func(v *Preparation) { v.Cache.Main.File = "../trivy.db" },
		"size":     func(v *Preparation) { v.Cache.Main.Size = 0 },
		"overflow": func(v *Preparation) {
			v.Cache.Main.Size = distribution.MaximumDatabaseExpandedBytes
			v.Cache.Java.Size = 1
		},
		"metadata-cap": func(v *Preparation) { v.JavaMetadata.Size = 65537 },
		"schema":       func(v *Preparation) { v.JavaMetadata.Schema = 2 },
		"clock": func(v *Preparation) {
			v.MainMetadata.UpdatedAt = v.MainMetadata.UpdatedAt.In(time.FixedZone("offset", 3600))
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			value := original
			mutate(&value)
			if _, err := DecodePreparation(marshalPreparation(t, value)); err == nil {
				t.Fatal("invalid member accepted")
			}
		})
	}
}

func TestDatabaseJobMeasurementRequiresExactComparisonData(t *testing.T) {
	value := preparationData()
	clock := time.Date(2026, 10, 10, 2, 0, 0, 0, time.UTC)
	var output bytes.Buffer
	if emitMeasurement(&output, value, "final", value.Cache, clock) != nil {
		t.Fatal("emit DATA")
	}
	row, err := DecodeMeasurement(bytes.TrimSpace(output.Bytes()), value)
	if err != nil || row.ObservedAt != clock || row.Phase != "final" {
		t.Fatalf("decode: %v", err)
	}
	for name, mutate := range map[string]func(*Preparation){"claim": func(v *Preparation) { v.ClaimID = uuid.New() }, "scanner": func(v *Preparation) { v.ScannerDigest = "sha256:" + strings.Repeat("b", 64) }, "feed": func(v *Preparation) { v.Cache.FeedDigest = "sha256:" + strings.Repeat("b", 64) }, "main": func(v *Preparation) { v.Cache.Main.Digest = "sha256:" + strings.Repeat("b", 64) }, "metadata": func(v *Preparation) { v.JavaMetadata.Digest = "sha256:" + strings.Repeat("b", 64) }} {
		t.Run(name, func(t *testing.T) {
			expected := value
			mutate(&expected)
			if _, err := DecodeMeasurement(output.Bytes(), expected); err == nil {
				t.Fatal("foreign observation accepted")
			}
		})
	}
	if err := emitMeasurement(&output, value, "periodic", value.Cache, clock); err == nil {
		t.Fatal("periodic observation accepted")
	}
	if _, err := DecodeMeasurement(append(output.Bytes(), []byte("{}")...), value); err == nil {
		t.Fatal("trailing observation accepted")
	}
}
