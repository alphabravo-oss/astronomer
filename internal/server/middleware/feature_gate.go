// Package middleware — feature-flag gating.
//
// Migration 046 added the platform_settings table; this middleware
// gates a route group on a single `feature.*` boolean key. When the
// flag is `false`, the wrapped handler returns 404 (not 403) — the
// operator's stance is "this feature is not present in this install"
// rather than "you can't use it". 403 would be wrong because:
//
//  1. Even a superuser sees 404. A 403 implies "ask for permission",
//     which is misleading when the surface is intentionally absent.
//  2. The frontend pre-fetches the branding/feature subset and hides
//     tabs on `false`; defense-in-depth on the API side renders any
//     stale frontend a clean 404 instead of a leaky 403.
//
// Cache: the per-request DB hit is avoided by holding the result in a
// process-local SettingsCache with a 30s TTL. Mutations via the
// platform_settings handler invalidate the cache key synchronously so
// the local process sees changes immediately; remote replicas pick
// them up on the next 30s expiry.
package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/platformsettings"
)

// FeatureFlagReader is the minimal cache surface FeatureGate needs.
// Implemented by *handler.SettingsCache in production; tests inject a
// fake that returns canned values without touching the DB.
type FeatureFlagReader interface {
	BoolValue(ctx context.Context, key string, fallback bool) bool
}

// SettingsRowReader is the optional DB surface used when the caller
// wires the gate without a cache (single-test path). Both shapes
// satisfy the broader handler.SettingsReader interface.
type SettingsRowReader interface {
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
}

// FeatureGate resolves its fallback from the shared setting registry. Unknown
// keys fail closed, which makes adding a route without registering its feature
// an immediately visible error instead of an accidental default-on release.
func FeatureGate(key string, reader FeatureFlagReader) func(http.Handler) http.Handler {
	fallback, registered := platformsettings.FeatureDefault(key)
	if !registered {
		fallback = false
	}
	return FeatureGateDefault(key, reader, fallback)
}

// FeatureGateDefault returns a chi middleware that 404s when the feature flag
// is false. A nil reader always fails closed: registry defaults apply only when
// the settings dependency is healthy enough to distinguish an absent row from
// an unavailable store.
func FeatureGateDefault(key string, reader FeatureFlagReader, fallback bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if reader != nil && reader.BoolValue(r.Context(), key, fallback) {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{
					"code":    "feature_disabled",
					"message": "This feature is not enabled on this Astronomer installation.",
				},
			})
		})
	}
}
