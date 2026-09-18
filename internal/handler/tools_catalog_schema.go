package handler

import (
	"context"
	"fmt"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/baseline"
	"github.com/google/uuid"
)

// Phase B5 — CIS operator
//
// Catalog entry for `rancher/cis-operator`. Per-cluster install. Once the
// release is rolled out, the operator surfaces three CRDs that astronomer-go
// proxies through the existing tunnel:
//
//   - `ClusterScan`         (cis.cattle.io/v1) — created by SecurityHandler.CreateScan
//   - `ClusterScanProfile`  (cis.cattle.io/v1) — listed by SecurityHandler.ListProfiles
//   - `ClusterScanReport`   (cis.cattle.io/v1) — polled by tasks.HandleSecurityIngest
//
// The `cluster_tools` row is seeded in the canonical v1 initial schema so the
// catalog is consistent across fresh deploys and replicas without requiring a
// separate seed step. Keeping the
// chart coordinates here as a Go-side constant means handlers and tasks can
// reference them without re-querying the DB.
const (
	CISOperatorToolSlug      = "cis-operator"
	CISOperatorToolName      = "CIS Scanner (Rancher)"
	CISOperatorChartName     = "rancher-cis-benchmark"
	CISOperatorChartRepoURL  = "https://charts.rancher.io"
	CISOperatorChartCategory = "security"
	CISOperatorNamespace     = "cis-operator-system"
)

// CISOperatorChartCoordinates returns the chart coordinates for cis-operator
// as the same struct shape stored under `cluster_tools.charts`. Exposed so
// tests and other packages can reference one source of truth.
func CISOperatorChartCoordinates() toolChart {
	return toolChart{
		ChartName: CISOperatorChartName,
		RepoURL:   CISOperatorChartRepoURL,
		Namespace: CISOperatorNamespace,
		Order:     0,
	}
}

// Phase B4 — Dex
//
// Catalog entry for `dex` from the dexidp Helm repo. Single-instance install
// in the management cluster; we don't run Dex per-tenant. Once the release is
// rolled out, the operator's flow is:
//
//  1. PUT /api/v1/auth/dex/settings/ with the public issuer URL Astronomer
//     should treat as the OIDC provider (e.g. https://dex.example.com).
//  2. POST /api/v1/auth/dex/connectors/ for each upstream IdP (Azure AD,
//     LDAP, SAML, ...).
//  3. POST /api/v1/auth/dex/apply/ to reconcile the retained runtime Secret
//     that the chart's Deployment mounts read-only.
//  4. POST /api/v1/auth/dex/register-as-sso/ to add a `dex` row in
//     sso_configurations so A1's generic OIDC path treats Dex as a regular
//     /auth/login/dex/ provider.
//
// The `cluster_tools` row is seeded in the canonical v1 initial schema
// alongside the dex_connectors / dex_settings tables. Chart coordinates
// stay here as Go constants so handlers/tests have one source of truth.
const (
	DexToolSlug          = "dex"
	DexToolName          = "Dex Identity Broker"
	DexChartName         = "dex"
	DexChartRepoURL      = "https://charts.dexidp.io"
	DexChartCategory     = "auth"
	DexDefaultNamespace  = "dex"
	DexRuntimeSecretName = "astronomer-dex-runtime"
)

// DexChartCoordinates returns the chart coordinates for dex as the same
// struct shape stored under `cluster_tools.charts`.
func DexChartCoordinates() toolChart {
	return toolChart{
		ChartName: DexChartName,
		RepoURL:   DexChartRepoURL,
		Namespace: DexDefaultNamespace,
		Order:     0,
	}
}

