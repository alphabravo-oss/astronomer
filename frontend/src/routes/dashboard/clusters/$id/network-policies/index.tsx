import { Select } from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { createFileRoute } from "@tanstack/react-router";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
/**
 * Cluster detail → Network policies tab. Lists every applied
 * NetworkPolicy template for this cluster, grouped by namespace. The
 * "Apply template" action picks a template + one or more namespaces and
 * fires the per-namespace POST. Migration 068.
 */

import { useState } from "react";
import {
  useNetworkPolicyApplications,
  useNetworkPolicyTemplates,
} from "@/lib/hooks/policy-queries";
import { QueryStates } from "@/components/ui/query-states";
import { StatusBadge } from "@/components/ui/status-badge";

import { Link as RouterLink } from "@tanstack/react-router";
import { ArrowLeft, Plus, Trash2, RefreshCw, Loader2 } from "lucide-react";
import { toastApiError, toastError, toastSuccess } from "@/lib/toast";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";

import {
  applyNetworkPolicy,
  deleteNetworkPolicyApplication,
  reapplyNetworkPolicyApplication,
  type NetworkPolicyApplication,
} from "@/lib/api/settings";

function ClusterNetworkPoliciesPage() {
  const params = Route.useParams();
  const clusterID = params?.id ?? "";

  const applicationsQuery = useNetworkPolicyApplications(clusterID);
  const templatesQuery = useNetworkPolicyTemplates();
  const apps = applicationsQuery.data ?? [];
  const templates = templatesQuery.data ?? [];
  const loading = applicationsQuery.isLoading || templatesQuery.isLoading;
  const [openApply, setOpenApply] = useState(false);
  const [pickedTemplate, setPickedTemplate] = useState("");
  const [namespaces, setNamespaces] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [revokeTarget, setRevokeTarget] =
    useState<NetworkPolicyApplication | null>(null);
  const [revoking, setRevoking] = useState(false);

  const refresh = async () => {
    await Promise.all([applicationsQuery.refetch(), templatesQuery.refetch()]);
  };

  const handleApply = async () => {
    if (!pickedTemplate) {
      toastError("Pick a template first");
      return;
    }
    const list = namespaces
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    if (list.length === 0) {
      toastError("Enter at least one namespace");
      return;
    }
    setSubmitting(true);
    try {
      await applyNetworkPolicy(clusterID, {
        template_id: pickedTemplate,
        namespaces: list,
      });
      toastSuccess(`Applied to ${list.length} namespace(s)`);
      setOpenApply(false);
      setPickedTemplate("");
      setNamespaces("");
      await refresh();
    } catch (err: unknown) {
      toastApiError("Apply failed", err);
    } finally {
      setSubmitting(false);
    }
  };

  const handleRevoke = async () => {
    if (!revokeTarget) return;
    setRevoking(true);
    try {
      await deleteNetworkPolicyApplication(clusterID, revokeTarget.id);
      toastSuccess("Application revoked");
      setRevokeTarget(null);
      await refresh();
    } catch (err: unknown) {
      toastApiError("Revoke failed", err);
    } finally {
      setRevoking(false);
    }
  };

  const handleReapply = async (app: NetworkPolicyApplication) => {
    try {
      await reapplyNetworkPolicyApplication(clusterID, app.id);
      toastSuccess("Reapply queued");
      await refresh();
    } catch (err: unknown) {
      toastApiError("Reapply failed", err);
    }
  };

  if (applicationsQuery.isError)
    return <QueryStates query={applicationsQuery}>{null}</QueryStates>;
  if (templatesQuery.isError)
    return <QueryStates query={templatesQuery}>{null}</QueryStates>;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <RouterLink
          to="/dashboard/clusters/$id" params={{ id: clusterID }}
          className="inline-flex items-center text-sm text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-4 w-4 mr-1" /> Back to cluster
        </RouterLink>
        <button
          type="button"
          onClick={() => setOpenApply((v) => !v)}
          className="inline-flex items-center gap-1 px-3 py-1.5 text-sm rounded-sm border border-border bg-card hover:bg-muted"
        >
          <Plus className="h-4 w-4" /> Apply template
        </button>
      </div>

      <div>
        <h1 className="text-2xl font-semibold tracking-tight">
          Network policies
        </h1>
        <p className="text-sm text-muted-foreground mt-1 max-w-3xl">
          NetworkPolicy templates applied to namespaces in this cluster. The
          reconciler keeps each application server-side-applied; drifting rows
          are re-stamped on the next 5m tick.
        </p>
      </div>

      {openApply && (
        <div className="rounded-lg border border-border bg-card p-4 space-y-3">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <label className="text-sm space-y-1 block">
              <span className="text-muted-foreground">Template</span>
              <Select
                className="w-full px-2 py-1 rounded-sm border border-border bg-background text-sm"
                value={pickedTemplate}
                onChange={(e) => setPickedTemplate(e.target.value)}
              >
                <option value="">Pick one...</option>
                {templates
                  .filter((t) => t.enabled)
                  .map((t) => (
                    <option key={t.id} value={t.id}>
                      {t.name} ({t.slug})
                    </option>
                  ))}
              </Select>
            </label>
            <label className="text-sm space-y-1 block">
              <span className="text-muted-foreground">Namespaces</span>
              <Input
                type="text"
                className="w-full px-2 py-1 rounded-sm border border-border bg-background text-sm font-mono"
                placeholder="team-a, team-b"
                value={namespaces}
                onChange={(e) => setNamespaces(e.target.value)}
              />
              <span className="text-xs text-muted-foreground">
                Comma- or space-separated
              </span>
            </label>
          </div>
          <button
            type="button"
            onClick={handleApply}
            disabled={submitting}
            className="inline-flex items-center gap-1 px-3 py-1.5 text-sm rounded-sm border border-border bg-foreground text-background hover:opacity-90 disabled:opacity-50"
          >
            {submitting ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Plus className="h-4 w-4" />
            )}
            Apply
          </button>
        </div>
      )}

      {loading ? (
        <div className="flex items-center text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 mr-2 animate-spin" /> Loading...
        </div>
      ) : (
        <div className="overflow-x-auto rounded-lg border border-border">
          <Table className="w-full text-sm">
            <TableHeader className="bg-muted">
              <TableRow className="text-left">
                <TableHead className="px-3 py-2 font-medium">
                  Template
                </TableHead>
                <TableHead className="px-3 py-2 font-medium">
                  Namespace
                </TableHead>
                <TableHead className="px-3 py-2 font-medium">
                  Policy name
                </TableHead>
                <TableHead className="px-3 py-2 font-medium">Status</TableHead>
                <TableHead className="px-3 py-2 font-medium">
                  Last applied
                </TableHead>
                <TableHead className="px-3 py-2 text-right" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {apps.map((a) => (
                <TableRow
                  key={a.id}
                  className="border-b border-border last:border-0"
                >
                  <TableCell className="px-3 py-2 font-mono text-xs">
                    {a.template_slug ?? a.template_id}
                  </TableCell>
                  <TableCell className="px-3 py-2 font-mono text-xs">
                    {a.namespace}
                  </TableCell>
                  <TableCell className="px-3 py-2 font-mono text-xs">
                    {a.policy_name}
                  </TableCell>
                  <TableCell className="px-3 py-2">
                    <StatusBadge status={a.status} />
                    {a.last_error && (
                      <div className="text-xs text-status-error mt-0.5">
                        {a.last_error}
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="px-3 py-2 text-xs text-muted-foreground">
                    {a.last_applied_at ?? "—"}
                  </TableCell>
                  <TableCell className="px-3 py-2 text-right">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        type="button"
                        onClick={() => handleReapply(a)}
                        className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-sm border border-border hover:bg-muted"
                      >
                        <RefreshCw className="h-3 w-3" /> Reapply
                      </button>
                      <button
                        type="button"
                        onClick={() => setRevokeTarget(a)}
                        className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-sm border border-status-error/30 text-status-error hover:bg-status-error/10"
                      >
                        <Trash2 className="h-3 w-3" />
                      </button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
              {apps.length === 0 && (
                <TableRow>
                  <TableCell
                    colSpan={6}
                    className="px-3 py-6 text-center text-sm text-muted-foreground"
                  >
                    <div className="space-y-3">
                      <p>No network policies applied yet.</p>
                      <p className="text-xs">
                        Pick a curated template from the{" "}
                        <a
                          href="/dashboard/settings/platform"
                          className="underline"
                        >
                          platform network-policy templates
                        </a>{" "}
                        and click <em>Apply template</em> to roll one out.
                      </p>
                    </div>
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      )}
      <ConfirmDialog
        open={revokeTarget !== null}
        onClose={() => setRevokeTarget(null)}
        onConfirm={() => void handleRevoke()}
        title="Revoke network policy"
        description="The reconciler will stop enforcing this template in the selected namespace."
        confirmText="Revoke"
        variant="destructive"
        loading={revoking}
        impact={
          revokeTarget
            ? {
                scope: `${revokeTarget.template_slug ?? revokeTarget.template_id} in namespace ${revokeTarget.namespace}`,
                consequences: [
                  `The managed NetworkPolicy ${revokeTarget.policy_name} will be removed.`,
                  "Traffic previously denied by this policy may become reachable.",
                ],
                recovery: "Apply the template to this namespace again.",
              }
            : undefined
        }
      />
    </div>
  );
}

export const Route = createFileRoute(
  "/dashboard/clusters/$id/network-policies/",
)({
  component: ClusterNetworkPoliciesPage,
});
