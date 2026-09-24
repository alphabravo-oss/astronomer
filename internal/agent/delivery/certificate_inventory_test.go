package delivery

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"testing"
	"time"
)

func TestCertificateLifecycleMetadata(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		expiry   string
		expired  bool
		expected string
	}{
		{"", false, "not reported"}, {"invalid", false, "invalid timestamp"},
		{"2026-09-22T00:00:00Z", true, "(expired)"}, {"2026-09-22T00:00:01Z", false, "2026-09-22T00:00:01Z"},
	} {
		item := &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"notAfter": test.expiry, "renewalTime": "2026-09-21T00:00:00Z"}, "spec": map[string]any{"secretName": "do-not-read"}}}
		detail, expired := certificateInventoryDetail(item, now)
		if expired != test.expired || !strings.Contains(detail, test.expected) || !strings.Contains(detail, "renewal scheduled 2026-09-21") || strings.Contains(detail, "do-not-read") {
			t.Fatalf("detail=%q expired=%v", detail, expired)
		}
	}
}
