package version

// Set via ldflags at build time from the git tag (the release workflow stamps
// VERSION/GIT_COMMIT/BUILD_DATE into every binary). This default is the single
// source of truth for UN-released dev builds: bump it when the dev cycle moves
// to the next version. Keep it in sync with deploy/chart/Chart.yaml appVersion.
var (
	Version   = "0.2.0-dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)
