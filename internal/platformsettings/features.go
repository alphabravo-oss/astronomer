// Package platformsettings owns platform-setting keys and defaults that are
// consumed outside the HTTP handler package.
package platformsettings

const (
	FeatureCatalog               = "feature.catalog"
	FeatureProjects              = "feature.projects"
	FeatureMonitoring            = "feature.monitoring"
	FeatureSharedGrafana         = "feature.shared_grafana"
	FeatureHostedLoki            = "feature.hosted_loki"
	FeatureSecurity              = "feature.security"
	FeatureBackups               = "feature.backups"
	FeatureDelivery              = "feature.delivery"
	FeatureAlerting              = "feature.alerting"
	FeatureCharlie               = "feature.charlie"
	FeatureExtensions            = "feature.extensions"
	FeatureControlPlaneSnapshots = "feature.control_plane_snapshots"
)

var featureDefaults = map[string]bool{
	FeatureCatalog:               true,
	FeatureProjects:              true,
	FeatureMonitoring:            true,
	FeatureSharedGrafana:         true,
	FeatureHostedLoki:            false,
	FeatureSecurity:              true,
	FeatureBackups:               true,
	FeatureDelivery:              true,
	FeatureAlerting:              true,
	FeatureCharlie:               false,
	FeatureExtensions:            false,
	FeatureControlPlaneSnapshots: false,
}

// FeatureDefault returns the registered default. Unknown feature keys fail
// closed instead of silently shipping a new route as enabled.
func FeatureDefault(key string) (bool, bool) {
	value, ok := featureDefaults[key]
	return value, ok
}
