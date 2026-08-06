package charliequalification

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"
)

// infrastructureQualificationOperator is deliberately package-private and is
// not accepted by LiveConfig. The production hook therefore cannot inject an
// observation provider and these scenarios remain fail-closed until a concrete
// fixed operator exists. Keeping the typed boundary here lets that operator be
// implemented without adding command, URL, or arbitrary evidence inputs to the
// hook. Tests use it only to prove validation and mandatory cleanup semantics.
type infrastructureQualificationOperator interface {
	LeaderKillFailover(context.Context, Candidate) (LeaderFailoverObservation, error)
	CleanInstall(context.Context, Candidate) (CleanInstallObservation, error)
	IsolationMatrix(context.Context, Candidate) (TenantIsolationObservation, error)
	ResilienceMatrix(context.Context, Candidate) (ResilienceObservation, error)
	UpgradeRollback(context.Context, Candidate) (UpgradeRollbackObservation, error)
	Restore(context.Context, string, Candidate) (RestorationObservation, error)
}

type CandidateObservation struct {
	Commit             string
	Version            string
	CentralImageDigest string
	AgentImageDigest   string
	CentralChartDigest string
	AgentChartDigest   string
}

type LeaderFailoverObservation struct {
	Candidate             CandidateObservation
	BaselineStateDigest   string
	LeaderBefore          string
	KilledInstance        string
	LeaderAfter           string
	EpochBefore           uint64
	EpochAfter            uint64
	KillStartedAt         time.Time
	ReplacementReadyAt    time.Time
	SSEConnectionID       string
	SSELastEventBefore    string
	SSEFirstEventAfter    string
	SSEResumed            bool
	ActionID              string
	ActionExecutionCount  uint64
	ActionCompletionCount uint64
}

type CleanInstallObservation struct {
	Candidate                 CandidateObservation
	BaselineStateDigest       string
	DatabaseID                string
	DatabaseCreatedAt         time.Time
	PreMigrationTableCount    uint64
	MigrationHead             string
	AppliedMigrationHead      string
	PGVectorExtensionVersion  string
	ObjectRoundTripDigest     string
	RegistryAuthenticationID  string
	RegistryPulledImageDigest string
	TLSVersion                uint16
	AdminPrincipalID          string
	AdminLoginStatus          int
	DevelopmentBypassCount    uint64
	Running                   CandidateObservation
}

type IsolationSubject struct {
	DeploymentID       string
	ProductID          string
	CredentialDigest   string
	SessionOwnerDigest string
	UsageOwnerDigest   string
	FindingOwnerDigest string
	AuditOwnerDigest   string
}

type CrossReadObservation struct {
	Domain           string
	FromDeploymentID string
	ToDeploymentID   string
	Status           int
	ReturnedRecords  uint64
}

type TenantIsolationObservation struct {
	Candidate           CandidateObservation
	BaselineStateDigest string
	Subjects            []IsolationSubject
	CrossReads          []CrossReadObservation
}

type ResilienceObservation struct {
	Candidate                       CandidateObservation
	BaselineStateDigest             string
	AuthorityBefore                 string
	AuthorityAfterRestart           string
	LeaderBefore                    string
	LeaderAfter                     string
	EpochBefore                     uint64
	EpochAfter                      uint64
	CentralOutageStatus             int
	CentralOutageWorkClaimsBefore   uint64
	CentralOutageWorkClaimsAfter    uint64
	ProductOutageStatus             int
	ProductOutageWorkClaimsBefore   uint64
	ProductOutageWorkClaimsAfter    uint64
	CredentialBeforeDigest          string
	CredentialAfterDigest           string
	StaleCredentialStatus           int
	RotatedCredentialStatus         int
	DisclosureBeforeDigest          string
	DisclosureAfterDigest           string
	AuthorityAfterDisclosureDrift   string
	PendingBeforeEmergencyDisable   uint64
	PendingAfterEmergencyDisable    uint64
	AuthorityAfterEmergencyDisable  string
	AuthorityAfterAutomaticRecovery string
	ExplicitEnableRequestID         string
	AuthorityAfterExplicitEnable    string
}

