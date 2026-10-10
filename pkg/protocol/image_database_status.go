package protocol

// ImageDatabaseStatusV1 is an additive public read contract. Existing image
// report/SDK layouts stay unchanged. No field grants acceptance or job authority.
type ImageDatabaseStatusV1 struct {
 ReportID string `json:"report_id"`
 ScanState string `json:"scan_state"`
 MainState string `json:"main_state"`
 JavaState string `json:"java_state"`
 JavaApplicability string `json:"java_applicability"`
 Reason string `json:"reason"`
 FindingCount int64 `json:"finding_count"`
 ObservationRevision int64 `json:"observation_revision"`
 WitnessRevision int64 `json:"witness_revision"`
 Cycle int64 `json:"cycle"`
 AcceptedGeneration int64 `json:"accepted_generation"`
 AcceptedFeedDigest string `json:"accepted_feed_digest,omitempty"`
 ReportExpired bool `json:"report_expired"`
 RescanRequired bool `json:"rescan_required"`
 EligibleForCleanPosture bool `json:"eligible_for_clean_posture"`
 CurrentWorkloadChecked bool `json:"current_workload_checked"`
 ResourceCapacityExhausted bool `json:"resource_capacity_exhausted"`
}
