package main

import (
	"bytes"
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
