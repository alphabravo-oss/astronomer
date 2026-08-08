package charlie

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

//go:embed audit_contract.json
var auditContractJSON []byte

type auditFieldContract struct {
	Kind       string   `json:"kind"`
	Values     []string `json:"values,omitempty"`
	AllowEmpty bool     `json:"allow_empty,omitempty"`
}

type AuditEventContract struct {
	Prefix        string   `json:"prefix"`
	Actions       []string `json:"actions"`
	ResourceType  string   `json:"resource_type"`
	AllowedFields []string `json:"allowed_fields"`
	Coverage      []string `json:"coverage"`
	NotApplicable []string `json:"not_applicable"`
	Correlation   string   `json:"correlation"`
}

type AuditGovernanceContract struct {
	Owner           string `json:"owner"`
	SystemOfRecord  string `json:"system_of_record"`
	RetentionClass  string `json:"retention_class"`
	CorrelationRule string `json:"correlation_rule"`
}

type AuditContract struct {
	Version                 int                           `json:"version"`
	CoverageClasses         []string                      `json:"coverage_classes"`
	Governance              AuditGovernanceContract       `json:"governance"`
	DenialCodes             []string                      `json:"denial_codes"`
	Fields                  map[string]auditFieldContract `json:"fields"`
	Events                  []AuditEventContract          `json:"events"`
	ForbiddenFieldFragments []string                      `json:"forbidden_field_fragments"`
}

