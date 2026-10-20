package version

import (
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// FIPSMode is stamped to "true" by `make fips-build` (GOEXPERIMENT=boringcrypto,
// -tags fips). Default builds never link the FIPS module (guide ch.06).
var FIPSMode = "false"

// buildInfo is the guide ch.05 "Family 2" gauge:
// service_build_info{version,commit,build_time,go_version,fips_mode} = 1.
// It is registered on the default registry at process start so every binary
// (server, worker, agent) exposes it on its /metrics listener.
var buildInfo = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "service_build_info",
	Help: "Build identity of the running binary; always 1.",
}, []string{"version", "commit", "build_time", "go_version", "fips_mode"})

func init() {
	buildInfo.WithLabelValues(Version, GitCommit, BuildDate, runtime.Version(), FIPSMode).Set(1)
}

// IsFIPS reports whether this binary was produced by `make fips-build`.
func IsFIPS() bool { return FIPSMode == "true" }
