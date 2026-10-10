package scanner

import (
	"strings"
	"testing"
	"time"
)

func freshnessFixture(now time.Time) (DatabaseFreshnessPolicy, ScanTargetBinding, ScanUseObservation, AcceptedDatabaseState) {
	policy := DatabaseFreshnessPolicy{ReportTTL: 6 * time.Hour, MainMaximumAge: 30 * time.Hour, JavaMaximumAge: 48 * time.Hour, ClockSkew: 5 * time.Minute}
	digest := "sha256:" + strings.Repeat("a", 64)
	main := UsedDatabaseObservation{SubjectDigest: digest, LayerDigest: digest, MetadataDigest: digest, Schema: 2, UpdatedAt: now.Add(-time.Hour), NextUpdate: now.Add(23 * time.Hour)}
	java := main
	java.Schema = 1
	target := ScanTargetBinding{ImageDigest: digest, AssignmentGeneration: 7}
	scan := ScanUseObservation{JobUID: "owned-job", ReportUID: "owned-report", ImageDigest: digest, ScannerDigest: digest, AssignmentGeneration: target.AssignmentGeneration, CompletedAt: now.Add(-time.Hour), State: WorkloadScanComplete, FindingCount: 0, JavaApplicability: JavaDatabaseNeeded, Main: main, Java: java}
	accepted := AcceptedDatabaseState{FeedDigest: digest, ScannerDigest: digest, Generation: 1, ExpiresAt: now.Add(time.Hour), MirrorReady: true, Main: main, Java: java}
	return policy, target, scan, accepted
}

func TestMeasuredFreshDatabaseAndReportCanQualifyCleanPosture(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	policy, target, scan, accepted := freshnessFixture(now)
	result := EvaluateDatabaseFreshness(now, policy, target, scan, &accepted)
	if !result.EligibleForCleanPosture || result.MainState != DatabaseFresh || result.JavaState != DatabaseFresh || result.RescanRequired {
		t.Fatal("complete matching measured provenance was not fresh")
	}
	scan.JavaApplicability, scan.Java = JavaDatabaseNotNeeded, UsedDatabaseObservation{}
	result = EvaluateDatabaseFreshness(now, policy, target, scan, &accepted)
	if !result.EligibleForCleanPosture || result.JavaState != DatabaseNotNeeded {
		t.Fatal("measured Java-not-needed applicability demanded a Java DB")
	}
}

