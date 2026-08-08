package charliequalification

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func qualificationCandidate() Candidate {
	return Candidate{
		Ref: "qualification-candidate", Commit: repeatHex('a'), Version: "1.2.3",
		CentralImageDigest: digestOf('1'), AgentImageDigest: digestOf('2'),
		CentralChartDigest: digestOf('3'), AgentChartDigest: digestOf('4'),
	}
}

func observedCandidate(candidate Candidate) CandidateObservation {
	return CandidateObservation{Commit: candidate.Commit, Version: candidate.Version, CentralImageDigest: candidate.CentralImageDigest, AgentImageDigest: candidate.AgentImageDigest, CentralChartDigest: candidate.CentralChartDigest, AgentChartDigest: candidate.AgentChartDigest}
}

func repeatHex(character byte) string { return string(makeFilled(40, character)) }
func digestOf(character byte) string  { return "sha256:" + string(makeFilled(64, character)) }
func makeFilled(count int, character byte) []byte {
	value := make([]byte, count)
	for index := range value {
		value[index] = character
	}
	return value
}

func validLeaderObservation(candidate Candidate) LeaderFailoverObservation {
	start := time.Now().UTC().Add(-time.Minute)
	return LeaderFailoverObservation{
		Candidate: observedCandidate(candidate), BaselineStateDigest: digestOf('5'),
		LeaderBefore: "leader-a", KilledInstance: "leader-a", LeaderAfter: "leader-b",
		EpochBefore: 7, EpochAfter: 8, KillStartedAt: start, ReplacementReadyAt: start.Add(30 * time.Second),
		SSEConnectionID: "sse-connection", SSELastEventBefore: "event-100", SSEFirstEventAfter: "event-101", SSEResumed: true,
		ActionID: "action-one", ActionExecutionCount: 1, ActionCompletionCount: 1,
	}
}

func TestLeaderFailoverRequiresEpochSSEAndActionUniqueness(t *testing.T) {
	candidate := qualificationCandidate()
	tests := []struct {
		name   string
		mutate func(*LeaderFailoverObservation)
	}{
		{"candidate", func(v *LeaderFailoverObservation) { v.Candidate.AgentImageDigest = digestOf('9') }},
		{"killed leader", func(v *LeaderFailoverObservation) { v.KilledInstance = "leader-c" }},
		{"replacement", func(v *LeaderFailoverObservation) { v.LeaderAfter = v.LeaderBefore }},
		{"bounded", func(v *LeaderFailoverObservation) { v.ReplacementReadyAt = v.KillStartedAt.Add(3 * time.Minute) }},
		{"epoch", func(v *LeaderFailoverObservation) { v.EpochAfter = v.EpochBefore }},
		{"sse resume", func(v *LeaderFailoverObservation) { v.SSEResumed = false }},
		{"sse event progression", func(v *LeaderFailoverObservation) { v.SSEFirstEventAfter = v.SSELastEventBefore }},
		{"duplicate execution", func(v *LeaderFailoverObservation) { v.ActionExecutionCount = 2 }},
		{"duplicate completion", func(v *LeaderFailoverObservation) { v.ActionCompletionCount = 2 }},
	}
	if result := validateLeaderFailover(candidate, validLeaderObservation(candidate)); !result.Passed || len(result.Assertions) != 7 {
		t.Fatalf("valid failover rejected: %#v", result)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validLeaderObservation(candidate)
			test.mutate(&value)
			if result := validateLeaderFailover(candidate, value); result.Passed {
				t.Fatalf("false failover evidence passed: %#v", result)
			}
		})
	}
}

func validCleanInstallObservation(candidate Candidate) CleanInstallObservation {
	return CleanInstallObservation{
		Candidate: observedCandidate(candidate), BaselineStateDigest: digestOf('5'), DatabaseID: "database-fresh", DatabaseCreatedAt: time.Now().UTC(),
		PreMigrationTableCount: 0, MigrationHead: "migration-20260806", AppliedMigrationHead: "migration-20260806", PGVectorExtensionVersion: "0.8.0",
		ObjectRoundTripDigest: digestOf('6'), RegistryAuthenticationID: "registry-auth-attempt", RegistryPulledImageDigest: candidate.CentralImageDigest,
		TLSVersion: tlsVersion13, AdminPrincipalID: "fresh-admin", AdminLoginStatus: http.StatusOK, DevelopmentBypassCount: 0, Running: observedCandidate(candidate),
	}
}

