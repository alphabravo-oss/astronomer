package scanner

import (
	"regexp"
	"time"
)

type DatabasePostureState string

const (
	DatabaseFresh             DatabasePostureState = "fresh"
	DatabaseStale             DatabasePostureState = "stale"
	DatabaseUnknown           DatabasePostureState = "unknown"
	DatabaseUnavailable       DatabasePostureState = "unavailable"
	DatabaseMirrorUnreachable DatabasePostureState = "mirror_unreachable"
	DatabaseNotNeeded         DatabasePostureState = "not_needed"
	DatabaseFeedExpired       DatabasePostureState = "feed_expired"
)

type WorkloadScanState string

const (
	WorkloadScanPending     WorkloadScanState = "pending"
	WorkloadScanFailed      WorkloadScanState = "failed"
	WorkloadScanComplete    WorkloadScanState = "complete"
	WorkloadScanUnavailable WorkloadScanState = "unavailable"
)

type JavaDatabaseApplicability string

const (
	JavaDatabaseNeeded    JavaDatabaseApplicability = "needed"
	JavaDatabaseNotNeeded JavaDatabaseApplicability = "not_needed"
	JavaDatabaseUnknown   JavaDatabaseApplicability = "unknown"
)

var freshnessDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type DatabaseFreshnessPolicy struct {
	ReportTTL      time.Duration
	MainMaximumAge time.Duration
	JavaMaximumAge time.Duration
	ClockSkew      time.Duration
}

// ScanUseObservation must be populated by measured owned job/report provenance,
// never from a configured repository, scan completion or optional user label.
// This pure evaluator grants no authorization, trust or mutation authority.
type ScanUseObservation struct {
	JobUID, ReportUID, ImageDigest, ScannerDigest string
	AssignmentGeneration                          int64
	CompletedAt                                   time.Time
	State                                         WorkloadScanState
	FindingCount                                  int64
	JavaApplicability                             JavaDatabaseApplicability
	Main, Java                                    UsedDatabaseObservation
}

// ScanTargetBinding comes from the current desired workload assignment, not
// from report-controlled metadata. Composition must retain that ownership.
type ScanTargetBinding struct {
	ImageDigest          string
	AssignmentGeneration int64
}

type UsedDatabaseObservation struct {
	SubjectDigest, LayerDigest, MetadataDigest string
	Schema                                     int
	UpdatedAt, NextUpdate                      time.Time
}

// AcceptedDatabaseState is a projection of the durable authenticated payload
// state. Actual composition must read acceptance/revocation atomically; this
// struct cannot itself prove that such a transaction or verification occurred.
type AcceptedDatabaseState struct {
	FeedDigest    string
	ScannerDigest string
	Generation    int64
	ExpiresAt     time.Time
	Revoked       bool
	MirrorReady   bool
	Main, Java    UsedDatabaseObservation
}

type DatabaseFreshnessResult struct {
	ScanState               WorkloadScanState
	MainState, JavaState    DatabasePostureState
	FindingsPresent         bool
	ReportExpired           bool
	RescanRequired          bool
	EligibleForCleanPosture bool
}