type UpgradeRollbackObservation struct {
	Candidate                      CandidateObservation
	BaselineStateDigest            string
	UpgradeRunning                 CandidateObservation
	RollbackPinned                 CandidateObservation
	RollbackRunning                CandidateObservation
	AuthorityBefore                string
	AuthorityAfterUpgrade          string
	AuthorityAfterRollback         string
	CredentialBeforeDigest         string
	CredentialAfterDigest          string
	StaleCredentialStatus          int
	RotatedCredentialStatus        int
	ExplicitReenableAttempted      bool
	AutomaticAuthorityRestoreCount uint64
}

type RestorationObservation struct {
	Scenario            string
	OriginalStateDigest string
	RestoredStateDigest string
	Safe                bool
	AuthorityBefore     string
	AuthorityAfter      string
	AuthorityRestored   bool
}

var infrastructureTimeout = map[string]time.Duration{
	"leader_kill_failover": 5 * time.Minute,
	"clean_install":        15 * time.Minute,
	"isolation_matrix":     10 * time.Minute,
	"resilience_matrix":    15 * time.Minute,
	"upgrade_rollback":     20 * time.Minute,
}

func (d *LiveDriver) infrastructureQualification(ctx context.Context, request ScenarioRequest) (result ScenarioResult) {
	if d.infrastructure == nil || !validCandidate(request.Candidate) {
		return Unsupported(request.Scenario)
	}
	before, err := d.Counters(ctx)
	if err != nil {
		return Unsupported(request.Scenario)
	}
	timeout, ok := infrastructureTimeout[request.Scenario]
	if !ok {
		return Unsupported(request.Scenario)
	}
	scenarioCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	baselineDigest := ""
	switch request.Scenario {
	case "leader_kill_failover":
		var observation LeaderFailoverObservation
		observation, err = d.infrastructure.LeaderKillFailover(scenarioCtx, request.Candidate)
		baselineDigest = observation.BaselineStateDigest
		if err == nil {
			result = validateLeaderFailover(request.Candidate, observation)
		}
	case "clean_install":
		var observation CleanInstallObservation
		observation, err = d.infrastructure.CleanInstall(scenarioCtx, request.Candidate)
		baselineDigest = observation.BaselineStateDigest
		if err == nil {
			result = validateCleanInstall(request.Candidate, observation)
		}
	case "isolation_matrix":
		var observation TenantIsolationObservation
		observation, err = d.infrastructure.IsolationMatrix(scenarioCtx, request.Candidate)
		baselineDigest = observation.BaselineStateDigest
		if err == nil {
			result = validateIsolation(request.Candidate, observation)
		}
	case "resilience_matrix":
		var observation ResilienceObservation
		observation, err = d.infrastructure.ResilienceMatrix(scenarioCtx, request.Candidate)
		baselineDigest = observation.BaselineStateDigest
		if err == nil {
			result = validateResilience(request.Candidate, observation)
		}
	case "upgrade_rollback":
		var observation UpgradeRollbackObservation
		observation, err = d.infrastructure.UpgradeRollback(scenarioCtx, request.Candidate)
		baselineDigest = observation.BaselineStateDigest
		if err == nil {
			result = validateUpgradeRollback(request.Candidate, observation)
		}
	}
	if err != nil {
		result = Unsupported(request.Scenario)
	}

	// Cleanup is mandatory even when collection or validation failed. A fresh,
	// bounded context prevents a cancelled client request from skipping restore.
	restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	restored, restoreErr := d.infrastructure.Restore(restoreCtx, request.Scenario, request.Candidate)
	restoreCancel()
	if restoreErr != nil || !validRestoration(request.Scenario, baselineDigest, restored) {
		return Unsupported(request.Scenario)
	}
	counterCtx, counterCancel := context.WithTimeout(context.Background(), 30*time.Second)
	after, counterErr := d.Counters(counterCtx)
	counterCancel()
	if counterErr != nil || !sameCounterKeys(before.Downstream, after.Downstream, downstreamKeys) {
		return Unsupported(request.Scenario)
	}
	if err != nil || !result.Passed {
		return Unsupported(request.Scenario)
	}
	return result
}

