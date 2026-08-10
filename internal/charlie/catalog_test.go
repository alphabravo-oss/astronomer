package charlie

import (
	"strings"
	"testing"
	"time"
)

func TestCapabilityCatalogPinsSRESurfaceAndManagedTargetBoundary(t *testing.T) {
	all := append(ReadCapabilityCatalog(), WriteCapabilityCatalog()...)
	seen := map[string]bool{}
	for _, capability := range all {
		if seen[capability.Name] {
			t.Fatalf("duplicate capability %s", capability.Name)
		}
		seen[capability.Name] = true
		if capability.ManagedTargetAccess {
			t.Fatalf("capability %s can access a managed target", capability.Name)
		}
		if capability.Source == "" || capability.RBACResource == "" || capability.RBACVerb == "" || capability.MaxResponseBytes <= 0 || capability.TimeoutSeconds <= 0 {
			t.Fatalf("capability %s has incomplete safety metadata: %+v", capability.Name, capability)
		}
		for _, field := range capability.AcceptedFields {
			for _, forbidden := range []string{"url", "sql", "gvr", "kubeconfig", "command", "shell", "namespace", "downstream"} {
				if strings.Contains(strings.ToLower(field), forbidden) {
					t.Fatalf("capability %s accepts forbidden field %q", capability.Name, field)
				}
			}
		}
	}

	for _, required := range []string{
		"astronomer.agent_fleet.summary", "astronomer.agent_fleet.list",
		"astronomer.agent_fleet.get", "astronomer.agent_fleet.connection_history",
		"astronomer.agent_fleet.upgrade_status", "astronomer.agent_fleet.ingestion_health",
		"astronomer.tunnel.health", "astronomer.tunnel.replica_distribution",
		"astronomer.tunnel.recent_errors",
		"astronomer.management.pods", "astronomer.management.rollout_status",
		"astronomer.installation.summary", "astronomer.management.pod_logs",
	} {
		if !seen[required] {
			t.Errorf("required management-plane capability %s is missing", required)
		}
	}
	// Descriptions must be specific enough for the model to pick tools.
	for _, capability := range all {
		if capability.Effect != EffectRead {
			continue
		}
		if len(capability.Description) < 40 {
			t.Errorf("read capability %s has a too-generic description", capability.Name)
		}
	}
}

func TestCapabilityCatalogOmitsProhibitedOperations(t *testing.T) {
	for _, capability := range append(ReadCapabilityCatalog(), WriteCapabilityCatalog()...) {
		name := strings.ToLower(capability.Name)
		for _, forbidden := range []string{"downstream", "shell", "exec", "apply", "patch", "delete", "secret", "credential", "agent_upgrade", "production_restore"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("prohibited capability %s was disclosed", capability.Name)
			}
		}
	}
}

func TestWriteCatalogRequiresIdempotencyPreconditionsAndVerification(t *testing.T) {
	for _, capability := range WriteCapabilityCatalog() {
		if capability.Effect != EffectWrite || !capability.Idempotent || !capability.RequiresPrecondition || !capability.RequiresVerification {
			t.Fatalf("unsafe write descriptor: %+v", capability)
		}
		if !containsCatalogField(capability.AcceptedFields, "operation_id") {
			t.Fatalf("write %s lacks a product idempotency field", capability.Name)
		}
		if !containsCatalogField(capability.AcceptedFields, "resource_id") {
			t.Fatalf("write %s lacks an exact ProductContext resource field", capability.Name)
		}
		if capability.Destructive {
			t.Fatalf("v1 must not disclose destructive capability %s", capability.Name)
		}
		resource := capabilityFieldSchemas(capability.Name)["resource_id"]
		if !resource.Required || resource.Type != "string" || resource.MaxLength != 128 || resource.Pattern != opaqueIDPattern {
			t.Fatalf("write %s has an unsafe ProductContext resource schema: %+v", capability.Name, resource)
		}
	}
}

func TestOnlyBoundedQueueRetryIsAutoEligible(t *testing.T) {
	for _, capability := range WriteCapabilityCatalog() {
		want := capability.Name == "astronomer.queue.retry_task"
		if capability.AutoEligible != want {
			t.Fatalf("auto eligibility for %s = %t, want %t", capability.Name, capability.AutoEligible, want)
		}
	}
}

func TestDestructiveCatalogFactAlwaysWinsOverAutomationMetadata(t *testing.T) {
	capability := WriteCapabilityCatalog()[0]
	capability.Destructive = true
	capability.AutoEligible = true
	decision := DecideAuthority(AuthorityInput{
		FeatureEnabled: true, ConnectionActive: true, Mode: ModeAuto,
		Effect: capability.Effect, Destructive: capability.Destructive,
		DisclosureCurrent: true, LiveAuthorized: true, AutoEligible: capability.AutoEligible,
	}, time.Now())
	if decision.Code != DeniedDestructive {
		t.Fatalf("destructive catalog fact did not deny automation: %+v", decision)
	}
}

func TestAgentFleetCatalogUsesDatabaseOrServerTelemetryOnly(t *testing.T) {
	for _, capability := range ReadCapabilityCatalog() {
		if !strings.Contains(capability.Name, "agent_fleet") && !strings.Contains(capability.Name, "tunnel") {
			continue
		}
		if capability.Source != SourceAstronomerDatabase && capability.Source != SourceAstronomerServer {
			t.Fatalf("%s source=%s can cross the management-plane boundary", capability.Name, capability.Source)
		}
	}
}

func containsCatalogField(fields []string, wanted string) bool {
	for _, field := range fields {
		if field == wanted {
			return true
		}
	}
	return false
}
