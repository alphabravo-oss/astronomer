package userpreferences

import (
	"encoding/json"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func FromRow(row sqlc.UserPreference) (Preferences, error) {
	prefs := Preferences{
		Theme:          Theme(row.Theme),
		TableDensity:   TableDensity(row.TableDensity),
		LandingRoute:   row.LandingRoute,
		TimeFormat:     TimeFormat(row.TimeFormat),
		Favorites:      []string{},
		PinnedClusters: []string{},
		StarredTypes:   []string{},
		RowsPerPage:    int(row.RowsPerPage),
		DateFormat:     row.DateFormat,
	}
	if err := json.Unmarshal(row.Favorites, &prefs.Favorites); err != nil {
		return Preferences{}, err
	}
	if err := json.Unmarshal(row.PinnedClusters, &prefs.PinnedClusters); err != nil {
		return Preferences{}, err
	}
	if len(row.StarredTypes) > 0 {
		if err := json.Unmarshal(row.StarredTypes, &prefs.StarredTypes); err != nil {
			return Preferences{}, err
		}
	}
	if prefs.StarredTypes == nil {
		prefs.StarredTypes = []string{}
	}
	if err := prefs.Validate(); err != nil {
		return Preferences{}, err
	}
	return prefs, nil
}
