import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
import { Link as RouterLink } from "@tanstack/react-router";
import { Textarea } from "@/components/ui/textarea";
import {
  AlertTriangle,
  CheckCircle2,
  Loader2,
  Network,
  Plus,
  Shield,
} from "lucide-react";
import {
  type ServiceMeshDetection,
  type ServiceMeshInventory,
  type ServiceMeshPolicyValidation,
  type ServiceMeshKind,
} from "@/lib/api/cluster-service-mesh";
import { ActionButton } from "@/components/ui/action-button";
import { Tooltip } from "@/components/ui/tooltip";
import { SkeletonText } from "@/components/ui/skeleton";
import { BARE_BUTTON } from "@/lib/bare-button";

// meshLabel maps the backend enum to a human-readable string. Kept as a
// pure mapping (no JSX) so it can be reused in headers + tile labels.
export function meshLabel(kind: ServiceMeshKind): string {
  switch (kind) {
    case "istio":
      return "Istio";
    case "linkerd":
      return "Linkerd";
    case "kuma":
      return "Kuma";
    case "cilium":
      return "Cilium Mesh";
    case "none":
      return "No mesh installed";
    default:
      return "Detection pending";
  }
}

// meshAccent picks a tailwind-friendly accent class per mesh so the hero
// card visually distinguishes between meshes without an icon library.
export function meshAccent(kind: ServiceMeshKind): string {
  switch (kind) {
    case "istio":
      return "text-status-info";
    case "linkerd":
      return "text-status-success";
    case "kuma":
      return "text-primary";
    case "cilium":
      return "text-status-warning";
    case "none":
      return "text-muted-foreground";
    default:
      return "text-muted-foreground";
  }
}

// ─── Detection hero card ────────────────────────────────────────────────────
export function HeroCard({
  detection,
  clusterId,
}: {
  detection: ServiceMeshDetection;
  clusterId: string;
}) {
  const isInstalled =
    detection.detectedMesh !== "none" && detection.detectedMesh !== "unknown";
  return (
    <div className="rounded-lg border border-border bg-card p-6">
      <div className="flex items-start justify-between gap-4">
        <div className="flex items-start gap-4">
          <div className="rounded-md bg-accent/30 p-2.5">
            <Network
              className={`h-6 w-6 ${meshAccent(detection.detectedMesh)}`}
            />
          </div>
          <div>
            <h2 className="text-lg font-semibold text-foreground">
              {meshLabel(detection.detectedMesh)}
            </h2>
            <p className="text-sm text-muted-foreground mt-1">
              {isInstalled ? (
                <>
                  {detection.detectedVersion ? (
                    <>
                      Version{" "}
                      <span className="font-mono">
                        {detection.detectedVersion}
                      </span>
                      {detection.controlPlaneNamespace && (
                        <>
                          {" "}
                          at{" "}
                          <span className="font-mono">
                            {detection.controlPlaneNamespace}
                          </span>
                        </>
                      )}
                    </>
                  ) : detection.controlPlaneNamespace ? (
                    <>
                      Control plane in{" "}
                      <span className="font-mono">
                        {detection.controlPlaneNamespace}
                      </span>
                    </>
                  ) : (
                    "Detected — version unknown"
                  )}
                </>
              ) : detection.detectedMesh === "none" ? (
                "No service mesh detected on this cluster."
              ) : (
                "No detection has run yet; click Re-detect to populate."
              )}
            </p>
            {detection.lastError && (
              <p className="text-xs text-status-warning mt-2 flex items-start gap-1.5">
                <AlertTriangle className="h-3.5 w-3.5 shrink-0 mt-0.5" />
                <span>{detection.lastError}</span>
              </p>
            )}
          </div>
        </div>
        {!isInstalled && (
          <RouterLink
            to="/dashboard/clusters/$id/apps"
            params={{ id: clusterId }}
            search={{ section: "browse", install: "istio-base" }}
            className="inline-flex items-center gap-1.5 h-(--control-h) px-3 rounded-sm text-sm font-medium
              bg-primary text-primary-foreground hover:bg-primary/90 transition-colors shrink-0"
          >
            <Plus className="h-3.5 w-3.5" />
            Install a mesh
          </RouterLink>
        )}
      </div>
    </div>
  );
}

