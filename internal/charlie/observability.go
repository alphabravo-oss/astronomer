package charlie

import "log/slog"

// DecisionLog is a deliberately content-free authority event. Correlation IDs
// are opaque and bounded; resource names, prompts, evidence, arguments,
// authorization references, URLs, errors, and credentials have no fields here.
type DecisionLog struct {
	SessionID  string
	ActionID   string
	Capability string
	Mode       Mode
	Effect     Effect
	Decision   AuthorityDecision
}

func LogAuthorityDecision(logger *slog.Logger, event DecisionLog) {
	if logger == nil {
		return
	}
	result := "denied"
	code := string(event.Decision.Code)
	if event.Decision.Allowed {
		result = "allowed"
		code = "allowed"
	}
	capability := "unknown"
	if descriptor, found := capabilityByName(event.Capability); found {
		capability = descriptor.Name
	}
	logger.Info("Charlie authority decision",
		"event", "charlie_authority_decision",
		"session_digest", digestBytes([]byte(event.SessionID)),
		"action_digest", digestBytes([]byte(event.ActionID)),
		"capability", capability,
		"mode", event.Mode,
		"effect", event.Effect,
		"result", result,
		"code", code,
	)
}