// EvaluateDatabaseFreshness keeps known findings visible while withholding a
// clean qualified posture for missing, mismatched, stale or revoked provenance.
// Upstream UpdatedAt is the age clock; completion never refreshes a DB's age.
func EvaluateDatabaseFreshness(now time.Time, policy DatabaseFreshnessPolicy, target ScanTargetBinding, scan ScanUseObservation, accepted *AcceptedDatabaseState) DatabaseFreshnessResult {
	result := DatabaseFreshnessResult{ScanState: scan.State, MainState: DatabaseUnknown, JavaState: DatabaseUnknown, FindingsPresent: scan.State == WorkloadScanComplete && scan.FindingCount > 0}
	if now.IsZero() || !validFreshnessPolicy(policy) || !validScanObservation(now, policy, scan) {
		return result
	}
	if !freshnessDigest.MatchString(target.ImageDigest) || target.AssignmentGeneration <= 0 {
		return result
	}
	if target.ImageDigest != scan.ImageDigest || target.AssignmentGeneration != scan.AssignmentGeneration {
		result.RescanRequired = true
		return result
	}
	if scan.JavaApplicability == JavaDatabaseNotNeeded {
		result.JavaState = DatabaseNotNeeded
	}
	if accepted == nil || accepted.Revoked || accepted.Generation <= 0 || !freshnessDigest.MatchString(accepted.FeedDigest) {
		result.MainState = DatabaseUnavailable
		if scan.JavaApplicability == JavaDatabaseNeeded {
			result.JavaState = DatabaseUnavailable
		}
		return result
	}
	if accepted.ExpiresAt.IsZero() || !now.Before(accepted.ExpiresAt) {
		result.MainState = DatabaseFeedExpired
		if scan.JavaApplicability == JavaDatabaseNeeded {
			result.JavaState = DatabaseFeedExpired
		}
		return result
	}
	if !accepted.MirrorReady {
		result.MainState = DatabaseMirrorUnreachable
		if scan.JavaApplicability == JavaDatabaseNeeded {
			result.JavaState = DatabaseMirrorUnreachable
		}
		return result
	}
	if !freshnessDigest.MatchString(accepted.ScannerDigest) {
		return result
	}
	if scan.ScannerDigest != accepted.ScannerDigest {
		result.MainState = DatabaseStale
		if scan.JavaApplicability == JavaDatabaseNeeded {
			result.JavaState = DatabaseStale
		}
		result.RescanRequired = true
		return result
	}
	result.MainState, result.RescanRequired = evaluateUsedDatabase(now, policy.ClockSkew, policy.MainMaximumAge, 2, scan.Main, accepted.Main)
	if scan.JavaApplicability == JavaDatabaseNeeded {
		var rescan bool
		result.JavaState, rescan = evaluateUsedDatabase(now, policy.ClockSkew, policy.JavaMaximumAge, 1, scan.Java, accepted.Java)
		result.RescanRequired = result.RescanRequired || rescan
	}
	result.ReportExpired = now.Sub(scan.CompletedAt) >= policy.ReportTTL
	result.RescanRequired = result.RescanRequired || result.ReportExpired
	result.EligibleForCleanPosture = scan.State == WorkloadScanComplete && scan.FindingCount == 0 && !result.ReportExpired && result.MainState == DatabaseFresh && (result.JavaState == DatabaseFresh || result.JavaState == DatabaseNotNeeded)
	return result
}

func validFreshnessPolicy(policy DatabaseFreshnessPolicy) bool {
	return policy.ReportTTL > 0 && policy.ReportTTL <= 6*time.Hour && policy.MainMaximumAge > 0 && policy.MainMaximumAge <= 30*time.Hour && policy.JavaMaximumAge > 0 && policy.JavaMaximumAge <= 48*time.Hour && policy.ClockSkew >= 0 && policy.ClockSkew <= 5*time.Minute
}

func validScanObservation(now time.Time, policy DatabaseFreshnessPolicy, scan ScanUseObservation) bool {
	if scan.State != WorkloadScanPending && scan.State != WorkloadScanFailed && scan.State != WorkloadScanComplete && scan.State != WorkloadScanUnavailable {
		return false
	}
	if scan.State != WorkloadScanComplete || scan.FindingCount < 0 || scan.JobUID == "" || len(scan.JobUID) > 128 || scan.ReportUID == "" || len(scan.ReportUID) > 128 || !freshnessDigest.MatchString(scan.ImageDigest) || !freshnessDigest.MatchString(scan.ScannerDigest) || scan.AssignmentGeneration <= 0 || scan.CompletedAt.IsZero() || scan.CompletedAt.After(now.Add(policy.ClockSkew)) {
		return false
	}
	return scan.JavaApplicability == JavaDatabaseNeeded || scan.JavaApplicability == JavaDatabaseNotNeeded || scan.JavaApplicability == JavaDatabaseUnknown
}

func evaluateUsedDatabase(now time.Time, skew, maximum time.Duration, schema int, used, expected UsedDatabaseObservation) (DatabasePostureState, bool) {
	if !validUsedDatabase(now, skew, schema, used) || !validUsedDatabase(now, skew, schema, expected) {
		return DatabaseUnknown, false
	}
	if used != expected {
		return DatabaseStale, true
	}
	if now.Sub(used.UpdatedAt) > maximum || !now.Before(used.NextUpdate) {
		return DatabaseStale, true
	}
	return DatabaseFresh, false
}

func validUsedDatabase(now time.Time, skew time.Duration, schema int, used UsedDatabaseObservation) bool {
	return freshnessDigest.MatchString(used.SubjectDigest) && freshnessDigest.MatchString(used.LayerDigest) && freshnessDigest.MatchString(used.MetadataDigest) && used.Schema == schema && !used.UpdatedAt.IsZero() && !used.NextUpdate.IsZero() && !used.UpdatedAt.After(now.Add(skew)) && used.NextUpdate.After(used.UpdatedAt)
}
