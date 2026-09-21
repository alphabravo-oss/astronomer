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
 * Cluster Templates · Detail.
 *
 * Read-only summary of the template + a list of clusters bound to it (one
 * row per cluster with its apply status). Edit jumps to `./edit` where the
 * full form is re-rendered.
 */
import { Link as RouterLink } from "@tanstack/react-router";
import { useNavigate } from "@tanstack/react-router";
import { ArrowLeft, PencilLine, Layers } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import {
  EmptyState,
  PermissionState,
  StatePanel,
} from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { PageHeader, PageShell } from "@/components/ui/page";
import { useCurrentUser } from "@/lib/hooks/auth";
import {
  useClusterTemplate,
  useClusterTemplateBoundClusters,
  canReadClusterTemplates,
  canWriteClusterTemplates,
} from "@/components/projects/hooks";
import { formatRelativeTime, cn } from "@/lib/utils";
import type { ClusterTemplateBoundCluster } from "@/lib/api/project-detail";

const statusStyles: Record<ClusterTemplateBoundCluster["status"], string> = {
  pending: "bg-status-warning/10 text-status-warning",
  applying: "bg-status-info/10 text-status-info",
  applied: "bg-status-success/10 text-status-success",
  failed: "bg-status-error/10 text-status-error",
};

