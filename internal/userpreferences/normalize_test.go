package userpreferences

import "testing"

func TestNormalizeOptionalPreservesRequiredFavoritesBoundary(t *testing.T) {
	for _, mode := range []string{"missing", "empty", "populated"} {
		t.Run(mode, func(t *testing.T) {
			p := Defaults()
			p.PinnedClusters = nil
			p.StarredTypes = nil
			p.RowsPerPage = 0
			p.DateFormat = ""
			switch mode {
			case "missing":
				p.Favorites = nil
			case "populated":
				p.Favorites = []string{"/dashboard"}
			}
			got := p.NormalizeOptional()
			if got.PinnedClusters == nil || got.StarredTypes == nil || got.RowsPerPage != 25 || got.DateFormat != "locale" {
				t.Fatal("optional defaults changed")
			}
			if mode == "missing" {
				if got.Favorites != nil || got.Validate() == nil {
					t.Fatal("required missing array became an accepted empty array")
				}
				return
			}
			if got.Validate() != nil || len(got.Favorites) != len(p.Favorites) {
				t.Fatal("valid explicit favorites changed")
			}
		})
	}
}
