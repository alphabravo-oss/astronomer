package charlie

import "context"

func (s *AdminService) modePrerequisites(ctx context.Context) (ModePrerequisites, error) {
	connection, err := s.connection(ctx)
	if err != nil {
		return ModePrerequisites{}, err
	}
	enabled, grants, err := s.automationState(ctx)
	if err != nil {
		return ModePrerequisites{}, err
	}
	prerequisites := ModePrerequisites{
		DisclosureAcknowledged:  connection.DisclosureDigest != "" && connection.AcknowledgedDisclosureDigest == connection.DisclosureDigest,
		AutomationIdentityReady: enabled,
		AutomationTargetReady:   grants,
	}
	if s.bridge == nil {
		return prerequisites, ErrAdminUnavailable
	}
	status, err := s.bridge.AdminStatus(ctx)
	if err != nil {
		return prerequisites, err
	}
	for _, capability := range status.AutoAllowlist {
		for _, descriptor := range WriteCapabilityCatalog() {
			if capability == descriptor.Name && descriptor.AutoEligible {
				prerequisites.AutomationAllowlistReady = true
				return prerequisites, nil
			}
		}
	}
	return prerequisites, nil
}