function ClusterTemplateDetailPage() {
  const navigate = useNavigate();
  const params = Route.useParams();
  const id = params.id;
  const { data: user } = useCurrentUser();
  const canRead = canReadClusterTemplates(user);
  const canWrite = canWriteClusterTemplates(user);

  const templateQuery = useClusterTemplate(id);
  const boundQuery = useClusterTemplateBoundClusters(canRead ? id : undefined);

  if (!canRead) {
    return (
      <div className="space-y-4">
        <RouterLink
          to="/dashboard/cluster-templates"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to bundles
        </RouterLink>
        <PermissionState
          permission="cluster_templates:read"
          description={
            <>
              You need <span className="font-mono">cluster_templates:read</span>{" "}
              to view this bundle.
            </>
          }
          className="rounded-lg border border-border bg-muted/30 p-6"
        />
      </div>
    );
  }

  if (
    templateQuery.isLoading ||
    templateQuery.isError ||
    templateQuery.data === undefined
  ) {
    return (
      <QueryStates
        query={templateQuery}
        loadingTitle="Loading onboarding bundle"
        permission="cluster_templates:read"
        errorTitle="Failed to load onboarding bundle"
        notFound={
          <StatePanel
            icon={Layers}
            title="Bundle not found"
            description="The onboarding bundle may have been deleted or is outside your access scope."
            actionLabel="Back to bundles"
            actionHref="/dashboard/cluster-templates"
          />
        }
      >
        {null}
      </QueryStates>
    );
  }
  const template = templateQuery.data;

  return (
    <PageShell>
      <RouterLink
        to="/dashboard/cluster-templates"
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to bundles
      </RouterLink>

      <PageHeader
        eyebrow="Onboarding Bundle"
        title={
          <span className="flex items-center gap-2">
            <Layers className="h-5 w-5 text-muted-foreground" />
            {template.displayName}
            <span className="text-xs text-muted-foreground font-mono font-normal">
              {template.name}
            </span>
          </span>
        }
        description={template.description || undefined}
        actions={
          canWrite ? (
            <ActionButton
              icon={<PencilLine className="h-3.5 w-3.5" />}
              onClick={() =>
                void navigate({ to: `/dashboard/cluster-templates/${template.id}/edit` })
              }
            >
              Edit
            </ActionButton>
          ) : undefined
        }
      />

      {/* Summary */}
      <section className="rounded-xl border border-border bg-card p-5 space-y-3">
        <h2 className="text-sm font-medium text-foreground">Spec</h2>
        <dl className="grid grid-cols-1 md:grid-cols-2 gap-x-6 gap-y-2 text-sm">
          <DetailRow label="Environment" value={template.spec.environment} />
          <DetailRow
            label="Tools"
            value={
              template.spec.tools.length === 0
                ? "—"
                : template.spec.tools
                    .map((t) => `${t.slug}${t.preset ? `:${t.preset}` : ""}`)
                    .join(", ")
            }
          />
          <DetailRow
            label="Labels"
            value={
              template.spec.labels.length === 0
                ? "—"
                : template.spec.labels
                    .map((l) => `${l.key}=${l.value}`)
                    .join(", ")
            }
          />
          <DetailRow
            label="Default PSA"
            value={template.spec.defaultProject.podSecurityProfile}
          />
          <DetailRow
            label="Default netpol"
            value={template.spec.defaultProject.networkPolicyMode}
          />
          <DetailRow
            label="Default CPU quota"
            value={template.spec.defaultProject.resourceQuotaCpu ?? "unlimited"}
          />
          <DetailRow
            label="Default memory quota"
            value={
              template.spec.defaultProject.resourceQuotaMemory ?? "unlimited"
            }
          />
          <DetailRow
            label="Default pod quota"
            value={
              template.spec.defaultProject.resourceQuotaPods != null
                ? String(template.spec.defaultProject.resourceQuotaPods)
                : "unlimited"
            }
          />
          <DetailRow
            label="Token rotation"
            value={`${template.spec.registrationPolicy.tokenRotationDays} days`}
          />
          <DetailRow
            label="Approval required"
            value={
              template.spec.registrationPolicy.requireApproval ? "yes" : "no"
            }
          />
        </dl>
        <p className="text-xs text-muted-foreground pt-2 border-t border-border">
          Created {formatRelativeTime(template.createdAt)}
          {template.createdBy ? ` by ${template.createdBy}` : ""} · Updated{" "}
          {formatRelativeTime(template.updatedAt)}
        </p>
      </section>

      {/* Bound clusters */}
      <section className="rounded-xl border border-border bg-card overflow-hidden">
        <div className="px-5 py-3 border-b border-border">
          <h2 className="text-sm font-medium text-foreground">
            Bound clusters
          </h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Registered clusters this bundle has been applied to.
          </p>
        </div>
        <QueryStates
          query={boundQuery}
          loadingTitle="Loading bound clusters"
          permission="cluster_templates:read"
          errorTitle="Failed to load bound clusters"
          isEmpty={(clusters) => clusters.length === 0}
          empty={
            <EmptyState
              icon={Layers} title="No clusters bound"
              description="Apply this bundle during cluster registration to track its rollout here."
              className="py-10"
              actionLabel="Register cluster" actionHref="/dashboard/clusters/register"
            />
          }
        >
          {(bound) => (
            <Table className="w-full text-sm">
              <TableHeader>
                <TableRow className="text-xs text-muted-foreground border-b border-border bg-muted/30">
                  <TableHead className="text-left font-medium py-2 px-4">
                    Cluster
                  </TableHead>
                  <TableHead className="text-left font-medium py-2 px-4">
                    Status
                  </TableHead>
                  <TableHead className="text-left font-medium py-2 px-4">
                    Last applied
                  </TableHead>
                  <TableHead className="text-left font-medium py-2 px-4">
                    Detail
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {bound.map((row) => (
                  <TableRow
                    key={row.clusterId}
                    className="border-b border-border last:border-0"
                  >
                    <TableCell className="py-2 px-4">
                      <RouterLink
                        to="/dashboard/clusters/$id" params={{ id: row.clusterId }}
                        className="text-foreground hover:underline underline-offset-2"
                      >
                        {row.clusterName}
                      </RouterLink>
                    </TableCell>
                    <TableCell className="py-2 px-4">
                      <span
                        className={cn(
                          "inline-flex px-2 py-0.5 rounded-sm text-xs font-medium capitalize",
                          statusStyles[row.status] ??
                            "bg-muted text-muted-foreground",
                        )}
                      >
                        {row.status}
                      </span>
                    </TableCell>
                    <TableCell className="py-2 px-4 text-xs text-muted-foreground">
                      {row.lastAppliedAt
                        ? formatRelativeTime(row.lastAppliedAt)
                        : "—"}
                    </TableCell>
                    <TableCell className="py-2 px-4 text-xs text-muted-foreground truncate max-w-[260px]">
                      {row.message || "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </QueryStates>
      </section>
    </PageShell>
  );
}

function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="text-foreground font-mono text-xs break-all">{value}</dd>
    </>
  );
}

export const Route = createFileRoute("/dashboard/cluster-templates/$id/")({
  component: ClusterTemplateDetailPage,
});
