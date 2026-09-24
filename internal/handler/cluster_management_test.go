package handler

import (
	"encoding/json"
	"testing"
)

func TestFullManagementEnrollmentPreservesAnnotations(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", `{"astronomer.io/agent-privilege-profile":"viewer","astronomer.io/image-scanning":"disabled","owner":{"team":"operations"}}`} {
		var annotations map[string]json.RawMessage
		if err := json.Unmarshal(fullManagementAnnotations(json.RawMessage(raw)), &annotations); err != nil {
			t.Fatal(err)
		}
		if string(annotations["astronomer.io/agent-privilege-profile"]) != `"admin"` {
			t.Fatalf("missing full-management intent: %s", raw)
		}
		if len(raw) > 10 && (string(annotations["astronomer.io/image-scanning"]) != `"disabled"` || string(annotations["owner"]) != `{"team":"operations"}`) {
			t.Fatalf("lost caller annotations: %v", annotations)
		}
	}
}