func validateLeaderFailover(candidate Candidate, value LeaderFailoverObservation) ScenarioResult {
	now := time.Now().UTC()
	validCandidate := candidateMatches(candidate, value.Candidate)
	identified := validEvidenceID(value.LeaderBefore) && value.KilledInstance == value.LeaderBefore
	killed := identified && value.KillStartedAt.UTC().Equal(value.KillStartedAt) && !value.KillStartedAt.IsZero() && value.KillStartedAt.After(now.Add(-5*time.Minute)) && !value.KillStartedAt.After(now.Add(time.Minute))
	replacement := validEvidenceID(value.LeaderAfter) && value.LeaderAfter != value.LeaderBefore && value.ReplacementReadyAt.After(value.KillStartedAt)
	bounded := replacement && value.ReplacementReadyAt.Sub(value.KillStartedAt) <= 2*time.Minute
	epoch := value.EpochBefore > 0 && value.EpochAfter > value.EpochBefore
	sse := validEvidenceID(value.SSEConnectionID) && validEvidenceID(value.SSELastEventBefore) && validEvidenceID(value.SSEFirstEventAfter) && value.SSEFirstEventAfter != value.SSELastEventBefore && value.SSEResumed
	unique := validEvidenceID(value.ActionID) && value.ActionExecutionCount == 1 && value.ActionCompletionCount == 1
	if !validCandidate || !validStateDigest(value.BaselineStateDigest) || !identified || !killed || !replacement || !bounded || !epoch || !sse || !unique {
		return Unsupported("leader_kill_failover")
	}
	return Passed("leader_kill_failover", "leader_identified", "leader_killed", "replacement_elected", "bounded_failover", "epoch_advanced", "sse_resumed", "no_duplicate_action")
}

func validateCleanInstall(candidate Candidate, value CleanInstallObservation) ScenarioResult {
	now := time.Now().UTC()
	fresh := validEvidenceID(value.DatabaseID) && !value.DatabaseCreatedAt.IsZero() && value.DatabaseCreatedAt.UTC().Equal(value.DatabaseCreatedAt) && value.DatabaseCreatedAt.After(now.Add(-20*time.Minute)) && !value.DatabaseCreatedAt.After(now.Add(time.Minute)) && value.PreMigrationTableCount == 0
	migrations := validEvidenceID(value.MigrationHead) && value.AppliedMigrationHead == value.MigrationHead
	pgvector := validSemverLike(value.PGVectorExtensionVersion)
	object := validStateDigest(value.ObjectRoundTripDigest)
	oci := validEvidenceID(value.RegistryAuthenticationID) && value.RegistryPulledImageDigest == candidate.CentralImageDigest
	tls := value.TLSVersion >= tlsVersion13
	admin := validEvidenceID(value.AdminPrincipalID) && value.AdminLoginStatus == http.StatusOK
	bypassAbsent := value.DevelopmentBypassCount == 0
	running := candidateMatches(candidate, value.Running)
	if !candidateMatches(candidate, value.Candidate) || !validStateDigest(value.BaselineStateDigest) || !fresh || !migrations || !pgvector || !object || !oci || !tls || !admin || !bypassAbsent || !running {
		return Unsupported("clean_install")
	}
	return Passed("clean_install", "fresh_database", "migrations_applied", "pgvector_ready", "object_storage_ready", "oci_authenticated", "tls_enforced", "admin_login_succeeded", "development_bypass_absent", "candidate_digests_running")
}

const tlsVersion13 uint16 = 0x0304

