// Insecure development key exposure (dev-keys-default-and-silent).
//
//   - astronomer_insecure_dev_key_in_use{key}
//
// The chart used to ship a working JWT signing key and Fernet key as its
// defaults, and the runtime accepted both outside production without a word.
// Both the server and the worker now report the live state on every boot, in
// every environment, so an install that came up on a published key is visible
// as a log line, a gauge, and an alert (AstronomerInsecureDevKeyInUse) rather
// than only in this repository's git history.

package observability

import (
	"log/slog"
	"slices"

	"github.com/prometheus/client_golang/prometheus"
)

// insecureDevKeyNames is the closed set of keys the gauge reports on, so a boot
// that fixes one of them publishes a 0 instead of leaving a stale 1 behind.
// Must match the names returned by config.DevSentinelsInUse.
var insecureDevKeyNames = []string{"secret_key", "encryption_key"}

var insecureDevKeyInUse = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "astronomer",
		Name:      "insecure_dev_key_in_use",
		Help:      "1 when the named credential is still set to a published development sentinel value, 0 otherwise.",
	},
	MetricLabels("key"),
)

func init() {
	prometheus.MustRegister(insecureDevKeyInUse)
}

// ReportInsecureDevKeys sets the gauge for every known key and logs an ERROR
// naming the ones in use. keys are the names returned by
// config.DevSentinelsInUse; an empty slice clears the gauges and logs nothing.
func ReportInsecureDevKeys(log *slog.Logger, keys []string) {
	for _, name := range insecureDevKeyNames {
		v := 0.0
		if slices.Contains(keys, name) {
			v = 1.0
		}
		insecureDevKeyInUse.WithLabelValues(MetricValues(name)...).Set(v)
	}
	if len(keys) == 0 {
		return
	}
	Logger(log).Error("insecure development key in use; this install's tokens are forgeable and its stored credentials are readable by anyone with the chart",
		"keys", keys,
		"remediation", "rotate per docs/secret-rotation-runbook.md and reinstall with your own secrets.secretKey / secrets.encryptionKey",
	)
}
