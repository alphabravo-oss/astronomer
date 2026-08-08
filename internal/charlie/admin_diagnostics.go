package charlie

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func (s *AdminService) Diagnostics(ctx context.Context, correlationID string) (AdminDiagnosticsView, error) {
	now := s.now().UTC()
	checked := now.Format(time.RFC3339)
	checks := []AdminDiagnosticCheck{}
	add := func(id, label, state, summary string) {
		checks = append(checks, AdminDiagnosticCheck{ID: id, Label: label, State: state, Summary: summary, NextAction: diagnosticNextAction(id, state), CheckedAt: checked})
	}
	connection, connectionErr := s.connection(ctx)
	if connectionErr != nil {
		add("local_config", "Local database and configuration", "unavailable", "Charlie configuration is not available.")
		for _, item := range [][2]string{{"product_bridge_mtls", "Product Bridge mTLS"}, {"agent_primary", "Agent primary replica"}, {"agent_standby", "Agent standby replica"}, {"central_via_agent", "Central through agent"}, {"leader_epoch", "Leader and fencing epoch"}, {"route_rag", "Route and RAG readiness"}, {"mcp_tls_discovery", "MCP TLS and discovery digest"}, {"oci_artifacts", "OCI chart and image"}, {"credential_expiry", "Certificate and credential expiry"}} {
			add(item[0], item[1], "unknown", "Check was not run because Charlie is not configured.")
		}
		return AdminDiagnosticsView{Overall: "unavailable", Checks: checks, CorrelationID: correlationID}, nil
	}
	add("local_config", "Local database and configuration", "healthy", "Local Charlie metadata is readable and contains no runtime credentials in this response.")
	if !connection.Active || connection.EmergencyDisabled || EffectiveMode(Mode(connection.RequestedMode), Mode(connection.VerifiedMode), connection.EmergencyDisabled) == ModeDisabled {
		// The disabled diagnostics path is network-quiesced. It remains available
		// as a local status surface but must not initiate a request to the product
		// agent, its bridge, Charlie central, or the Kubernetes installer. An
		// enabled connection's separate signed control heartbeat is not a
		// diagnostics request and is governed by the runtime lifecycle gate.
		for _, check := range inactiveDiagnosticChecks() {
			add(check.ID, check.Label, "inactive", check.Summary)
		}
		return AdminDiagnosticsView{Overall: "inactive", Checks: checks, CorrelationID: correlationID}, nil
	}
	bridgeStatus, bridgeErr := AdminBridgeStatus{}, ErrAdminUnavailable
	if s.bridge != nil {
		bridgeStatus, bridgeErr = s.bridge.AdminStatus(ctx)
	}
	if bridgeErr != nil {
		add("product_bridge_mtls", "Product Bridge mTLS", "unavailable", "The fixed local mTLS bridge could not be reached.")
	} else {
		add("product_bridge_mtls", "Product Bridge mTLS", "healthy", "The product-local bridge completed mutual TLS and returned bounded status.")
	}
	installation := AgentInstallationStatus{}
	installErr := ErrAdminUnavailable
	if s.installer != nil {
		installation, installErr = s.installer.Status(ctx, adminInstallSpec(connection))
	}
	primaryState := "healthy"
	if installation.ReadyReplicas < 1 {
		primaryState = "unavailable"
	}
	add("agent_primary", "Agent primary replica", primaryState, fmt.Sprintf("%d of %d replicas are ready.", installation.ReadyReplicas, installation.DesiredReplicas))
	standbyState := "healthy"
	if installation.ReadyReplicas < 2 {
		standbyState = "degraded"
	}
	add("agent_standby", "Agent standby replica", standbyState, fmt.Sprintf("%d ready replicas are visible locally.", installation.ReadyReplicas))
	centralState := "unavailable"
	if bridgeErr == nil && bridgeStatus.CentralHealth == "healthy" {
		centralState = "healthy"
	} else if bridgeErr == nil {
		centralState = "degraded"
	}
	add("central_via_agent", "Central through agent", centralState, "Central health is reported only through the product-local agent.")
	leaderState := "degraded"
	if bridgeErr == nil && bridgeStatus.Epoch > 0 && bridgeStatus.LeaderInstanceID != "" {
		leaderState = "healthy"
	}
	add("leader_epoch", "Leader and fencing epoch", leaderState, "Leader identity and epoch are reported by the local bridge.")
	routeState := "unknown"
	routeSummary := "The current bridge contract does not independently attest RAG readiness."
	if bridgeErr == nil && bridgeStatus.RouteID == connection.RouteID && bridgeStatus.CentralHealth == "healthy" {
		routeState = "healthy"
		routeSummary = "The configured route matches the agent report; central route health is available."
	}
	add("route_rag", "Route and RAG readiness", routeState, routeSummary)
	digestState := "degraded"
	if bridgeErr == nil && normalizeDigest(bridgeStatus.DisclosureDigest) == normalizeDigest(connection.DisclosureDigest) {
		digestState = "healthy"
	}
	add("mcp_tls_discovery", "MCP TLS and discovery digest", digestState, "The agent disclosure digest is compared with Astronomer's current MCP catalog.")
	artifactState := "unavailable"
	if installErr == nil && installation.ArtifactsVerified {
		artifactState = "healthy"
	} else if installation.ApplicationSynced {
		artifactState = "degraded"
	}
	add("oci_artifacts", "OCI chart and image", artifactState, "Argo application and immutable artifact status are checked locally and through the agent.")
	credentialState, credentialSummary := credentialExpiryDiagnostic(connection, now)
	add("credential_expiry", "Certificate and credential expiry", credentialState, credentialSummary)
	overall := "healthy"
	for _, check := range checks {
		if check.State == "unavailable" {
			overall = "unavailable"
			break
		}
		if check.State == "degraded" || check.State == "unknown" {
			overall = "degraded"
		}
	}
	return AdminDiagnosticsView{Overall: overall, Checks: checks, CorrelationID: correlationID}, nil
}

