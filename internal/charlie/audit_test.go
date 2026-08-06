package charlie

import (
	"regexp"
	"testing"
)

func TestCharlieAuditVocabularyIsBoundedAndCanonical(t *testing.T) {
	pattern := regexp.MustCompile(`^charlie\.[a-z_]+\.[a-z_]+$`)
	seen := map[string]bool{}
	for _, action := range AuditActions {
		if !pattern.MatchString(action) || seen[action] {
			t.Fatalf("invalid or duplicate Charlie audit action %q", action)
		}
		seen[action] = true
	}
	for _, resource := range []string{
		AuditResourceConnection, AuditResourceCertificate, AuditResourceAgent,
		AuditResourceMode, AuditResourceSession, AuditResourceTrigger,
		AuditResourceApproval, AuditResourceMCPDecision, AuditResourceFinding,
		AuditResourceFeature, AuditResourceDelegation,
	} {
		if resource == "" || len(resource) > 64 {
			t.Fatalf("invalid Charlie audit resource %q", resource)
		}
	}
}
