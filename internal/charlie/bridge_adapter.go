package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/google/uuid"
)

// RuntimeBridge is the sole adapter from product services to the fixed local
// Product Bridge. It cannot accept or construct a Charlie Central URL.
type RuntimeBridge struct{ runtime *contract.Runtime }

var bridgeFindingOpaqueIDPattern = regexp.MustCompile(opaqueIDPattern)

// BridgeFindingSummary is the complete background-sync contract. It contains
// only lifecycle metadata and the opaque session linkage needed to recover the
// product-owned authorization scope. Any central detail/content field makes
// the strict runtime decoder reject the response.
type BridgeFindingSummary struct {
	FindingID        string    `json:"finding_id"`
	SessionID        string    `json:"session_id"`
	InvestigationID  string    `json:"investigation_id"`
	DeduplicationKey string    `json:"deduplication_key"`
	RepeatCount      int32     `json:"repeat_count"`
	Severity         string    `json:"severity"`
	Status           string    `json:"status"`
	BlockCode        string    `json:"block_code"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func NewRuntimeBridge(runtime *contract.Runtime) (*RuntimeBridge, error) {
	if runtime == nil {
		return nil, fmt.Errorf("Charlie Product Bridge runtime is unavailable")
	}
	return &RuntimeBridge{runtime: runtime}, nil
}

func (b *RuntimeBridge) CreateSession(ctx context.Context, input BridgeSessionRequest, key string) (BridgeSessionReceipt, error) {
	attributes := map[string]string{
		"installation_id":         input.Context.InstallationID,
		"chart_version":           input.Context.ChartVersion,
		"namespace":               input.Context.Namespace,
		"release":                 input.Context.Release,
		"kubernetes_version":      input.Context.KubernetesVersion,
		"kubernetes_distribution": input.Context.KubernetesDistribution,
		"trigger":                 input.Context.Trigger,
		"current_ui_context":      input.Context.CurrentUIContext,
		"correlation_ref":         input.Context.CorrelationRef,
	}
	for name, value := range attributes {
		if value == "" {
			delete(attributes, name)
		}
	}
	resourceIDs := make([]string, 0, len(input.Context.Resources))
	resources := make([]contract.ResourceReference, 0, len(input.Context.Resources))
	for _, resource := range input.Context.Resources {
		resourceIDs = append(resourceIDs, resource.ID)
		resources = append(resources, contract.ResourceReference{Id: resource.ID, Kind: resource.Type})
	}
	request := contract.CreateSession{
		AuthorizationRef: input.AuthorizationRef,
		Intent:           input.Intent, Objective: input.Intent, ProductVersion: input.ProductVersion,
		RequestId: key, Resources: resources,
	}
	request.Actor.Id = input.ActorID
	request.Actor.Type = contract.CreateSessionActorType(input.ActorType)
	request.Actor.DisplayLabel = input.ActorLabel
	request.Context.Schema = input.Context.Schema
	request.Context.Data.Attributes = &attributes
	request.Context.Data.ResourceIds = &resourceIDs
	if input.Context.HealthSummary != "" {
		summary := input.Context.HealthSummary
		request.Context.Data.Summary = &summary
	}
	var response contract.Session
	if err := b.runtime.DoJSONAuthorized(ctx, http.MethodPost, "/sessions", key, input.AuthorizationRef, request, &response); err != nil {
		return BridgeSessionReceipt{}, err
	}
	return BridgeSessionReceipt{SessionID: response.SessionId, Revision: int64(response.Revision)}, nil
}

func (b *RuntimeBridge) CreateInvestigation(ctx context.Context, input BridgeInvestigationRequest, key string) (BridgeSessionReceipt, error) {
	attributes := map[string]string{
		"event_type": input.EventType, "resource_type": input.ResourceType,
		"fingerprint": input.Fingerprint, "repeat_count": fmt.Sprint(input.RepeatCount),
		"first_occurred_at": input.FirstOccurredAt.UTC().Format(time.RFC3339Nano),
		"last_occurred_at":  input.LastOccurredAt.UTC().Format(time.RFC3339Nano),
		"product_version":   input.ProductVersion,
	}
	// Trigger metadata is already redacted and bounded by Astronomer. Flatten
	// scalar values only; nested content cannot smuggle authority or unbounded
	// evidence into the bridge scope.
	var metadata map[string]any
	if len(input.SummaryMetadata) > 0 && json.Unmarshal(input.SummaryMetadata, &metadata) == nil {
		for name, value := range metadata {
			switch scalar := value.(type) {
			case string:
				if len(scalar) <= 512 {
					attributes["event_"+name] = scalar
				}
			case float64, bool:
				attributes["event_"+name] = fmt.Sprint(scalar)
			}
		}
	}
	resourceIDs := []string{input.ResourceID}
	request := contract.CreateInvestigation{AuthorizationRef: input.AuthorizationRef, RequestId: input.RequestID}
	request.Scope.Attributes = &attributes
	request.Scope.ResourceIds = &resourceIDs
	var response contract.InvestigationReceipt
	if err := b.runtime.DoJSONAuthorized(ctx, http.MethodPost, "/investigations", key, input.AuthorizationRef, request, &response); err != nil {
		return BridgeSessionReceipt{}, err
	}
	if strings.TrimSpace(response.SessionId) == "" || strings.TrimSpace(response.TurnId) == "" || response.Revision < 1 {
		return BridgeSessionReceipt{}, fmt.Errorf("Charlie investigation receipt is incomplete")
	}
	return BridgeSessionReceipt{SessionID: response.SessionId, Revision: int64(response.Revision)}, nil
}

func (b *RuntimeBridge) GetSession(ctx context.Context, sessionID, authorizationRef string) (json.RawMessage, error) {
	path, err := sessionPath(sessionID)
	if err != nil {
		return nil, err
	}
	var response contract.Session
	if err := b.runtime.DoJSONAuthorized(ctx, http.MethodGet, path, "", authorizationRef, nil, &response); err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

func (b *RuntimeBridge) GetHistory(ctx context.Context, sessionID, authorizationRef, cursor string, limit int) (json.RawMessage, error) {
	path, err := sessionPath(sessionID)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if limit > 0 {
		query.Set("limit", fmt.Sprint(limit))
	}
	historyPath := path + "/history"
	if encoded := query.Encode(); encoded != "" {
		historyPath += "?" + encoded
	}
	var response contract.HistoryPage
	if err := b.runtime.DoJSONAuthorized(ctx, http.MethodGet, historyPath, "", authorizationRef, nil, &response); err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

func (b *RuntimeBridge) CreateMessage(ctx context.Context, sessionID, authorizationRef string, clientMessageID uuid.UUID, message string) (json.RawMessage, error) {
	path, err := sessionPath(sessionID)
	if err != nil {
		return nil, err
	}
	request := contract.CreateMessage{AuthorizationRef: authorizationRef, RequestId: clientMessageID.String(), Message: message}
	var response contract.TurnReceipt
	if err := b.runtime.DoJSONAuthorized(ctx, http.MethodPost, path+"/messages", clientMessageID.String(), authorizationRef, request, &response); err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

func (b *RuntimeBridge) AbortSession(ctx context.Context, sessionID, authorizationRef string, requestID uuid.UUID) error {
	path, err := sessionPath(sessionID)
	if err != nil {
		return err
	}
	return b.runtime.DoJSONAuthorized(ctx, http.MethodPost, path+"/abort", requestID.String(), authorizationRef, contract.IdempotentCommand{RequestId: requestID.String()}, nil)
}

// StreamSessionEvents is the only streaming path from Astronomer to Charlie.
// The runtime connects exclusively to the fixed, cluster-local Product Bridge;
// event content remains opaque and is never logged or persisted by this
// adapter. The callback returning nil is the acknowledgement boundary.
func (b *RuntimeBridge) StreamSessionEvents(ctx context.Context, sessionID, authorizationRef, lastEventID string, handle func(contract.Event) error) error {
	if _, err := sessionPath(sessionID); err != nil {
		return err
	}
	if len(lastEventID) > 128 {
		return fmt.Errorf("Charlie event cursor is invalid")
	}
	return b.runtime.StreamEventsAuthorized(ctx, sessionID, lastEventID, authorizationRef, handle)
}

// ListApprovals returns only approvals visible through the caller's short-lived
// product delegation. Astronomer independently verifies every signed manifest
// and rechecks live product RBAC before rendering one as eligible.
func (b *RuntimeBridge) ListApprovals(ctx context.Context, authorizationRef string) ([]contract.Approval, error) {
	var response []contract.Approval
	if err := b.runtime.DoJSONAuthorized(ctx, http.MethodGet, "/approvals", "", authorizationRef, nil, &response); err != nil {
		return nil, err
	}
	if len(response) > 100 {
		return nil, fmt.Errorf("Charlie approval response exceeds the contract limit")
	}
	return response, nil
}

// ListFindings reads only bounded lifecycle summaries. Full finding content is
// fetched separately by GetFinding after Astronomer rechecks current resource
// authorization for one local finding.
func (b *RuntimeBridge) ListFindings(ctx context.Context, authorizationRef string) ([]BridgeFindingSummary, error) {
	var response []BridgeFindingSummary
	if err := b.runtime.DoJSONAuthorized(ctx, http.MethodGet, "/findings", "", authorizationRef, nil, &response); err != nil {
		return nil, err
	}
	if len(response) > MaxCharlieFindingItems {
		return nil, fmt.Errorf("Charlie finding response exceeds the contract limit")
	}
	for _, finding := range response {
		if !validBridgeFindingSummary(finding) {
			return nil, fmt.Errorf("Charlie finding summary is invalid")
		}
	}
	return response, nil
}

func validBridgeFindingSummary(finding BridgeFindingSummary) bool {
	if !bridgeFindingOpaqueIDPattern.MatchString(finding.FindingID) ||
		!bridgeFindingOpaqueIDPattern.MatchString(finding.SessionID) ||
		!bridgeFindingOpaqueIDPattern.MatchString(finding.InvestigationID) ||
		!isLowerHexDigest(finding.DeduplicationKey) || finding.RepeatCount < 1 ||
		finding.UpdatedAt.IsZero() || !validCentralFindingSeverity(finding.Severity) ||
		!validCentralFindingStatus(finding.Status) || !validCentralFindingBlockCode(finding.BlockCode) {
		return false
	}
	return true
}

func isLowerHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// DecideApproval binds the browser decision to the exact product-verified
// signed manifest digest. Charlie cannot substitute a different action, target,
// revision, or expiry after Astronomer presents it to the approver.
func (b *RuntimeBridge) DecideApproval(ctx context.Context, approvalID, authorizationRef string, requestID uuid.UUID, decision, manifestDigest string) (contract.Approval, error) {
	path, err := opaqueBridgePath("/approvals/", approvalID)
	if err != nil || requestID == uuid.Nil || (decision != "approve" && decision != "reject") || len(manifestDigest) != 64 {
		return contract.Approval{}, fmt.Errorf("Charlie approval decision is invalid")
	}
	request := contract.ApprovalDecision{
		AuthorizationRef: authorizationRef,
		Decision:         contract.ApprovalDecisionDecision(decision),
		ManifestDigest:   manifestDigest,
		RequestId:        requestID.String(),
	}
	var response contract.Approval
	if err := b.runtime.DoJSONAuthorized(ctx, http.MethodPost, path+"/decision", requestID.String(), authorizationRef, request, &response); err != nil {
		return contract.Approval{}, err
	}
	return response, nil
}

func (b *RuntimeBridge) GetFinding(ctx context.Context, findingID, authorizationRef string) (json.RawMessage, error) {
	path, err := opaqueBridgePath("/findings/", findingID)
	if err != nil {
		return nil, err
	}
	var response json.RawMessage
	if err := b.runtime.DoJSONAuthorized(ctx, http.MethodGet, path, "", authorizationRef, nil, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (b *RuntimeBridge) TransitionFinding(ctx context.Context, findingID, authorizationRef string, requestID uuid.UUID, transition string) (json.RawMessage, error) {
	path, err := opaqueBridgePath("/findings/", findingID)
	if err != nil {
		return nil, err
	}
	bridgeTransition := map[string]string{"acknowledged": "acknowledge", "dismissed": "dismiss", "resolved": "resolve"}[transition]
	if bridgeTransition == "" {
		return nil, fmt.Errorf("Charlie finding transition is invalid")
	}
	request := map[string]string{"request_id": requestID.String(), "transition": bridgeTransition, "actor_ref": "product-user"}
	var response json.RawMessage
	if err := b.runtime.DoJSONAuthorized(ctx, http.MethodPost, path+"/transitions", requestID.String(), authorizationRef, request, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func opaqueBridgePath(prefix, identifier string) (string, error) {
	if strings.TrimSpace(identifier) == "" || len(identifier) > 128 {
		return "", fmt.Errorf("Charlie bridge identifier is invalid")
	}
	return prefix + url.PathEscape(identifier), nil
}

func sessionPath(sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" || len(sessionID) > 128 {
		return "", fmt.Errorf("Charlie session identifier is invalid")
	}
	return "/sessions/" + url.PathEscape(sessionID), nil
}
