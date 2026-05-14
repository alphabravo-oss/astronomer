package handler

// T6.074 — builtin-template guard tests. Pins the contract that
// platform-baseline/platform-default templates cannot be created,
// updated, or deleted via the handler.

import (
	"testing"
)

func TestIsBuiltinTemplate(t *testing.T) {
	for _, name := range []string{"platform-baseline", "platform-default"} {
		if !isBuiltinTemplate(name) {
			t.Errorf("isBuiltinTemplate(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"prod-baseline", "my-template", "", "platform"} {
		if isBuiltinTemplate(name) {
			t.Errorf("isBuiltinTemplate(%q) = true, want false", name)
		}
	}
}
