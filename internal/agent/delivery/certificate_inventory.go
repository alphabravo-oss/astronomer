package delivery

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Certificate status contains public lifecycle metadata; never read Secrets or
// certificate/private-key payloads to populate this inventory.
func certificateInventoryDetail(item *unstructured.Unstructured, now time.Time) (detail string, expired bool) {
	expiry := "not reported"
	if raw := nestedString(item.Object, "status", "notAfter"); raw != "" {
		if timestamp, err := time.Parse(time.RFC3339, raw); err == nil {
			expiry = timestamp.UTC().Format(time.RFC3339)
			expired = !timestamp.After(now)
			if expired {
				expiry += " (expired)"
			}
		} else {
			expiry = "invalid timestamp"
		}
	}
	renewal := "not reported"
	if raw := nestedString(item.Object, "status", "renewalTime"); raw != "" {
		if timestamp, err := time.Parse(time.RFC3339, raw); err == nil {
			renewal = timestamp.UTC().Format(time.RFC3339)
		} else {
			renewal = "invalid timestamp"
		}
	}
	return fmt.Sprintf("cert-manager status: expires %s; renewal scheduled %s. Schedule is not proof of successful renewal.", expiry, renewal), expired
}
