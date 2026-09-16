package middleware

import (
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

// Derived policies have stable evidence IDs but never occupy editable policy
// rows. Their lifecycle is the transactional audit.read_tier setting.
var readAuditTierPolicies = map[string]sqlc.ReadAuditPolicy{
	"diagnostic": {ID: uuid.MustParse("8ab6241e-c663-49a7-acfb-29fa091a4281"), Name: "tier:diagnostic", PathPattern: "*", Verbs: "GET,HEAD", SampleRate: 0.1, Enabled: true},
	"incident":   {ID: uuid.MustParse("8ab6241e-c663-49a7-acfb-29fa091a4282"), Name: "tier:incident", PathPattern: "*", Verbs: "GET,HEAD", SampleRate: 1, Enabled: true},
}

func readAuditTierPolicy(tier string) *sqlc.ReadAuditPolicy {
	policy, ok := readAuditTierPolicies[tier]
	if !ok {
		return nil
	}
	return &policy
}