// Phase B2 — Velero backup engine
//
// Catalog entry for `velero` from the vmware-tanzu Helm repo. Per-cluster
// install — Velero must run in each cluster you want to back up. Once the
// release is rolled out, the operator's flow is:
//
//  1. POST /api/v1/backups/storage/ with the cloud credentials + cluster id;
//     BackupHandler.applyVeleroBSL writes the BackupStorageLocation CR and
//     a credentials Secret into the Velero namespace.
//  2. POST /api/v1/backups/schedules/ with cron + include/exclude filters;
//     BackupHandler.applyVeleroSchedule projects the row into a Velero
//     `Schedule` CR. Velero's controller fans backups out on cron upstream.
//  3. POST /api/v1/backups/schedules/{id}/trigger-now/ creates a one-off
//     Velero `Backup` CR for instant backups; the reconciler polls
//     `status.phase` until terminal.
//  4. POST /api/v1/backups/{id}/restore/ writes a Velero `Restore` CR.
//
// The chart's BSL/VSL are intentionally empty in the default values: the
// real BackupStorageLocation is created from the BackupStorageConfig handler
// once a user wires up their cloud destination — keeping the install path
// independent of credentials so we can install Velero before anyone has
// configured S3.
//
// The cluster_tools row is seeded by the canonical v1 initial schema alongside
// the tables that track the Velero CR identities. Chart coordinates stay here as Go constants so
// handlers and tests share one source of truth.
const (
	VeleroToolSlug         = "velero"
	VeleroToolName         = "Velero"
	VeleroChartName        = "velero"
	VeleroChartRepoURL     = "https://vmware-tanzu.github.io/helm-charts"
	VeleroChartCategory    = "backup"
	VeleroDefaultNamespace = "velero"
)

// VeleroChartCoordinates returns the chart coordinates for velero as the same
// struct shape stored under `cluster_tools.charts`.
func VeleroChartCoordinates() toolChart {
	return toolChart{
		ChartName: VeleroChartName,
		RepoURL:   VeleroChartRepoURL,
		Namespace: VeleroDefaultNamespace,
		Order:     0,
	}
}

// Supportability / TLS posture — cert-manager
//
// Catalog entry for `cert-manager` from the Jetstack Helm repo. It can run on
// either the management cluster or workload clusters; Astronomer uses it
// primarily to automate TLS for the management Gateway, but operators may also
// use it for app/workload ingress on managed clusters.
//
// The `cluster_tools` row is seeded in the canonical v1 initial schema.
const (
	CertManagerToolSlug         = "cert-manager"
	CertManagerToolName         = "cert-manager"
	CertManagerChartName        = "cert-manager"
	CertManagerChartRepoURL     = "https://charts.jetstack.io"
	CertManagerChartCategory    = "security"
	CertManagerDefaultNamespace = "astronomer-cert-manager"
)

// CertManagerChartCoordinates returns the chart coordinates for cert-manager
// as the same struct shape stored under `cluster_tools.charts`.
func CertManagerChartCoordinates() toolChart {
	return toolChart{
		ChartName: CertManagerChartName,
		RepoURL:   CertManagerChartRepoURL,
		Namespace: CertManagerDefaultNamespace,
		Order:     0,
	}
}

// VeleroDefaultValuesYAML is the conservative defaults snippet baked into the
// catalog `presets["default"]`. We intentionally leave both backupStorageLocation
// and volumeSnapshotLocation empty — the BackupStorageLocation is owned by
// BackupHandler and is populated when the user POSTs to /backups/storage/.
//
// `initContainers` enumerates the provider plugins available for selection in
// the UI; users pick one (aws / gcp / azure / csi) at install time and we
// emit only the chosen plugin into the rendered values.
const VeleroDefaultValuesYAML = `configuration:
  backupStorageLocation: []
  volumeSnapshotLocation: []
deployNodeAgent: false
snapshotsEnabled: true
backupsEnabled: true
metrics:
  enabled: true
serviceAccount:
  server:
    create: true
initContainers: []
`

// ToolScope describes where a catalog tool is meant to run.
type ToolScope string

