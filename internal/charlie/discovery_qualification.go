package charlie

import (
	"context"
	"fmt"
)

const (
	DiscoveryQualificationMixed     = "mixed_catalog"
	DiscoveryQualificationMalformed = "malformed_catalog"
)

// AdminDiscoveryQualificationView is a fixed conformance result, not an input
// catalog or an activation API. Operators may select only one of the embedded
// cases below; no caller-controlled capability, schema, or safety metadata is
// accepted.
type AdminDiscoveryQualificationView struct {
	Scenario          string   `json:"scenario"`
	CandidateEnabled  bool     `json:"candidate_enabled"`
	AcceptedCount     int      `json:"accepted_count"`
	RejectedCount     int      `json:"rejected_count"`
	AcceptedNames     []string `json:"accepted_names"`
	DisclosureDigest  string   `json:"disclosure_digest,omitempty"`
	CatalogBound      bool     `json:"catalog_bound"`
	MalformedRejected bool     `json:"malformed_rejected"`
}

func (s *AdminService) DiscoveryQualification(ctx context.Context, scenario string) (AdminDiscoveryQualificationView, error) {
	connection, err := s.connection(ctx)
	if err != nil {
		return AdminDiscoveryQualificationView{}, err
	}
	if !connection.Active || connection.EmergencyDisabled || EffectiveMode(Mode(connection.RequestedMode), Mode(connection.VerifiedMode), connection.EmergencyDisabled) == ModeDisabled {
		return AdminDiscoveryQualificationView{}, ErrAdminConflict
	}
	return qualifyFixedDiscovery(scenario)
}

func qualifyFixedDiscovery(scenario string) (AdminDiscoveryQualificationView, error) {
	readCatalog, writeCatalog := ReadCapabilityCatalog(), WriteCapabilityCatalog()
	if len(readCatalog) == 0 || len(writeCatalog) == 0 {
		return AdminDiscoveryQualificationView{}, ErrAdminUnavailable
	}
	malformed := writeCatalog[0]
	malformed.Destructive = true
	malformed.AutoEligible = true
	malformed.Risk = "low"
	malformed.Rollback = "fully_reversible"
	malformed.RequiresVerification = false

	var candidate []CapabilityDescriptor
	switch scenario {
	case DiscoveryQualificationMixed:
		candidate = []CapabilityDescriptor{readCatalog[0], writeCatalog[0], malformed}
	case DiscoveryQualificationMalformed:
		candidate = []CapabilityDescriptor{malformed}
	default:
		return AdminDiscoveryQualificationView{}, ErrAdminConflict
	}
	tools := mcpToolsFromCatalog(context.Background(), candidate, nil)
	view := AdminDiscoveryQualificationView{
		Scenario: scenario, CandidateEnabled: len(tools) > 0,
		AcceptedCount: len(tools), RejectedCount: len(candidate) - len(tools),
		AcceptedNames: make([]string, 0, len(tools)), MalformedRejected: len(tools) < len(candidate),
	}
	for _, tool := range tools {
		name, ok := tool["name"].(string)
		if !ok || name == "" {
			return AdminDiscoveryQualificationView{}, fmt.Errorf("qualified discovery output is invalid")
		}
		view.AcceptedNames = append(view.AcceptedNames, name)
	}
	if len(tools) > 0 {
		view.DisclosureDigest = capabilityDisclosureDigest(tools)
		view.CatalogBound = view.DisclosureDigest != ""
	}
	return view, nil
}
