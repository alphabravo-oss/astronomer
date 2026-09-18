package projects

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

func TestPrepareCreateOwnsProjectPolicyDefaultsAndCapacity(t *testing.T) {
	params, err := PrepareCreate(CreateInput{
		Name: "payments", ClusterID: uuid.New(), Namespaces: json.RawMessage(`["api","worker"]`),
	})
	if err != nil {
		t.Fatalf("PrepareCreate() error = %v", err)
	}
	if params.PodSecurityProfile != DefaultPodSecurityProfile || params.NetworkPolicyMode != "none" {
		t.Fatalf("defaults = profile %q, mode %q", params.PodSecurityProfile, params.NetworkPolicyMode)
	}
	if string(params.ResourceQuota) != "{}" || string(params.LimitRange) != "{}" {
		t.Fatalf("policy JSON defaults = quota %s limit %s", params.ResourceQuota, params.LimitRange)
	}
	cap := "not-a-quantity"
	_, err = PrepareCreate(CreateInput{
		Name: "payments", ClusterID: uuid.New(), Namespaces: json.RawMessage(`["api","worker"]`),
		ResourceQuotaCPULimit: &cap,
	})
	if err == nil || !IsValidationError(err) {
		t.Fatalf("bounded cap error = %v, want validation error", err)
	}
}

func TestPlanUpdatePreservesOmittedNamespacesAndComputesLifecycleDelta(t *testing.T) {
	existing := sqlc.Project{ID: uuid.New(), Namespaces: json.RawMessage(`["api","worker"]`),
		PodSecurityProfile: "baseline", NetworkPolicyMode: "isolated"}
	plan, err := PlanUpdate(existing, UpdateInput{DisplayName: "Payments"})
	if err != nil {
		t.Fatalf("PlanUpdate() error = %v", err)
	}
	if got := string(plan.Params.Namespaces); got != `["api","worker"]` {
		t.Fatalf("namespaces = %s, want existing namespaces", got)
	}
	if len(plan.Added) != 0 || len(plan.Removed) != 0 {
		t.Fatalf("omitted namespaces diff = added %v removed %v", plan.Added, plan.Removed)
	}
	plan, err = PlanUpdate(existing, UpdateInput{Namespaces: json.RawMessage(`["worker","jobs"]`), NetworkPolicyMode: "none"})
	if err != nil {
		t.Fatalf("PlanUpdate() error = %v", err)
	}
	if strings.Join(plan.Added, ",") != "jobs" || strings.Join(plan.Removed, ",") != "api" {
		t.Fatalf("diff = added %v removed %v", plan.Added, plan.Removed)
	}
}

func TestPlanPolicyUpdateRejectsUnsupportedMode(t *testing.T) {
	_, err := PlanPolicyUpdate(sqlc.Project{ID: uuid.New(), PodSecurityProfile: "baseline", NetworkPolicyMode: "none"}, PolicyInput{
		NetworkPolicyMode: pointer("open"),
	})
	if err == nil || !IsValidationError(err) {
		t.Fatalf("PlanPolicyUpdate() error = %v, want validation error", err)
	}
}

func TestNamespaceOwnershipRulesAreDomainOwned(t *testing.T) {
	if !IsReservedNamespace("kube-system") || !IsReservedNamespace("default") {
		t.Fatal("reserved namespaces must remain denied by the project domain")
	}
	project := sqlc.Project{Namespaces: json.RawMessage(`["api"]`)}
	if err := ValidateNamespaceAddition(project, "api"); err == nil {
		t.Fatal("duplicate namespace was accepted")
	}
}

func pointer(value string) *string { return &value }
