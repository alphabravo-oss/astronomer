package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDatabaseRefRejectsUnsafeIdentity(t *testing.T) {
	for _, test := range []struct{ name, url string }{
		{"shared", "postgres://user:password@localhost/astronomer"},
		{"override", "postgres://user:password@localhost/plan032_w12_test?dbname=astronomer"},
		{"search-path", "postgres://user:password@localhost/plan032_w12_test?options=-csearch_path=public"},
		{"fragment", "postgres://user:password@localhost/plan032_w12_test#unbound"},
		{"wrong-scheme", "https://localhost/plan032_w12_test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ref.json")
			data, _ := json.Marshal(map[string]string{"database_url": test.url})
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := readDatabaseRef(path); err == nil {
				t.Fatal("unsafe reference accepted")
			}
		})
	}
}

func TestDatabaseRefRejectsReadableAndSymlinkFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ref.json")
	if err := os.WriteFile(path, []byte(`{"database_url":"postgres://user:password@localhost/plan032_w12_test"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readDatabaseRef(path); err == nil {
		t.Fatal("publicly readable secret accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readDatabaseRef(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, database, err := readDatabaseRef(path); err != nil || database != "plan032_w12_test" {
		t.Fatal("dedicated private reference rejected")
	}
}

func TestFaultMarkerBoundToCurrentOwnedRun(t *testing.T) {
	uid := "12345678-1234-1234-1234-123456789abc"
	started := time.Now().Add(-time.Second)
	for _, test := range []struct {
		name   string
		change func(*faultRecord)
	}{
		{"other-cluster", func(f *faultRecord) { f.ClusterUID = "other" }},
		{"unrecognized-fault", func(f *faultRecord) { f.Fault = "pause-node" }},
		{"missing-pod", func(f *faultRecord) { f.PodUID = "" }},
		{"old-run", func(f *faultRecord) { f.At = started.Add(-time.Second) }},
		{"future", func(f *faultRecord) { f.At = time.Now().Add(time.Minute) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := faultRecord{ClusterUID: uid, PodUID: uid, Fault: "owned-primary-pod-deletion", At: time.Now()}
			test.change(&f)
			path := filepath.Join(t.TempDir(), "fault.json")
			data, _ := json.Marshal(f)
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readFault(path, uid, started); err == nil {
				t.Fatal("unbound fault accepted")
			}
		})
	}
}

func TestSyntheticAuditChainAndTenantSeparation(t *testing.T) {
	for sequence := 1; sequence <= 5; sequence++ {
		tenant, key, payload, previous, audit := record(sequence)
		if tenant == "" || key == "" || payload == "" || previous != auditDigest(sequence-1) || audit != auditDigest(sequence) {
			t.Fatalf("invalid synthetic chain at %d", sequence)
		}
	}
	first, _, _, _, _ := record(1)
	second, _, _, _, _ := record(2)
	if first == second {
		t.Fatal("workload must exercise distinct tenants")
	}
}