func TestCleanInstallRejectsIncompleteOrSubstitutedEvidence(t *testing.T) {
	candidate := qualificationCandidate()
	tests := []struct {
		name   string
		mutate func(*CleanInstallObservation)
	}{
		{"existing tables", func(v *CleanInstallObservation) { v.PreMigrationTableCount = 1 }},
		{"migration mismatch", func(v *CleanInstallObservation) { v.AppliedMigrationHead = "other" }},
		{"pgvector absent", func(v *CleanInstallObservation) { v.PGVectorExtensionVersion = "" }},
		{"object storage absent", func(v *CleanInstallObservation) { v.ObjectRoundTripDigest = "" }},
		{"wrong oci image", func(v *CleanInstallObservation) { v.RegistryPulledImageDigest = digestOf('9') }},
		{"weak tls", func(v *CleanInstallObservation) { v.TLSVersion = 0x0303 }},
		{"login failed", func(v *CleanInstallObservation) { v.AdminLoginStatus = http.StatusUnauthorized }},
		{"development bypass", func(v *CleanInstallObservation) { v.DevelopmentBypassCount = 1 }},
		{"wrong running digest", func(v *CleanInstallObservation) { v.Running.CentralChartDigest = digestOf('9') }},
	}
	if result := validateCleanInstall(candidate, validCleanInstallObservation(candidate)); !result.Passed || len(result.Assertions) != 9 {
		t.Fatalf("valid clean install rejected: %#v", result)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validCleanInstallObservation(candidate)
			test.mutate(&value)
			if validateCleanInstall(candidate, value).Passed {
				t.Fatal("false clean-install evidence passed")
			}
		})
	}
}

func validIsolationObservation(candidate Candidate) TenantIsolationObservation {
	subjects := []IsolationSubject{
		{DeploymentID: "deployment-a", ProductID: "product-one", CredentialDigest: digestOf('5'), SessionOwnerDigest: digestOf('6'), UsageOwnerDigest: digestOf('7'), FindingOwnerDigest: digestOf('8'), AuditOwnerDigest: digestOf('9')},
		{DeploymentID: "deployment-b", ProductID: "product-one", CredentialDigest: digestOf('a'), SessionOwnerDigest: digestOf('b'), UsageOwnerDigest: digestOf('c'), FindingOwnerDigest: digestOf('d'), AuditOwnerDigest: digestOf('e')},
		{DeploymentID: "deployment-c", ProductID: "product-two", CredentialDigest: digestOf('f'), SessionOwnerDigest: digestOf('0'), UsageOwnerDigest: digestOf('1'), FindingOwnerDigest: digestOf('2'), AuditOwnerDigest: digestOf('3')},
	}
	reads := make([]CrossReadObservation, 0, 30)
	for _, from := range subjects {
		for _, to := range subjects {
			if from.DeploymentID == to.DeploymentID {
				continue
			}
			for _, domain := range []string{"credentials", "sessions", "usage", "findings", "audit"} {
				reads = append(reads, CrossReadObservation{Domain: domain, FromDeploymentID: from.DeploymentID, ToDeploymentID: to.DeploymentID, Status: http.StatusForbidden})
			}
		}
	}
	return TenantIsolationObservation{Candidate: observedCandidate(candidate), BaselineStateDigest: digestOf('4'), Subjects: subjects, CrossReads: reads}
}

func TestIsolationRequiresUniqueOwnershipAndCompleteCrossReadDenial(t *testing.T) {
	candidate := qualificationCandidate()
	valid := validIsolationObservation(candidate)
	if result := validateIsolation(candidate, valid); !result.Passed || len(result.Assertions) != 8 {
		t.Fatalf("valid isolation matrix rejected: %#v", result)
	}
	tests := []struct {
		name   string
		mutate func(*TenantIsolationObservation)
	}{
		{"credential reused", func(v *TenantIsolationObservation) { v.Subjects[1].CredentialDigest = v.Subjects[0].CredentialDigest }},
		{"session owner reused", func(v *TenantIsolationObservation) {
			v.Subjects[1].SessionOwnerDigest = v.Subjects[0].SessionOwnerDigest
		}},
		{"usage owner reused", func(v *TenantIsolationObservation) { v.Subjects[1].UsageOwnerDigest = v.Subjects[0].UsageOwnerDigest }},
		{"finding owner reused", func(v *TenantIsolationObservation) {
			v.Subjects[1].FindingOwnerDigest = v.Subjects[0].FindingOwnerDigest
		}},
		{"audit owner reused", func(v *TenantIsolationObservation) { v.Subjects[1].AuditOwnerDigest = v.Subjects[0].AuditOwnerDigest }},
		{"cross read allowed", func(v *TenantIsolationObservation) {
			v.CrossReads[0].Status = http.StatusOK
			v.CrossReads[0].ReturnedRecords = 1
		}},
		{"cross read omitted", func(v *TenantIsolationObservation) { v.CrossReads = v.CrossReads[1:] }},
		{"cross read duplicated", func(v *TenantIsolationObservation) { v.CrossReads[1] = v.CrossReads[0] }},
		{"no second product", func(v *TenantIsolationObservation) { v.Subjects[2].ProductID = v.Subjects[0].ProductID }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validIsolationObservation(candidate)
			test.mutate(&value)
			if validateIsolation(candidate, value).Passed {
				t.Fatal("false isolation evidence passed")
			}
		})
	}
}