// LocalDiagnostics is the feature-disabled diagnostic contract. Even if the
// last persisted connection was active, it deliberately performs database-only
// reads and reports all runtime/network checks as inactive.
func (s *AdminService) LocalDiagnostics(ctx context.Context, correlationID string) (AdminDiagnosticsView, error) {
	checked := s.now().UTC().Format(time.RFC3339)
	checks := []AdminDiagnosticCheck{}
	add := func(id, label, state, summary string) {
		checks = append(checks, AdminDiagnosticCheck{ID: id, Label: label, State: state, Summary: summary, NextAction: diagnosticNextAction(id, state), CheckedAt: checked})
	}
	if _, err := s.connection(ctx); err != nil {
		state := "unavailable"
		summary := "Charlie configuration is not available."
		if errors.Is(err, ErrAdminNotConfigured) {
			state = "inactive"
			summary = "Charlie is not configured; no product-agent or central request was made."
		}
		add("local_config", "Local database and configuration", state, summary)
	} else {
		add("local_config", "Local database and configuration", "healthy", "Local Charlie metadata is readable and contains no runtime credentials in this response.")
	}
	for _, check := range inactiveDiagnosticChecks() {
		add(check.ID, check.Label, "inactive", check.Summary)
	}
	return AdminDiagnosticsView{Overall: "inactive", Checks: checks, CorrelationID: correlationID}, nil
}

func inactiveDiagnosticChecks() []AdminDiagnosticCheck {
	const summary = "Not run while Charlie is disabled; no product-agent or central request was made."
	return []AdminDiagnosticCheck{
		{ID: "product_bridge_mtls", Label: "Product Bridge mTLS", Summary: summary},
		{ID: "agent_primary", Label: "Agent primary replica", Summary: summary},
		{ID: "agent_standby", Label: "Agent standby replica", Summary: summary},
		{ID: "central_via_agent", Label: "Central through agent", Summary: summary},
		{ID: "leader_epoch", Label: "Leader and fencing epoch", Summary: summary},
		{ID: "route_rag", Label: "Route and RAG readiness", Summary: summary},
		{ID: "mcp_tls_discovery", Label: "MCP TLS and discovery digest", Summary: summary},
		{ID: "oci_artifacts", Label: "OCI chart and image", Summary: summary},
		{ID: "credential_expiry", Label: "Certificate and credential expiry", Summary: summary},
	}
}

func diagnosticNextAction(id, state string) string {
	if state == "healthy" {
		return "No operator action is required."
	}
	if state == "inactive" {
		return "Enable Charlie explicitly before running connectivity diagnostics; no request is sent while disabled."
	}
	actions := map[string]string{
		"local_config":        "Verify the signed onboarding package and local Charlie configuration, then rerun diagnostics.",
		"product_bridge_mtls": "Check the Charlie agent pods and Product Bridge Service/TLS trust, then rerun diagnostics.",
		"agent_primary":       "Inspect the Argo application and agent StatefulSet; restore at least one ready replica.",
		"agent_standby":       "Restore the configured standby replica before enabling approval or automation.",
		"central_via_agent":   "Check agent egress to the configured Charlie endpoint; do not add direct server egress.",
		"leader_epoch":        "Confirm one elected agent leader and a current fencing epoch before permitting writes.",
		"route_rag":           "Verify the Charlie route and product-version knowledge release from Charlie administration.",
		"mcp_tls_discovery":   "Rediscover the MCP catalog and acknowledge the exact new disclosure digest.",
		"oci_artifacts":       "Reconcile the Argo application using the immutable chart and image digests from Charlie OCI.",
		"credential_expiry":   "Rotate the Charlie agent credentials and certificates before they expire, then verify the new status.",
	}
	if action := actions[id]; action != "" {
		return action
	}
	return "Keep Charlie in read-only or disabled mode and rerun diagnostics after correcting this boundary."
}

// credentialExpiryDiagnostic uses the exact signed onboarding expiries already
// persisted with the connection. Inferring a nominal lifetime from the last
// rotation time hides short-lived artifact credentials and reports "unknown"
// immediately after a valid first install.
func credentialExpiryDiagnostic(connection sqlc.CharlieConnection, now time.Time) (string, string) {
	certificate := connection.CertificateExpiresAt.UTC()
	artifact := connection.ArtifactCredentialExpiresAt.UTC()
	if certificate.IsZero() || artifact.IsZero() {
		return "unknown", "Exact certificate or artifact credential expiry metadata is unavailable."
	}
	earliest := certificate
	kind := "certificate"
	if artifact.Before(earliest) {
		earliest = artifact
		kind = "artifact credential"
	}
	state := "healthy"
	if !earliest.After(now.UTC().Add(7 * 24 * time.Hour)) {
		state = "degraded"
	}
	condition := "expires"
	if !earliest.After(now.UTC()) {
		condition = "expired"
	}
	return state, fmt.Sprintf("The earliest active %s %s at %s.", kind, condition, earliest.Format(time.RFC3339))
}

func normalizeDigest(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "sha256:")
}

