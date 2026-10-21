package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

func TestArchiveReproducibleAndClosedReaderRejectsMutation(t *testing.T) {
	t.Parallel()
	members := map[string][]byte{"catalog.json": []byte(`{"schema_version":1}`), "charts/chart.tgz": []byte("exact bytes")}
	a, err := archive(members)
	if err != nil {
		t.Fatal(err)
	}
	b, err := archive(members)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("release archive is not reproducible")
	}
	parsed, err := distribution.ReadBoundedRegularArchive(a, 1<<20, 1<<20, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(parsed["catalog.json"], members["catalog.json"]) {
		t.Fatal("archive changed catalog bytes")
	}
	a[len(a)-1] ^= 1
	if _, err := distribution.ReadBoundedRegularArchive(a, 1<<20, 1<<20, 10); err == nil {
		t.Fatal("corrupt release archive accepted")
	}
}

func TestOutputWritesStayWithinReleaseDirectory(t *testing.T) {
	t.Parallel()
	outside := t.TempDir()
	output := filepath.Join(outside, "release")
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	p := producer{out: output}
	if err := p.write("manifest.json", []byte("manifest")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(output, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../escaped.json", "escape/escaped.json"} {
		if err := p.write(name, []byte("outside")); err == nil {
			t.Fatalf("write outside output accepted: %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "escaped.json")); !os.IsNotExist(err) {
		t.Fatalf("unexpected escaped file: %v", err)
	}
}