// ─── Health tile (one of the 4-grid items) ──────────────────────────────────
export function HealthTile({
  label,
  value,
  suffix,
  hint,
}: {
  label: string;
  value: number | string;
  suffix?: string;
  hint?: string;
}) {
  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
        {label}
      </p>
      <p className="text-2xl font-semibold text-foreground mt-1">
        {value}
        {suffix && (
          <span className="text-sm font-normal text-muted-foreground ml-1">
            {suffix}
          </span>
        )}
      </p>
      {hint && <p className="text-xs text-muted-foreground mt-1">{hint}</p>}
    </div>
  );
}

export function InventoryPanel({
  inventory,
  loading,
}: {
  inventory?: ServiceMeshInventory;
  loading: boolean;
}) {
  if (loading) {
    return (
      <div
        className="rounded-lg border border-border bg-card p-(--card-p) h-36"
        aria-busy="true"
      >
        <span className="sr-only">Loading inventory…</span>
        <SkeletonText lines={3} />
      </div>
    );
  }
  if (!inventory) return null;
  return (
    <div className="rounded-lg border border-border bg-card overflow-hidden">
      <div className="px-5 py-4 border-b border-border flex items-center justify-between gap-4">
        <div>
          <h2 className="text-sm font-semibold text-foreground">
            Mesh resources
          </h2>
          <p className="text-xs text-muted-foreground mt-1">
            {inventory.totalCount} resources tracked
          </p>
        </div>
        {inventory.notice && (
          <span className="text-xs text-status-warning text-right max-w-md">
            {inventory.notice}
          </span>
        )}
      </div>
      <div
        className="overflow-x-auto"
        role="region"
        aria-label="Service mesh inventory"
        tabIndex={0}
      >
        <Table className="w-full text-sm">
          <TableHeader className="bg-muted/40 text-xs text-muted-foreground">
            <TableRow>
              <TableHead className="text-left font-medium px-5 py-2.5">
                Kind
              </TableHead>
              <TableHead className="text-left font-medium px-5 py-2.5">
                API
              </TableHead>
              <TableHead className="text-right font-medium px-5 py-2.5">
                Count
              </TableHead>
              <TableHead className="text-left font-medium px-5 py-2.5">
                Objects
              </TableHead>
              <TableHead className="text-left font-medium px-5 py-2.5">
                Ownership
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {inventory.resources.map((resource) => {
              const gitOpsOwned = resource.items.filter(
                (item) => item.readOnly,
              ).length;
              const preview = resource.items.slice(0, 3);
              return (
                <TableRow
                  key={resource.kind}
                  className="border-t border-border"
                >
                  <TableCell className="px-5 py-(--row-py) font-medium text-foreground whitespace-nowrap">
                    {resource.kind}
                  </TableCell>
                  <TableCell className="px-5 py-(--row-py) text-xs text-muted-foreground font-mono whitespace-nowrap">
                    {resource.apiVersion}
                  </TableCell>
                  <TableCell className="px-5 py-(--row-py) text-right tabular-nums text-foreground">
                    {resource.count}
                  </TableCell>
                  <TableCell className="px-5 py-(--row-py) min-w-64">
                    {resource.count === 0 ? (
                      <span className="text-xs text-muted-foreground">
                        {resource.notice || "None"}
                      </span>
                    ) : (
                      <div className="flex flex-wrap gap-1.5">
                        {preview.map((item) => (
                          <Tooltip
                            key={`${resource.kind}:${item.namespace || "_"}:${item.name}`}
                            content={item.reason || undefined}
                          >
                            <span className="inline-flex items-center rounded-sm border border-border px-2 py-1 text-xs text-foreground">
                              {item.namespace && (
                                <span className="text-muted-foreground mr-1">
                                  {item.namespace}/
                                </span>
                              )}
                              {item.name}
                            </span>
                          </Tooltip>
                        ))}
                        {resource.count > preview.length && (
                          <span className="text-xs text-muted-foreground px-1 py-1">
                            +{resource.count - preview.length}
                          </span>
                        )}
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="px-5 py-(--row-py) whitespace-nowrap">
                    {gitOpsOwned > 0 ? (
                      <span className="inline-flex items-center gap-1 rounded-sm bg-status-warning/10 px-2 py-1 text-xs text-status-warning">
                        <Shield className="h-3 w-3" />
                        {gitOpsOwned} GitOps owned
                      </span>
                    ) : (
                      <span className="text-xs text-muted-foreground">
                        Direct edit allowed
                      </span>
                    )}
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}

export function PolicyValidationPanel({
  value,
  onChange,
  result,
  validating,
  onValidate,
}: {
  value: string;
  onChange: (value: string) => void;
  result?: ServiceMeshPolicyValidation;
  validating: boolean;
  onValidate: () => void;
}) {
  const findings = result ? [...result.errors, ...result.warnings] : [];
  return (
    <div className="rounded-lg border border-border bg-card overflow-hidden">
      <div className="px-5 py-4 border-b border-border flex items-center justify-between gap-4">
        <div>
          <h2 className="text-sm font-semibold text-foreground">
            Policy validation
          </h2>
          {result && (
            <p className="text-xs text-muted-foreground mt-1">
              {result.kind || "Object"}{" "}
              {result.namespace ? `${result.namespace}/` : ""}
              {result.name || ""}
            </p>
          )}
        </div>
        <ActionButton
          {...BARE_BUTTON}
          onClick={onValidate}
          disabled={validating || value.trim().length === 0}
          className="inline-flex items-center gap-1.5 h-8 px-3 rounded-sm text-xs font-medium
            border border-border text-foreground hover:bg-accent transition-colors
            disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {validating ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <CheckCircle2 className="h-3.5 w-3.5" />
          )}
          Validate
        </ActionButton>
      </div>
      <div className="grid gap-0 lg:grid-cols-[minmax(0,1fr)_360px]">
        <Textarea
          value={value}
          onChange={(event) => onChange(event.target.value)}
          spellCheck={false}
          className="min-h-72 w-full resize-y bg-background p-4 font-mono text-xs text-foreground outline-hidden border-b lg:border-b-0 lg:border-r border-border"
          placeholder={`apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: payments
  namespace: payments
spec:
  hosts:
    - payments.example.com`}
        />
        <div className="p-4 space-y-3">
          {result ? (
            <>
              <div
                className={`rounded-sm border px-3 py-2 text-xs ${
                  result.applyAllowed
                    ? "border-status-success/30 bg-status-success/10 text-status-success"
                    : "border-status-warning/30 bg-status-warning/10 text-status-warning"
                }`}
              >
                {result.applyAllowed
                  ? "Validation passed"
                  : result.readOnly
                    ? "GitOps-owned resource"
                    : "Validation requires changes"}
              </div>
              {findings.length > 0 ? (
                <div className="space-y-2">
                  {findings.map((finding, index) => (
                    <div
                      key={`${finding.field || "finding"}:${index}`}
                      className="rounded-sm border border-border p-3 text-xs"
                    >
                      <div className="font-medium text-foreground">
                        {finding.severity === "error" ? "Error" : "Warning"}
                        {finding.field ? `: ${finding.field}` : ""}
                      </div>
                      <p className="text-muted-foreground mt-1 leading-relaxed">
                        {finding.message}
                      </p>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="text-xs text-muted-foreground">
                  No validation findings.
                </p>
              )}
            </>
          ) : (
            <p className="text-xs text-muted-foreground">
              No validation result.
            </p>
          )}
        </div>
      </div>
    </div>
  );
}
