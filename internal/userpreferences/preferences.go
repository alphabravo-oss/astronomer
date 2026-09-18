// Package userpreferences owns the canonical schema and validation rules for
// operator console preferences. Persistence and transport layers use this
// package rather than maintaining their own stringly typed registries.
package userpreferences

import (
	"errors"
	"fmt"
	"strings"
)

type Theme string
type TableDensity string
type TimeFormat string

const (
	ThemeLight  Theme = "light"
	ThemeDark   Theme = "dark"
	ThemeSystem Theme = "system"

	DensityCompact     TableDensity = "compact"
	DensityComfortable TableDensity = "comfortable"

	TimeLocale TimeFormat = "locale"
	Time12Hour TimeFormat = "12h"
	Time24Hour TimeFormat = "24h"
)

const MaxFavorites = 12

var (
	allowedLandingRoutes = map[string]struct{}{
		"/dashboard": {}, "/dashboard/clusters": {}, "/dashboard/projects": {},
		"/dashboard/workloads": {}, "/dashboard/delivery": {},
		"/dashboard/monitoring": {}, "/dashboard/alerting": {},
		"/dashboard/security": {}, "/dashboard/audit": {},
	}
	allowedFavoriteRoutes = map[string]struct{}{
		"/dashboard": {}, "/dashboard/clusters": {}, "/dashboard/projects": {},
		"/dashboard/workloads": {}, "/dashboard/delivery": {},
		"/dashboard/monitoring": {}, "/dashboard/alerting": {},
		"/dashboard/logging": {}, "/dashboard/security": {},
		"/dashboard/rbac": {}, "/dashboard/audit": {},
		"/dashboard/tools": {}, "/dashboard/extensions": {},
	}
)

// openapi:request UserPreferences
// Preferences is the complete, versionless preference document exposed by
// the API. New keys require an intentional schema, database, API and UI change.
type Preferences struct {
	Theme        Theme        `json:"theme"`
	TableDensity TableDensity `json:"table_density"`
	LandingRoute string       `json:"landing_route"`
	TimeFormat   TimeFormat   `json:"time_format"`
	Favorites    []string     `json:"favorites"`
}

func Defaults() Preferences {
	return Preferences{
		Theme: ThemeSystem, TableDensity: DensityComfortable,
		LandingRoute: "/dashboard", TimeFormat: TimeLocale,
		Favorites: []string{},
	}
}

func (p Preferences) Validate() error {
	if p.Theme != ThemeLight && p.Theme != ThemeDark && p.Theme != ThemeSystem {
		return fmt.Errorf("theme must be light, dark, or system")
	}
	if p.TableDensity != DensityCompact && p.TableDensity != DensityComfortable {
		return fmt.Errorf("table_density must be compact or comfortable")
	}
	if _, ok := allowedLandingRoutes[p.LandingRoute]; !ok {
		return fmt.Errorf("landing_route is not an available landing page")
	}
	if p.TimeFormat != TimeLocale && p.TimeFormat != Time12Hour && p.TimeFormat != Time24Hour {
		return fmt.Errorf("time_format must be locale, 12h, or 24h")
	}
	if len(p.Favorites) > MaxFavorites {
		return fmt.Errorf("favorites may contain at most %d routes", MaxFavorites)
	}
	seen := make(map[string]struct{}, len(p.Favorites))
	for _, route := range p.Favorites {
		if route != strings.TrimSpace(route) {
			return errors.New("favorite routes must not contain surrounding whitespace")
		}
		if _, ok := allowedFavoriteRoutes[route]; !ok {
			return fmt.Errorf("favorite route %q is not available", route)
		}
		if _, duplicate := seen[route]; duplicate {
			return fmt.Errorf("favorite route %q is duplicated", route)
		}
		seen[route] = struct{}{}
	}
	return nil
}