func validateIsolation(candidate Candidate, value TenantIsolationObservation) ScenarioResult {
	if !candidateMatches(candidate, value.Candidate) || !validStateDigest(value.BaselineStateDigest) || len(value.Subjects) != 3 {
		return Unsupported("isolation_matrix")
	}
	subjects := append([]IsolationSubject(nil), value.Subjects...)
	sort.Slice(subjects, func(i, j int) bool { return subjects[i].DeploymentID < subjects[j].DeploymentID })
	deployments, products, credentials := map[string]bool{}, map[string]int{}, map[string]bool{}
	sessionOwners, usageOwners, findingOwners, auditOwners := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, subject := range subjects {
		if !validEvidenceID(subject.DeploymentID) || !validEvidenceID(subject.ProductID) || !validStateDigest(subject.CredentialDigest) || !validStateDigest(subject.SessionOwnerDigest) || !validStateDigest(subject.UsageOwnerDigest) || !validStateDigest(subject.FindingOwnerDigest) || !validStateDigest(subject.AuditOwnerDigest) || deployments[subject.DeploymentID] || credentials[subject.CredentialDigest] || sessionOwners[subject.SessionOwnerDigest] || usageOwners[subject.UsageOwnerDigest] || findingOwners[subject.FindingOwnerDigest] || auditOwners[subject.AuditOwnerDigest] {
			return Unsupported("isolation_matrix")
		}
		deployments[subject.DeploymentID], credentials[subject.CredentialDigest] = true, true
		sessionOwners[subject.SessionOwnerDigest], usageOwners[subject.UsageOwnerDigest] = true, true
		findingOwners[subject.FindingOwnerDigest], auditOwners[subject.AuditOwnerDigest] = true, true
		products[subject.ProductID]++
	}
	if len(products) != 2 {
		return Unsupported("isolation_matrix")
	}
	hasPair := false
	for _, count := range products {
		hasPair = hasPair || count == 2
	}
	if !hasPair || !completeCrossReadMatrix(subjects, value.CrossReads) {
		return Unsupported("isolation_matrix")
	}
	return Passed("isolation_matrix", "two_deployments_same_product", "second_product_created", "credentials_isolated", "sessions_isolated", "usage_isolated", "findings_isolated", "audit_isolated", "cross_reads_denied")
}

func completeCrossReadMatrix(subjects []IsolationSubject, reads []CrossReadObservation) bool {
	domains := []string{"credentials", "sessions", "usage", "findings", "audit"}
	expected := make(map[string]bool, len(domains)*len(subjects)*(len(subjects)-1))
	for _, from := range subjects {
		for _, to := range subjects {
			if from.DeploymentID == to.DeploymentID {
				continue
			}
			for _, domain := range domains {
				expected[domain+"\x00"+from.DeploymentID+"\x00"+to.DeploymentID] = false
			}
		}
	}
	if len(reads) != len(expected) {
		return false
	}
	for _, read := range reads {
		key := read.Domain + "\x00" + read.FromDeploymentID + "\x00" + read.ToDeploymentID
		seen, exists := expected[key]
		if !exists || seen || (read.Status != http.StatusForbidden && read.Status != http.StatusNotFound) || read.ReturnedRecords != 0 {
			return false
		}
		expected[key] = true
	}
	for _, seen := range expected {
		if !seen {
			return false
		}
	}
	return true
}

func validateResilience(candidate Candidate, value ResilienceObservation) ScenarioResult {
	restart := value.AuthorityBefore == "read_only" && value.AuthorityAfterRestart == "read_only"
	leader := validEvidenceID(value.LeaderBefore) && validEvidenceID(value.LeaderAfter) && value.LeaderBefore != value.LeaderAfter && value.EpochBefore > 0 && value.EpochAfter > value.EpochBefore
	central := failedClosedStatus(value.CentralOutageStatus) && value.CentralOutageWorkClaimsBefore == value.CentralOutageWorkClaimsAfter
	product := failedClosedStatus(value.ProductOutageStatus) && value.ProductOutageWorkClaimsBefore == value.ProductOutageWorkClaimsAfter
	rotation := validStateDigest(value.CredentialBeforeDigest) && validStateDigest(value.CredentialAfterDigest) && value.CredentialBeforeDigest != value.CredentialAfterDigest && value.StaleCredentialStatus == http.StatusUnauthorized && value.RotatedCredentialStatus == http.StatusOK
	disclosure := validStateDigest(value.DisclosureBeforeDigest) && validStateDigest(value.DisclosureAfterDigest) && value.DisclosureBeforeDigest != value.DisclosureAfterDigest && value.AuthorityAfterDisclosureDrift == "disabled"
	emergency := value.PendingBeforeEmergencyDisable > 0 && value.PendingAfterEmergencyDisable == 0 && value.AuthorityAfterEmergencyDisable == "disabled"
	recovery := value.AuthorityAfterAutomaticRecovery == "disabled" && validEvidenceID(value.ExplicitEnableRequestID) && value.AuthorityAfterExplicitEnable == "read_only"
	if !candidateMatches(candidate, value.Candidate) || !validStateDigest(value.BaselineStateDigest) || !restart || !leader || !central || !product || !rotation || !disclosure || !emergency || !recovery {
		return Unsupported("resilience_matrix")
	}
	return Passed("resilience_matrix", "restart_authority_not_increased", "leader_loss_recovered", "central_outage_failed_closed", "product_outage_failed_closed", "credential_rotation_converged", "disclosure_drift_disabled", "emergency_disable_drained", "recovery_required_explicit_enable")
}