func TestUnknownStaleOrFailedProvenanceNeverQualifiesCleanPosture(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, mutation := range []string{"job", "report", "image", "changed-image", "changed-assignment", "missing-assignment", "missing-target", "scanner", "changed-scanner", "missing-accepted-scanner", "timestamp", "future-scan", "pending", "failed", "unavailable", "invalid-state", "negative-findings", "unknown-java", "missing-java", "main-schema", "java-schema", "main-digest", "metadata", "future-db", "next-order", "main-age", "java-age", "next-expired", "expired-report", "new-snapshot", "revoked", "generation", "feed", "feed-expired", "mirror", "nil-accepted", "policy"} {
		t.Run(mutation, func(t *testing.T) {
			policy, target, scan, accepted := freshnessFixture(now)
			state := &accepted
			switch mutation {
			case "job":
				scan.JobUID = ""
			case "report":
				scan.ReportUID = ""
			case "image":
				scan.ImageDigest = "configured-image"
			case "changed-image":
				target.ImageDigest = "sha256:" + strings.Repeat("b", 64)
			case "changed-assignment":
				target.AssignmentGeneration++
			case "missing-assignment":
				scan.AssignmentGeneration = 0
			case "missing-target":
				target = ScanTargetBinding{}
			case "scanner":
				scan.ScannerDigest = "configured-scanner"
			case "changed-scanner":
				scan.ScannerDigest = "sha256:" + strings.Repeat("b", 64)
			case "missing-accepted-scanner":
				accepted.ScannerDigest = ""
			case "timestamp":
				scan.CompletedAt = time.Time{}
			case "future-scan":
				scan.CompletedAt = now.Add(6 * time.Minute)
			case "pending":
				scan.State = WorkloadScanPending
			case "failed":
				scan.State = WorkloadScanFailed
			case "unavailable":
				scan.State = WorkloadScanUnavailable
			case "invalid-state":
				scan.State = "other"
			case "negative-findings":
				scan.FindingCount = -1
			case "unknown-java":
				scan.JavaApplicability = JavaDatabaseUnknown
			case "missing-java":
				scan.Java = UsedDatabaseObservation{}
			case "main-schema":
				scan.Main.Schema = 1
			case "java-schema":
				scan.Java.Schema = 2
			case "main-digest":
				scan.Main.SubjectDigest = "configured-database"
			case "metadata":
				scan.Main.MetadataDigest = ""
			case "future-db":
				scan.Main.UpdatedAt = now.Add(6 * time.Minute)
			case "next-order":
				scan.Main.NextUpdate = scan.Main.UpdatedAt
			case "main-age":
				scan.Main.UpdatedAt = now.Add(-31 * time.Hour)
				accepted.Main = scan.Main
			case "java-age":
				scan.Java.UpdatedAt = now.Add(-49 * time.Hour)
				accepted.Java = scan.Java
			case "next-expired":
				scan.Main.NextUpdate = now
				accepted.Main = scan.Main
			case "expired-report":
				scan.CompletedAt = now.Add(-6 * time.Hour)
			case "new-snapshot":
				accepted.Main.SubjectDigest = "sha256:" + strings.Repeat("b", 64)
			case "revoked":
				accepted.Revoked = true
			case "generation":
				accepted.Generation = 0
			case "feed":
				accepted.FeedDigest = "configured-feed"
			case "feed-expired":
				accepted.ExpiresAt = now
			case "mirror":
				accepted.MirrorReady = false
			case "nil-accepted":
				state = nil
			case "policy":
				policy.ReportTTL = 7 * time.Hour
			}
			result := EvaluateDatabaseFreshness(now, policy, target, scan, state)
			if result.EligibleForCleanPosture {
				t.Fatal("missing, expired, unmeasured or failed state qualified a clean posture")
			}
			if mutation == "expired-report" && (!result.ReportExpired || !result.RescanRequired) {
				t.Fatal("report TTL did not force a rescan")
			}
			if mutation == "new-snapshot" && (!result.RescanRequired || result.MainState != DatabaseStale) {
				t.Fatal("new verified accepted snapshot left an older clean cache current")
			}
			if mutation == "changed-scanner" && (!result.RescanRequired || result.MainState != DatabaseStale) {
				t.Fatal("changed measured scanner identity did not invalidate clean cache")
			}
			if (mutation == "changed-image" || mutation == "changed-assignment") && (!result.RescanRequired || result.MainState != DatabaseUnknown) {
				t.Fatal("changed desired assignment or image did not invalidate the prior report")
			}
			if mutation == "mirror" && result.MainState != DatabaseMirrorUnreachable {
				t.Fatal("mirror outage lost its distinct state")
			}
		})
	}
}

func TestScanCompletionAndNextUpdateCannotRefreshOldDatabase(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	policy, target, scan, accepted := freshnessFixture(now)
	scan.CompletedAt = now
	scan.Main.UpdatedAt = now.Add(-31 * time.Hour)
	scan.Main.NextUpdate = now.Add(30 * 24 * time.Hour)
	accepted.Main = scan.Main
	result := EvaluateDatabaseFreshness(now, policy, target, scan, &accepted)
	if result.MainState != DatabaseStale || !result.RescanRequired || result.EligibleForCleanPosture {
		t.Fatal("fresh scan completion/future NextUpdate refreshed upstream DB age")
	}
	scan.FindingCount = 1
	result = EvaluateDatabaseFreshness(now, policy, target, scan, &accepted)
	if !result.FindingsPresent || result.EligibleForCleanPosture {
		t.Fatal("stale database hid known findings or qualified clean")
	}
}

func TestExpiredReportRemainsVisibleAcrossIndependentFailures(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, failure := range []string{"mirror", "feed", "revocation", "target", "scanner", "used-provenance", "nil-state"} {
		t.Run(failure, func(t *testing.T) {
			policy, target, scan, accepted := freshnessFixture(now)
			scan.CompletedAt, scan.FindingCount = now.Add(-6*time.Hour), 1
			state := &accepted
			switch failure {
			case "mirror":
				accepted.MirrorReady = false
			case "feed":
				accepted.ExpiresAt = now
			case "revocation":
				accepted.Revoked = true
			case "target":
				target.AssignmentGeneration++
			case "scanner":
				scan.ScannerDigest = "sha256:" + strings.Repeat("b", 64)
			case "used-provenance":
				scan.Main.MetadataDigest = ""
			case "nil-state":
				state = nil
			}
			result := EvaluateDatabaseFreshness(now, policy, target, scan, state)
			if !result.ReportExpired || !result.RescanRequired || !result.FindingsPresent || result.EligibleForCleanPosture {
				t.Fatal("independent failure masked expired report or known findings")
			}
		})
	}
}
