package gitops

import (
	"errors"
	"fmt"
	"strings"
)

var ErrUnsupportedIntent = errors.New("gitops: unsupported registration intent")

// ValidateSupportedIntent fails before writes, including direct/dry-run Apply
// callers. Parsing a field must never imply its desired state was reconciled.
func ValidateSupportedIntent(doc ClusterRegistration) error {
	var fields []string
	if len(doc.Spec.Registries) > 0 {
		fields = append(fields, "spec.registries")
	}
	if len(doc.Spec.ToolPresets) > 0 {
		fields = append(fields, "spec.toolPresets")
	}
	if len(fields) > 0 {
		return fmt.Errorf("%w: %s are not reconciled; remove these fields and configure the cluster through the registry/tool APIs", ErrUnsupportedIntent, strings.Join(fields, ", "))
	}
	return nil
}
