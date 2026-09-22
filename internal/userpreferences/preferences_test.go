package userpreferences

import (
	"strconv"
	"testing"
)

func TestDefaultsAreValid(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("Defaults().Validate() = %v", err)
	}
}

func TestValidateRejectsUnregisteredValues(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Preferences)
	}{
		{"theme", func(p *Preferences) { p.Theme = "sepia" }},
		{"density", func(p *Preferences) { p.TableDensity = "tiny" }},
		{"landing route", func(p *Preferences) { p.LandingRoute = "https://example.com" }},
		{"time format", func(p *Preferences) { p.TimeFormat = "seconds" }},
		{"favorite", func(p *Preferences) { p.Favorites = []string{"/dashboard/not-real"} }},
		{"duplicate favorite", func(p *Preferences) { p.Favorites = []string{"/dashboard", "/dashboard"} }},
		{"pinned cluster not a uuid", func(p *Preferences) {
			p.PinnedClusters = []string{"not-a-uuid"}
		}},
		{"duplicate pinned cluster", func(p *Preferences) {
			p.PinnedClusters = []string{
				"11111111-1111-1111-1111-111111111111",
				"11111111-1111-1111-1111-111111111111",
			}
		}},
		{"too many pinned clusters", func(p *Preferences) {
			ids := make([]string, MaxPinnedClusters+1)
			for i := range ids {
				ids[i] = "11111111-1111-1111-1111-" + fixedSuffix(i)
			}
			p.PinnedClusters = ids
		}},
		{"rows per page", func(p *Preferences) { p.RowsPerPage = 15 }},
		{"zero rows per page", func(p *Preferences) { p.RowsPerPage = 0 }},
		{"date format", func(p *Preferences) { p.DateFormat = "epoch" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefs := Defaults()
			tt.edit(&prefs)
			if err := prefs.Validate(); err == nil {
				t.Fatal("Validate() accepted an unregistered preference")
			}
		})
	}
}

func TestValidateAcceptsEveryAllowedRowsPerPage(t *testing.T) {
	for rows := range AllowedRowsPerPage {
		prefs := Defaults()
		prefs.RowsPerPage = rows
		if err := prefs.Validate(); err != nil {
			t.Fatalf("Validate() rejected rows_per_page=%d: %v", rows, err)
		}
	}
}

func TestValidateAcceptsEveryDateFormat(t *testing.T) {
	for _, format := range []string{DateLocale, DateISO, DateRelative} {
		prefs := Defaults()
		prefs.DateFormat = format
		if err := prefs.Validate(); err != nil {
			t.Fatalf("Validate() rejected date_format=%q: %v", format, err)
		}
	}
}

func TestValidateAcceptsEmptyPinnedClusters(t *testing.T) {
	prefs := Defaults()
	prefs.PinnedClusters = nil
	if err := prefs.Validate(); err != nil {
		t.Fatalf("Validate() rejected nil pinned_clusters: %v", err)
	}
}

// fixedSuffix produces a distinct, valid 12-hex-digit UUID suffix per index
// so TestValidateRejectsUnregisteredValues's "too many" case is entirely
// well-formed except for its length.
func fixedSuffix(i int) string {
	s := strconv.Itoa(100000000000 + i)
	return s[len(s)-12:]
}