func validResilienceObservation(candidate Candidate) ResilienceObservation {
	return ResilienceObservation{
		Candidate: observedCandidate(candidate), BaselineStateDigest: digestOf('5'), AuthorityBefore: "read_only", AuthorityAfterRestart: "read_only",
		LeaderBefore: "leader-a", LeaderAfter: "leader-b", EpochBefore: 11, EpochAfter: 12,
		CentralOutageStatus: http.StatusServiceUnavailable, CentralOutageWorkClaimsBefore: 4, CentralOutageWorkClaimsAfter: 4,
		ProductOutageStatus: http.StatusBadGateway, ProductOutageWorkClaimsBefore: 4, ProductOutageWorkClaimsAfter: 4,
		CredentialBeforeDigest: digestOf('6'), CredentialAfterDigest: digestOf('7'), StaleCredentialStatus: http.StatusUnauthorized, RotatedCredentialStatus: http.StatusOK,
		DisclosureBeforeDigest: digestOf('8'), DisclosureAfterDigest: digestOf('9'), AuthorityAfterDisclosureDrift: "disabled",
		PendingBeforeEmergencyDisable: 2, PendingAfterEmergencyDisable: 0, AuthorityAfterEmergencyDisable: "disabled",
		AuthorityAfterAutomaticRecovery: "disabled", ExplicitEnableRequestID: "explicit-enable-request", AuthorityAfterExplicitEnable: "read_only",
	}
}

func TestResilienceRequiresEveryFailClosedTransition(t *testing.T) {
	candidate := qualificationCandidate()
	if result := validateResilience(candidate, validResilienceObservation(candidate)); !result.Passed || len(result.Assertions) != 8 {
		t.Fatalf("valid resilience matrix rejected: %#v", result)
	}
	tests := []struct {
		name   string
		mutate func(*ResilienceObservation)
	}{
		{"restart escalated", func(v *ResilienceObservation) { v.AuthorityAfterRestart = "automated" }},
		{"leader epoch stale", func(v *ResilienceObservation) { v.EpochAfter = v.EpochBefore }},
		{"central outage claimed work", func(v *ResilienceObservation) { v.CentralOutageWorkClaimsAfter++ }},
		{"product outage accepted", func(v *ResilienceObservation) { v.ProductOutageStatus = http.StatusOK }},
		{"old credential accepted", func(v *ResilienceObservation) { v.StaleCredentialStatus = http.StatusOK }},
		{"new credential rejected", func(v *ResilienceObservation) { v.RotatedCredentialStatus = http.StatusUnauthorized }},
		{"disclosure stayed enabled", func(v *ResilienceObservation) { v.AuthorityAfterDisclosureDrift = "read_only" }},
		{"emergency left pending work", func(v *ResilienceObservation) { v.PendingAfterEmergencyDisable = 1 }},
		{"automatic recovery enabled", func(v *ResilienceObservation) { v.AuthorityAfterAutomaticRecovery = "read_only" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validResilienceObservation(candidate)
			test.mutate(&value)
			if validateResilience(candidate, value).Passed {
				t.Fatal("false resilience evidence passed")
			}
		})
	}
}

