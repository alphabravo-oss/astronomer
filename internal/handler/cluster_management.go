package handler

import (
	"encoding/json"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
)

// Enrollment always grants the agent full management; end-user actions remain
// scoped by Astronomer RBAC. Legacy profile annotations are no longer selectors.
func fullManagementAnnotations(raw json.RawMessage) json.RawMessage {
	var annotations map[string]json.RawMessage
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &annotations); err != nil {
			return raw // Preserve the existing free-form JSON contract.
		}
	}
	if annotations == nil {
		annotations = make(map[string]json.RawMessage)
	}
	annotations[agenttemplate.PrivilegeProfileAnnotation] = json.RawMessage(`"admin"`)
	encoded, _ := json.Marshal(annotations) // All values came from valid JSON.
	return encoded
}