func validateUpgradeRollback(candidate Candidate, value UpgradeRollbackObservation) ScenarioResult {
	upgrade := candidateMatches(candidate, value.Candidate) && candidateMatches(candidate, value.UpgradeRunning)
	rollback := validCandidateObservation(value.RollbackPinned) && validCandidateObservation(value.RollbackRunning) && value.RollbackPinned == value.RollbackRunning && value.RollbackPinned != value.UpgradeRunning
	authority := value.AuthorityBefore == "read_only" && value.AuthorityAfterUpgrade == "disabled" && value.AuthorityAfterRollback == "disabled" && value.AutomaticAuthorityRestoreCount == 0
	stale := validStateDigest(value.CredentialBeforeDigest) && validStateDigest(value.CredentialAfterDigest) && value.CredentialBeforeDigest != value.CredentialAfterDigest && value.StaleCredentialStatus == http.StatusUnauthorized && value.RotatedCredentialStatus == http.StatusOK
	explicit := !value.ExplicitReenableAttempted
	if !validStateDigest(value.BaselineStateDigest) || !upgrade || !rollback || !authority || !stale || !explicit {
		return Unsupported("upgrade_rollback")
	}
	return Passed("upgrade_rollback", "upgrade_candidate_pinned", "rollback_candidate_pinned", "authority_not_restored", "stale_credentials_rejected", "explicit_reenable_required")
}

func validRestoration(scenario, baseline string, value RestorationObservation) bool {
	if value.Scenario != scenario || !value.Safe || !validStateDigest(baseline) || value.OriginalStateDigest != baseline || !validStateDigest(value.RestoredStateDigest) {
		return false
	}
	if scenario == "upgrade_rollback" {
		return value.AuthorityBefore == "read_only" && value.AuthorityAfter == "disabled" && !value.AuthorityRestored
	}
	return value.RestoredStateDigest == baseline && value.AuthorityAfter == value.AuthorityBefore && !value.AuthorityRestored
}

func candidateMatches(candidate Candidate, value CandidateObservation) bool {
	return value.Commit == candidate.Commit && value.Version == candidate.Version && value.CentralImageDigest == candidate.CentralImageDigest && value.AgentImageDigest == candidate.AgentImageDigest && value.CentralChartDigest == candidate.CentralChartDigest && value.AgentChartDigest == candidate.AgentChartDigest
}

func validCandidateObservation(value CandidateObservation) bool {
	return commitPattern.MatchString(value.Commit) && versionPattern.MatchString(value.Version) && digestPattern.MatchString(value.CentralImageDigest) && digestPattern.MatchString(value.AgentImageDigest) && digestPattern.MatchString(value.CentralChartDigest) && digestPattern.MatchString(value.AgentChartDigest)
}

func validStateDigest(value string) bool { return digestPattern.MatchString(value) }

func validEvidenceID(value string) bool { return fixtureIDPattern.MatchString(value) }

func validSemverLike(value string) bool {
	parts := strings.Split(strings.TrimPrefix(value, "v"), ".")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}

func failedClosedStatus(status int) bool {
	return status == http.StatusServiceUnavailable || status == http.StatusBadGateway || status == http.StatusGatewayTimeout
}