func validUpgradeRollbackObservation(candidate Candidate) UpgradeRollbackObservation {
	rollback := observedCandidate(candidate)
	rollback.Commit = repeatHex('b')
	rollback.Version = "1.2.2"
	rollback.CentralImageDigest = digestOf('6')
	rollback.AgentImageDigest = digestOf('7')
	rollback.CentralChartDigest = digestOf('8')
	rollback.AgentChartDigest = digestOf('9')
	return UpgradeRollbackObservation{
		Candidate: observedCandidate(candidate), BaselineStateDigest: digestOf('5'), UpgradeRunning: observedCandidate(candidate), RollbackPinned: rollback, RollbackRunning: rollback,
		AuthorityBefore: "read_only", AuthorityAfterUpgrade: "disabled", AuthorityAfterRollback: "disabled",
		CredentialBeforeDigest: digestOf('a'), CredentialAfterDigest: digestOf('b'), StaleCredentialStatus: http.StatusUnauthorized, RotatedCredentialStatus: http.StatusOK,
		ExplicitReenableAttempted: false, AutomaticAuthorityRestoreCount: 0,
	}
}

func TestUpgradeRollbackNeverRestoresAuthorityOrStaleCredentials(t *testing.T) {
	candidate := qualificationCandidate()
	if result := validateUpgradeRollback(candidate, validUpgradeRollbackObservation(candidate)); !result.Passed || len(result.Assertions) != 5 {
		t.Fatalf("valid upgrade rollback rejected: %#v", result)
	}
	tests := []struct {
		name   string
		mutate func(*UpgradeRollbackObservation)
	}{
		{"upgrade substituted", func(v *UpgradeRollbackObservation) { v.UpgradeRunning.AgentImageDigest = digestOf('0') }},
		{"rollback unpinned", func(v *UpgradeRollbackObservation) { v.RollbackRunning.Commit = repeatHex('c') }},
		{"authority after upgrade", func(v *UpgradeRollbackObservation) { v.AuthorityAfterUpgrade = "read_only" }},
		{"authority after rollback", func(v *UpgradeRollbackObservation) { v.AuthorityAfterRollback = "read_only" }},
		{"automatic authority restore", func(v *UpgradeRollbackObservation) { v.AutomaticAuthorityRestoreCount = 1 }},
		{"stale credential accepted", func(v *UpgradeRollbackObservation) { v.StaleCredentialStatus = http.StatusOK }},
		{"rotated credential rejected", func(v *UpgradeRollbackObservation) { v.RotatedCredentialStatus = http.StatusUnauthorized }},
		{"implicit reenable", func(v *UpgradeRollbackObservation) { v.ExplicitReenableAttempted = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validUpgradeRollbackObservation(candidate)
			test.mutate(&value)
			if validateUpgradeRollback(candidate, value).Passed {
				t.Fatal("false upgrade/rollback evidence passed")
			}
		})
	}

	validRestore := RestorationObservation{Scenario: "upgrade_rollback", OriginalStateDigest: digestOf('5'), RestoredStateDigest: digestOf('6'), Safe: true, AuthorityBefore: "read_only", AuthorityAfter: "disabled"}
	if !validRestoration("upgrade_rollback", digestOf('5'), validRestore) {
		t.Fatal("safe disabled rollback restoration rejected")
	}
	validRestore.AuthorityAfter = "read_only"
	validRestore.AuthorityRestored = true
	if validRestoration("upgrade_rollback", digestOf('5'), validRestore) {
		t.Fatal("upgrade cleanup restored authority without explicit enable")
	}
}

func TestInfrastructureRestorationIsExactAndFailClosed(t *testing.T) {
	baseline := digestOf('5')
	for _, scenario := range []string{"leader_kill_failover", "clean_install", "isolation_matrix", "resilience_matrix"} {
		t.Run(scenario, func(t *testing.T) {
			valid := RestorationObservation{Scenario: scenario, OriginalStateDigest: baseline, RestoredStateDigest: baseline, Safe: true, AuthorityBefore: "read_only", AuthorityAfter: "read_only"}
			if !validRestoration(scenario, baseline, valid) {
				t.Fatal("exact safe restoration rejected")
			}
			mutations := []func(*RestorationObservation){
				func(v *RestorationObservation) { v.Scenario = "upgrade_rollback" },
				func(v *RestorationObservation) { v.OriginalStateDigest = digestOf('6') },
				func(v *RestorationObservation) { v.RestoredStateDigest = digestOf('6') },
				func(v *RestorationObservation) { v.Safe = false },
				func(v *RestorationObservation) { v.AuthorityAfter = "approval" },
				func(v *RestorationObservation) { v.AuthorityRestored = true },
			}
			for index, mutate := range mutations {
				value := valid
				mutate(&value)
				if validRestoration(scenario, baseline, value) {
					t.Fatalf("invalid restoration mutation %d passed", index)
				}
			}
		})
	}
}

type fakeInfrastructureOperator struct {
	leader       LeaderFailoverObservation
	leaderErr    error
	restore      RestorationObservation
	restoreErr   error
	restoreCalls int
}

