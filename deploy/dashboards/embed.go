// Package dashboards embeds the eight fleet Grafana JSON dashboards so the
// shared Grafana family can provision sidecar ConfigMaps without flipping
// metrics.dashboards.enabled on the Astronomer chart.
package dashboards

import "embed"

//go:embed *.json
var FS embed.FS
