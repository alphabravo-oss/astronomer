package tasks

import (
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/repo"
)

func TestResolveChartURL(t *testing.T) {
	cases := []struct {
		base, in, want string
	}{
		{"https://charts.jetstack.io", "https://charts.jetstack.io/cert-manager-v1.16.0.tgz", "https://charts.jetstack.io/cert-manager-v1.16.0.tgz"},
		{"https://charts.longhorn.io", "longhorn-1.7.0.tgz", "https://charts.longhorn.io/longhorn-1.7.0.tgz"},
		{"https://example.com/charts/", "sub/app-1.0.0.tgz", "https://example.com/charts/sub/app-1.0.0.tgz"},
	}
	for _, c := range cases {
		got, err := resolveChartURL(c.base, c.in)
		if err != nil {
			t.Fatalf("resolveChartURL(%q,%q): %v", c.base, c.in, err)
		}
		if got != c.want {
			t.Errorf("resolveChartURL(%q,%q) = %q, want %q", c.base, c.in, got, c.want)
		}
	}
}

// SortEntries + slice must keep the three NEWEST versions, regardless of index order.
func TestSortAndCapKeepsNewest(t *testing.T) {
	idx := repo.NewIndexFile()
	for _, v := range []string{"1.0.0", "1.3.0", "1.1.0", "1.2.0", "0.9.0"} {
		idx.Entries["app"] = append(idx.Entries["app"], &repo.ChartVersion{
			Metadata: &chart.Metadata{Name: "app", Version: v},
		})
	}
	idx.SortEntries()
	versions := idx.Entries["app"]
	if len(versions) > catalogMaxVersionsPerChart {
		versions = versions[:catalogMaxVersionsPerChart]
	}
	got := []string{versions[0].Version, versions[1].Version, versions[2].Version}
	want := []string{"1.3.0", "1.2.0", "1.1.0"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cap kept %v, want %v", got, want)
		}
	}
}
