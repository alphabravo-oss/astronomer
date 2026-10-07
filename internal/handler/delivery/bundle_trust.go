package delivery

import (
	"encoding/json"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

// Resolution records an explicit unsigned policy as "unsigned", not "verified".
// Honor that immutable source policy without treating absent or invalid policy
// as permission to bypass signature verification.
func bundleReadyForDelivery(row sqlc.ComponentBundleVersion) bool {
	if row.State != "ready" {
		return false
	}
	if row.VerificationStatus == "verified" {
		return true
	}
	if row.VerificationStatus != "unsigned" {
		return false
	}
	var source struct {
		Trust model.TrustPolicy `json:"trust_policy"`
	}
	if err := json.Unmarshal(row.SourceSpec, &source); err != nil {
		return false
	}
	return source.Trust.AllowUnsigned && source.Trust.Validate() == nil
}