func (f *fakeInfrastructureOperator) LeaderKillFailover(context.Context, Candidate) (LeaderFailoverObservation, error) {
	return f.leader, f.leaderErr
}
func (*fakeInfrastructureOperator) CleanInstall(context.Context, Candidate) (CleanInstallObservation, error) {
	return CleanInstallObservation{}, fmt.Errorf("not configured")
}
func (*fakeInfrastructureOperator) IsolationMatrix(context.Context, Candidate) (TenantIsolationObservation, error) {
	return TenantIsolationObservation{}, fmt.Errorf("not configured")
}
func (*fakeInfrastructureOperator) ResilienceMatrix(context.Context, Candidate) (ResilienceObservation, error) {
	return ResilienceObservation{}, fmt.Errorf("not configured")
}
func (*fakeInfrastructureOperator) UpgradeRollback(context.Context, Candidate) (UpgradeRollbackObservation, error) {
	return UpgradeRollbackObservation{}, fmt.Errorf("not configured")
}
func (f *fakeInfrastructureOperator) Restore(context.Context, string, Candidate) (RestorationObservation, error) {
	f.restoreCalls++
	return f.restore, f.restoreErr
}

func TestInfrastructureDriverAlwaysRestoresAndFailsClosed(t *testing.T) {
	candidate := qualificationCandidate()
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for _, key := range runtimeKeys {
			_, _ = fmt.Fprintf(w, "%s 0\n", defaultCounterMetrics()[key])
		}
		for _, operation := range []string{"kubernetes", "exec", "logs", "helm"} {
			_, _ = fmt.Fprintf(w, "astronomer_charlie_downstream_boundary_calls_total{entrypoint=%q,operation=%q} 0\n", "other", operation)
		}
		_, _ = fmt.Fprintln(w, `astronomer_charlie_downstream_boundary_calls_total{entrypoint="tunnel_message",operation="other"} 0`)
		_, _ = fmt.Fprintln(w, `astronomer_charlie_downstream_boundary_calls_total{entrypoint="kubernetes_proxy",operation="other"} 0`)
	}))
	defer metrics.Close()
	operator := &fakeInfrastructureOperator{
		leader:  validLeaderObservation(candidate),
		restore: RestorationObservation{Scenario: "leader_kill_failover", OriginalStateDigest: digestOf('5'), RestoredStateDigest: digestOf('5'), Safe: true, AuthorityBefore: "read_only", AuthorityAfter: "read_only"},
	}
	driver, err := NewLiveDriver(LiveConfig{AstronomerURL: metrics.URL, AdminToken: "admin", AllowHTTP: true, MetricSources: []MetricSource{{URL: metrics.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	// No production constructor accepts this test double. A real hook remains
	// Unsupported until a fixed operator is compiled and wired by main.
	driver.infrastructure = operator
	request := ScenarioRequest{Scenario: "leader_kill_failover", Candidate: candidate}
	if result := driver.Run(t.Context(), request); !result.Passed || operator.restoreCalls != 1 {
		t.Fatalf("live typed proof did not pass and restore: result=%#v restore=%d", result, operator.restoreCalls)
	}
	operator.leaderErr = fmt.Errorf("collection failed")
	operator.restoreCalls = 0
	if result := driver.Run(t.Context(), request); result.Passed || operator.restoreCalls != 1 {
		t.Fatalf("collection failure skipped restore or passed: result=%#v restore=%d", result, operator.restoreCalls)
	}
	operator.leaderErr = nil
	operator.restore.Safe = false
	operator.restoreCalls = 0
	if result := driver.Run(t.Context(), request); result.Passed || operator.restoreCalls != 1 {
		t.Fatalf("unsafe restoration passed: result=%#v restore=%d", result, operator.restoreCalls)
	}
}

func TestInfrastructureScenariosRequireOperator(t *testing.T) {
	if _, exposed := reflect.TypeOf(LiveConfig{}).FieldByName("Infrastructure"); exposed {
		t.Fatal("production LiveConfig exposes an infrastructure evidence injection surface")
	}
	driver := &LiveDriver{}
	for _, scenario := range []string{"leader_kill_failover", "clean_install", "isolation_matrix", "resilience_matrix", "upgrade_rollback"} {
		if result := driver.Run(t.Context(), ScenarioRequest{Scenario: scenario, Candidate: qualificationCandidate()}); result.Passed || len(result.Assertions) != 0 {
			t.Fatalf("%s passed without an operator: %#v", scenario, result)
		}
	}
}
