package main

import (
	"net/http"
	"time"
)

type config struct {
	realEstate        string
	checkOnly         bool
	warmup            time.Duration
	estateMixedFlags  []string
	workloadClient    *http.Client
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
