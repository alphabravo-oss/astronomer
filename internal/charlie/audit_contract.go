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
}

type AuditContract struct {
	Version                 int                           `json:"version"`
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
	if contract.Version != 1 || len(contract.Fields) == 0 || len(contract.Events) == 0 {
		return fmt.Errorf("invalid Charlie audit contract")
	}
	coverage := map[string]bool{"success": true, "denial": true, "failure": true, "replay": true, "redaction": true}
	for _, event := range contract.Events {
		if event.Prefix == "" || event.ResourceType == "" || len(event.Actions) == 0 || len(event.AllowedFields) == 0 || len(event.Coverage) == 0 {
			return fmt.Errorf("invalid Charlie audit event contract")
		}
		for _, action := range event.Actions {
			if !auditCodePattern.MatchString(action) || !strings.HasPrefix(action, event.Prefix) {
				return fmt.Errorf("invalid Charlie audit action contract")
			}
		}
		for _, field := range event.AllowedFields {
			if _, ok := contract.Fields[field]; !ok {
				return fmt.Errorf("unknown Charlie audit field contract")
			}
		}
		for _, class := range event.Coverage {
			if !coverage[class] {
				return fmt.Errorf("unknown Charlie audit coverage class")
			}
		}
	}
	return nil
}
