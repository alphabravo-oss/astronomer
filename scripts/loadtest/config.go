package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type config struct {
	compareMode       bool
	compareBaseline   string
	compareCandidate  string
	compareImages     string
	realEstate        string
	checkOnly         bool
	warmup            time.Duration
	estateMixedFlags  []string
	workloadClient    *http.Client
	workloadRequest   func(context.Context, uint64)
	workloadRPS       float64
	server            string
	metricsServer     string
	clusters          int
	rps               int
	duration          time.Duration
	tokenPath         string
	loginEmail        string
	loginPasswordPath string
	outPath           string
	verbose           bool
	skipAgents        bool // dev convenience — disable WS dial entirely
	keepFixtures      bool // debug convenience — retain API-created cluster rows
	certification     bool
	validateDrills    bool
	profilePath       string
	profileName       string
	resources         scaleResources
	reconnectStorm    reconnectStormConfig
	day2FailureDrill  []string
	fixtureClusterIDs []string
	mandatoryAudit    mandatoryAuditProfile
	auditObserverPath string
}

func parseFlags() *config {
	cfg := &config{}
	flag.StringVar(&cfg.server, "server", envOr("LOADTEST_SERVER", defaultServer), "management-plane base URL")
	flag.StringVar(&cfg.metricsServer, "metrics-server", envOr("LOADTEST_METRICS_SERVER", ""), "Prometheus metrics base URL (defaults to -server)")
	flag.IntVar(&cfg.clusters, "clusters", envOrInt("LOADTEST_CLUSTERS", defaultClusters), "number of synthetic agents to spawn")
	flag.IntVar(&cfg.rps, "rps", envOrInt("LOADTEST_RPS", defaultRPS), "aggregate HTTP request rate (per second)")
	flag.DurationVar(&cfg.duration, "duration", envOrDuration("LOADTEST_DURATION", defaultDuration), "how long to run")
	flag.StringVar(&cfg.tokenPath, "token", envOr("LOADTEST_TOKEN", ""), "path to a file holding an admin API bearer token")
	flag.StringVar(&cfg.loginEmail, "login-email", envOr("LOADTEST_LOGIN_EMAIL", ""), "local engineering only: email used to mint an ephemeral API token")
	flag.StringVar(&cfg.loginPasswordPath, "login-password-file", envOr("LOADTEST_LOGIN_PASSWORD_FILE", ""), "local engineering only: path or file descriptor containing the login password")
	flag.StringVar(&cfg.outPath, "out", envOr("LOADTEST_OUT", defaultOut), "where to write the markdown report")
	flag.StringVar(&cfg.profilePath, "profile", envOr("LOADTEST_PROFILE", ""), "optional YAML scale profile path")
	flag.StringVar(&cfg.auditObserverPath, "audit-observer-dsn", envOr("LOADTEST_AUDIT_OBSERVER_DATABASE_URL_FILE", ""), "path to a read-only PostgreSQL DSN used to independently observe durable audit outbox intents")
	flag.BoolVar(&cfg.verbose, "verbose", envOrBool("LOADTEST_VERBOSE", false), "log at debug level")
	flag.BoolVar(&cfg.skipAgents, "skip-agents", envOrBool("LOADTEST_SKIP_AGENTS", false), "do not dial synthetic agent WS — HTTP workload only")
	flag.BoolVar(&cfg.keepFixtures, "keep-fixtures", envOrBool("LOADTEST_KEEP_FIXTURES", false), "retain provisioned cluster fixtures for debugging")
	flag.BoolVar(&cfg.certification, "certification", envOrBool("LOADTEST_CERTIFICATION", false), "require reproducibility metadata and passing day-2 drill evidence")
	flag.BoolVar(&cfg.validateDrills, "validate-drill-evidence", false, "validate configured drill evidence provenance without running a load test")
	registerEstateFlags(cfg)
	registerEstateComparisonFlags(cfg)
	flag.Parse()
	cfg.compareMode = estateComparisonFlagPresent(flag.CommandLine)
	if estateComparisonRequested(cfg) {
		if err := validateEstateComparisonFlags(cfg, flag.CommandLine, os.Environ()); err != nil {
			fmt.Fprintln(os.Stderr, "offline estate comparison configuration rejected")
			os.Exit(1)
		}
		return cfg
	}
	collectEstateMixedFlags(cfg)
	if strings.TrimSpace(cfg.metricsServer) == "" {
		cfg.metricsServer = cfg.server
	}
	if cfg.profilePath != "" {
		profile, err := loadScaleProfile(cfg.profilePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load profile: %v\n", err)
			os.Exit(1)
		}
		if err := profile.apply(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "apply profile: %v\n", err)
			os.Exit(1)
		}
	}
	if cfg.resources.PodsPerCluster == 0 {
		cfg.resources = scaleResources{PodsPerCluster: 42, DeploymentsPerCluster: 10, ServicesPerCluster: 10}
	}
	return cfg
}