const (
	// ToolScopeControlPlane means the tool is part of the management plane
	// and should only be installed on the local Astronomer cluster (Dex).
	// Installing it on a workload cluster is almost always a
	// mistake — nothing on the workload cluster consumes it.
	ToolScopeControlPlane ToolScope = "control-plane"
	// ToolScopeWorkload means the tool is data-plane: it runs on each
	// workload cluster (Velero, CIS scanner, metrics-server). Installing it
	// on the management cluster is fine but rarely useful.
	ToolScopeWorkload ToolScope = "workload"
	// ToolScopeAny means there's no opinion — any cluster is acceptable.
	ToolScopeAny ToolScope = "any"
)

// toolScopes is the catalog→scope map. New tools should be added here so the
// install path can refuse misuse early. When a slug isn't listed we default
// to ToolScopeAny rather than blocking, which keeps the catalog extensible
// without requiring every tool to be classified.
var toolScopes = map[string]ToolScope{
	DexToolSlug:         ToolScopeControlPlane,
	VeleroToolSlug:      ToolScopeWorkload,
	CISOperatorToolSlug: ToolScopeWorkload,
}

// scopeForTool returns the configured scope for a tool slug, falling back to
// ToolScopeAny when no policy is registered.
func scopeForTool(slug string) ToolScope {
	if s, ok := toolScopes[slug]; ok {
		return s
	}
	return ToolScopeAny
}

// checkToolScope enforces the control-plane vs workload policy at install
// time. Returns (errorMessage, ok=false) when the cluster type is wrong for
// the tool's scope; (_, ok=true) means the install can proceed. A DB lookup
// failure is treated as ok=true so we never accidentally hard-block valid
// installs on a transient infrastructure issue — the actual install would
// fail loudly enough on its own.
func (h *ToolHandler) checkToolScope(ctx context.Context, slug string, clusterID uuid.UUID) (string, bool) {
	scope := scopeForTool(slug)
	if scope == ToolScopeAny {
		return "", true
	}
	cluster, err := h.queries.GetClusterByID(ctx, clusterID)
	if err != nil {
		return "", true
	}
	switch scope {
	case ToolScopeControlPlane:
		if !cluster.IsLocal {
			return fmt.Sprintf("%s is a control-plane tool and can only be installed on the local Astronomer cluster", slug), false
		}
	case ToolScopeWorkload:
		if cluster.IsLocal {
			return fmt.Sprintf("%s is a workload-cluster tool and should not be installed on the local Astronomer cluster", slug), false
		}
	}
	return "", true
}

// checkClusterRBACProfile blocks installing a cluster-RBAC baseline component
// (one whose chart ships ClusterRole/ClusterRoleBinding, webhooks or CRDs) onto
// a cluster whose agent runs on a non-admin privilege profile. Such an agent SA
// is read-only on cluster-scoped RBAC, so the install can never fully converge.
// Returns ("", true) — allowed — for any component not marked
// RequiresClusterRBAC, for admin-profile clusters, and (fail-open) when the
// cluster row can't be loaded, mirroring checkToolScope's tolerance so a
// transient lookup miss doesn't wrongly block an admin cluster.
func (h *ToolHandler) checkClusterRBACProfile(ctx context.Context, slug string, clusterID uuid.UUID) (string, bool) {
	component, ok := baseline.ComponentBySlug(slug)
	if slug != "istio" && (!ok || !component.RequiresClusterRBAC) {
		return "", true
	}
	cluster, err := h.queries.GetClusterByID(ctx, clusterID)
	if err != nil {
		return "", true
	}
	profile := agentPrivilegeProfileFromAnnotations(cluster.Annotations)
	if profile == agenttemplate.PrivilegeProfileAdmin {
		return "", true
	}
	return fmt.Sprintf(
		"%s installs cluster-scoped RBAC (ClusterRole/ClusterRoleBinding and admission webhooks) that this cluster's %q agent cannot create — only an admin-profile agent can. Re-adopt the cluster with the admin privilege profile before installing this tool.",
		slug, profile,
	), false
}