var (
	auditContractOnce sync.Once
	auditContract     AuditContract
	auditContractErr  error
	auditCodePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,127}$`)
	auditDigest       = regexp.MustCompile(`^(?:sha256:)?[a-f0-9]{64}$`)
)

func CharlieAuditContract() (AuditContract, error) {
	auditContractOnce.Do(func() {
		auditContractErr = json.Unmarshal(auditContractJSON, &auditContract)
		if auditContractErr == nil {
			auditContractErr = validateAuditContract(auditContract)
		}
	})
	return auditContract, auditContractErr
}

// EncodeCharlieAuditDetail is the only encoder for Charlie domain audit
// details. Unknown events and fields fail closed; callers cannot append
// arbitrary request, model, evidence, error, credential, or resource content.
func EncodeCharlieAuditDetail(action, resourceType string, fields map[string]any) ([]byte, error) {
	contract, err := CharlieAuditContract()
	if err != nil {
		return nil, fmt.Errorf("Charlie audit contract is unavailable")
	}
	event, ok := matchAuditEvent(contract.Events, action, resourceType)
	if !ok {
		return nil, fmt.Errorf("Charlie audit event is not allowlisted")
	}
	allowed := make(map[string]struct{}, len(event.AllowedFields))
	for _, field := range event.AllowedFields {
		allowed[field] = struct{}{}
	}
	for name, value := range fields {
		if _, ok := allowed[name]; !ok {
			return nil, fmt.Errorf("Charlie audit field is not allowlisted")
		}
		field, ok := contract.Fields[name]
		if !ok || !validAuditField(field, value) {
			return nil, fmt.Errorf("Charlie audit field is invalid")
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil || len(encoded) > 2048 {
		return nil, fmt.Errorf("Charlie audit detail is invalid")
	}
	return encoded, nil
}

func matchAuditEvent(events []AuditEventContract, action, resourceType string) (AuditEventContract, bool) {
	for _, event := range events {
		if event.ResourceType != resourceType {
			continue
		}
		for _, allowed := range event.Actions {
			if action == allowed {
				return event, true
			}
		}
	}
	return AuditEventContract{}, false
}

func validAuditField(contract auditFieldContract, value any) bool {
	switch contract.Kind {
	case "code", "digest", "enum":
		text, ok := value.(string)
		if !ok || text == "" && !contract.AllowEmpty {
			return false
		}
		if text == "" {
			return true
		}
		switch contract.Kind {
		case "code":
			return auditCodePattern.MatchString(text)
		case "digest":
			return auditDigest.MatchString(text)
		case "enum":
			for _, item := range contract.Values {
				if text == item {
					return true
				}
			}
		}
		return false
	case "count":
		switch number := value.(type) {
		case int:
			return number >= 0
		case int32:
			return number >= 0
		case int64:
			return number >= 0
		case uint:
			return true
		case uint32:
			return true
		case uint64:
			return true
		default:
			return false
		}
	case "bool":
		_, ok := value.(bool)
		return ok
	default:
		return false
	}
}

func validateAuditContract(contract AuditContract) error {
	if contract.Version != 1 || len(contract.Fields) == 0 || len(contract.Events) == 0 ||
		contract.Governance.Owner != "astronomer" || contract.Governance.SystemOfRecord != "audit_log" ||
		contract.Governance.RetentionClass != "platform_setting.audit.retention_days" || contract.Governance.CorrelationRule != "bounded_opaque_or_sha256" {
		return fmt.Errorf("invalid Charlie audit contract")
	}
	coverage := map[string]bool{"success": true, "denial": true, "failure": true, "replay": true, "redaction": true}
	if !exactStringSet(contract.CoverageClasses, coverage) || len(contract.DenialCodes) == 0 {
		return fmt.Errorf("invalid Charlie audit coverage vocabulary")
	}
	denials := make(map[string]bool, len(contract.DenialCodes))
	for _, code := range contract.DenialCodes {
		if !auditCodePattern.MatchString(code) || denials[code] {
			return fmt.Errorf("invalid Charlie audit denial vocabulary")
		}
		denials[code] = true
	}
	denialField, ok := contract.Fields["denial_code"]
	if !ok || denialField.Kind != "enum" || !denialField.AllowEmpty || !exactStringSet(denialField.Values, denials) {
		return fmt.Errorf("Charlie audit denial field differs from denial coverage")
	}
	seenActions := make(map[string]bool)
	for _, event := range contract.Events {
		if event.Prefix == "" || event.ResourceType == "" || len(event.Actions) == 0 || len(event.AllowedFields) == 0 || len(event.Coverage) == 0 || event.NotApplicable == nil || !validAuditCorrelation(event.Correlation) {
			return fmt.Errorf("invalid Charlie audit event contract")
		}
		for _, action := range event.Actions {
			if !auditCodePattern.MatchString(action) || !strings.HasPrefix(action, event.Prefix) || seenActions[action] {
				return fmt.Errorf("invalid Charlie audit action contract")
			}
			seenActions[action] = true
		}
		for _, field := range event.AllowedFields {
			if _, ok := contract.Fields[field]; !ok {
				return fmt.Errorf("unknown Charlie audit field contract")
			}
		}
		classified := make(map[string]bool, len(event.Coverage)+len(event.NotApplicable))
		for _, class := range append(append([]string(nil), event.Coverage...), event.NotApplicable...) {
			if !coverage[class] || classified[class] {
				return fmt.Errorf("unknown Charlie audit coverage class")
			}
			classified[class] = true
		}
		if !exactStringSetMap(classified, coverage) {
			return fmt.Errorf("incomplete Charlie audit coverage matrix")
		}
	}
	return nil
}

func exactStringSet(values []string, expected map[string]bool) bool {
	actual := make(map[string]bool, len(values))
	for _, value := range values {
		if actual[value] {
			return false
		}
		actual[value] = true
	}
	return exactStringSetMap(actual, expected)
}

func exactStringSetMap(actual, expected map[string]bool) bool {
	if len(actual) != len(expected) {
		return false
	}
	for value := range expected {
		if !actual[value] {
			return false
		}
	}
	return true
}

func validAuditCorrelation(value string) bool {
	switch value {
	case "request_context", "resource_id", "action_digest", "receipt_id":
		return true
	default:
		return false
	}
}
