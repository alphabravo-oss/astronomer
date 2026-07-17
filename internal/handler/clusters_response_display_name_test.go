package handler

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// display_name is optional and unset on the large majority of clusters (anything
// attached without the wizard). The UI renders it straight into the page's <h1>,
// so an empty value left the cluster nameless on its own detail page — and the
// empty heading still claimed a row, which is what made the status badge look
// stranded and un-aligned.
func TestClusterToResponseFallsBackToNameForDisplayName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cluster sqlc.Cluster
		want    string
	}{
		{"unset display_name falls back to name", sqlc.Cluster{Name: "ghcr-attach-test"}, "ghcr-attach-test"},
		{"explicit display_name wins", sqlc.Cluster{Name: "prod-1", DisplayName: "Production"}, "Production"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := clusterToResponse(tc.cluster).DisplayName; got != tc.want {
				t.Fatalf("DisplayName = %q, want %q", got, tc.want)
			}
		})
	}
}
