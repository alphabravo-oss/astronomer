package userpreferences

import "testing"

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
