package charlie

import (
	"fmt"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
)

// FindingAdvisoryDetail is the complete remote content allowed to cross from a
// Charlie finding into an Astronomer response. It deliberately has no action
// envelope, manifest, signature, authorization, approval, arguments, or
// request fields. Those objects remain on their dedicated execution channels.
type FindingAdvisoryDetail struct {
	EvidenceSummary   []string                  `json:"evidence_summary"`
	Diagnosis         string                    `json:"diagnosis"`
	Confidence        float32                   `json:"confidence"`
	RiskImpact        string                    `json:"risk_impact"`
	Preconditions     []string                  `json:"preconditions,omitempty"`
	Rollback          string                    `json:"rollback,omitempty"`
	OperatorChecks    []string                  `json:"operator_checks"`
	VerificationSteps []string                  `json:"verification_steps"`
	ManualRemediation *FindingManualRemediation `json:"manual_remediation,omitempty"`
}

type FindingManualRemediation struct {
	Preconditions  []string            `json:"preconditions,omitempty"`
	Steps          []string            `json:"steps"`
	ExpectedImpact string              `json:"expected_impact"`
	Rollback       string              `json:"rollback,omitempty"`
	Verification   FindingVerification `json:"verification"`
}

type FindingVerification struct {
	Method string   `json:"method"`
	Steps  []string `json:"steps"`
}

func advisoryDetailFromEnvelope(envelope contract.FindingEnvelope) (FindingAdvisoryDetail, error) {
	finding := envelope.Finding
	workflow := BridgeFindingSummary{
		FindingID: finding.FindingId, SessionID: finding.SessionId,
		Severity: string(finding.Severity), Status: string(finding.Status),
		WorkflowState: string(finding.Workflow.State), BlockCode: string(finding.BlockCode),
	}
	if envelope.Schema != "charlie.finding/v1" || !bridgeFindingOpaqueIDPattern.MatchString(finding.FindingId) ||
		!bridgeFindingOpaqueIDPattern.MatchString(finding.SessionId) || !validCentralFindingSeverity(workflow.Severity) ||
		!validCentralFindingStatus(workflow.Status) || !validCentralFindingWorkflow(workflow) || finding.Confidence < 0 || finding.Confidence > 1 ||
		!boundedAdvisoryText(finding.Diagnosis, 1, 4096) || !boundedAdvisoryText(finding.RiskImpact, 1, 2048) ||
		!boundedAdvisoryList(finding.EvidenceSummary, 0, 20, 1, 8192) || !boundedAdvisoryList(finding.OperatorChecks, 1, 16, 1, 512) ||
		!boundedAdvisoryList(finding.VerificationSteps, 1, 16, 1, 512) || !envelope.Storage.EncryptionRequired ||
		envelope.Storage.RetentionDays < 1 || envelope.Storage.RetentionDays > 90 || envelope.Storage.ExpiresAt.IsZero() {
		return FindingAdvisoryDetail{}, fmt.Errorf("Charlie finding advisory is invalid")
	}
	detail := FindingAdvisoryDetail{
		EvidenceSummary: append([]string(nil), finding.EvidenceSummary...), Diagnosis: finding.Diagnosis,
		Confidence: finding.Confidence, RiskImpact: finding.RiskImpact,
		OperatorChecks:    append([]string(nil), finding.OperatorChecks...),
		VerificationSteps: append([]string(nil), finding.VerificationSteps...),
	}
	if finding.Preconditions != nil {
		if !boundedAdvisoryList(*finding.Preconditions, 0, 16, 1, 256) {
			return FindingAdvisoryDetail{}, fmt.Errorf("Charlie finding advisory is invalid")
		}
		detail.Preconditions = append([]string(nil), (*finding.Preconditions)...)
	}
	if finding.Rollback != nil {
		if !boundedAdvisoryText(*finding.Rollback, 1, 1024) {
			return FindingAdvisoryDetail{}, fmt.Errorf("Charlie finding advisory is invalid")
		}
		detail.Rollback = *finding.Rollback
	}
	if remediation := finding.Workflow.ManualRemediation; remediation != nil {
		if !boundedAdvisoryList(remediation.Steps, 1, 16, 1, 512) ||
			!boundedAdvisoryText(remediation.ExpectedImpact, 1, 1024) ||
			!boundedAdvisoryText(remediation.Verification.Method, 1, 128) ||
			!boundedAdvisoryList(remediation.Verification.Steps, 1, 16, 1, 512) {
			return FindingAdvisoryDetail{}, fmt.Errorf("Charlie finding advisory is invalid")
		}
		view := &FindingManualRemediation{
			Steps: append([]string(nil), remediation.Steps...), ExpectedImpact: remediation.ExpectedImpact,
			Verification: FindingVerification{Method: remediation.Verification.Method, Steps: append([]string(nil), remediation.Verification.Steps...)},
		}
		if remediation.Preconditions != nil {
			if !boundedAdvisoryList(*remediation.Preconditions, 0, 16, 1, 256) {
				return FindingAdvisoryDetail{}, fmt.Errorf("Charlie finding advisory is invalid")
			}
			view.Preconditions = append([]string(nil), (*remediation.Preconditions)...)
		}
		if remediation.Rollback != nil {
			if !boundedAdvisoryText(*remediation.Rollback, 1, 1024) {
				return FindingAdvisoryDetail{}, fmt.Errorf("Charlie finding advisory is invalid")
			}
			view.Rollback = *remediation.Rollback
		}
		detail.ManualRemediation = view
	}
	return detail, nil
}

func boundedAdvisoryList(values []string, minimumItems, maximumItems, minimumLength, maximumLength int) bool {
	if len(values) < minimumItems || len(values) > maximumItems {
		return false
	}
	for _, value := range values {
		if !boundedAdvisoryText(value, minimumLength, maximumLength) {
			return false
		}
	}
	return true
}

func boundedAdvisoryText(value string, minimum, maximum int) bool {
	length := len(strings.TrimSpace(value))
	return length >= minimum && length <= maximum
}

type FindingAdvisoryDecision string

const (
	FindingAdvisoryAcknowledge FindingAdvisoryDecision = "acknowledge"
	FindingAdvisoryDismiss     FindingAdvisoryDecision = "dismiss"
	FindingAdvisoryResolve     FindingAdvisoryDecision = "resolve"
)

func (d FindingAdvisoryDecision) valid() bool {
	return d == FindingAdvisoryAcknowledge || d == FindingAdvisoryDismiss || d == FindingAdvisoryResolve
}
