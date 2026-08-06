package charlie

import (
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

func TestSafeAdminAlertDeliveryProofRecognizesOnlyExactContentFreeTemplate(t *testing.T) {
	findingID := uuid.New()
	now := time.Now().UTC()
	link := "/dashboard/charlie?tab=findings&finding=" + findingID.String()
	row := sqlc.CharlieAlertDelivery{
		ID: uuid.New(), FindingID: findingID, DeliveryKind: "initial", Status: "delivered",
		AttemptCount: 1, MaximumAttempts: 8, DeepLink: link,
		Subject:   "Charlie finding requires attention",
		Body:      "Review the durable finding in Astronomer. No approval or product action is implied by this notification. " + link,
		CreatedAt: now, UpdatedAt: now,
	}
	proof := safeAdminAlertDeliveryProof(row)
	if !proof.DeepLinkValid || !proof.ContentFree || proof.TemplateIdentity != "charlie.finding.initial/v1" || proof.DeliveryID != row.ID.String() {
		t.Fatalf("exact template was not recognized: %+v", proof)
	}
	for _, mutate := range []func(*sqlc.CharlieAlertDelivery){
		func(value *sqlc.CharlieAlertDelivery) { value.Subject += " payload-SENTINEL" },
		func(value *sqlc.CharlieAlertDelivery) { value.Body += " payload-SENTINEL" },
		func(value *sqlc.CharlieAlertDelivery) {
			value.DeepLink = "/dashboard/charlie?tab=findings&finding=" + uuid.NewString()
		},
		func(value *sqlc.CharlieAlertDelivery) { value.DeliveryKind = "unknown" },
	} {
		candidate := row
		mutate(&candidate)
		got := safeAdminAlertDeliveryProof(candidate)
		if got.ContentFree {
			t.Fatalf("modified delivery received content-free verdict: %+v", got)
		}
		encoded := strings.Join([]string{got.DeliveryID, got.FindingID, got.DeliveryKind, got.Status, got.TemplateIdentity}, " ")
		if strings.Contains(encoded, "SENTINEL") {
			t.Fatalf("proof leaked template content: %+v", got)
		}
	}
}
