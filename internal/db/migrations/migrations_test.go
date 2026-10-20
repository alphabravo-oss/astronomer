package migrations_test

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/migrations"
)

// TestVersionsAreContiguous guards the embedded set without Docker: every
// file follows NNNNN_<slug>.sql and versions run 1..N with no gap or
// duplicate, so goose's out-of-order detection can never trip in production.
func TestVersionsAreContiguous(t *testing.T) {
	t.Parallel()
	versions := migrations.Versions()
	if len(versions) == 0 {
		t.Fatal("embedded migration set is empty")
	}
	for i, v := range versions {
		if want := int64(i + 1); v != want {
			t.Fatalf("version at index %d is %d, want %d (gap or duplicate)", i, v, want)
		}
	}
	if migrations.Latest() != versions[len(versions)-1] {
		t.Fatalf("Latest() = %d, want %d", migrations.Latest(), versions[len(versions)-1])
	}
}
