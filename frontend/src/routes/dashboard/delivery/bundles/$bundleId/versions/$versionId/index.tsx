import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  DeliveryShell,
  Detail,
  DetailGrid,
  useDeliveryWorkspace,
  withProjectQuery,
} from "@/components/delivery/shared";
import {
  getComponentBundleVersion,
  type ComponentBundleVersion,
} from "@/lib/api/delivery-bundles";
import { useCurrentUser } from "@/lib/hooks/auth";
import { can } from "@/lib/permissions";
import { queryKeys } from "@/lib/query-keys";

type PrecedenceRow = {
  order: number;
  layer: string;
  scope: string;
  evidence: string;
  content?: unknown;
};

const sensitiveKey =
  /(password|passwd|token|secret|private.?key|credential|api.?key)/i;

function redactValues(value: unknown, key = ""): unknown {
  if (sensitiveKey.test(key)) return "[write-only or Secret reference]";
  if (Array.isArray(value)) return value.map((item) => redactValues(item));
  if (value && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>).map(
        ([childKey, child]) => [childKey, redactValues(child, childKey)],
      ),
    );
  }
  return value;
}

function precedenceRows(version: ComponentBundleVersion): PrecedenceRow[] {
  const renderer = version.rendererSpec;
  const configured =
    renderer.kind === "helm"
      ? redactValues(renderer.helm?.values ?? {})
      : `${renderer.kustomize?.patches?.length ?? 0} immutable patch(es); patch bodies are hidden from this view.`;
  return [
    {
      order: 1,
      layer: "Artifact defaults",
      scope: "Published chart or source revision",
      evidence:
        version.artifactDigest ||
        version.resolvedRevision ||
        "Resolution pending",
    },
    {
      order: 2,
      layer: "Bundle version",
      scope: "Project",
      evidence: "Immutable renderer configuration",
      content: configured,
    },
    {
      order: 3,
      layer: "Placement overrides",
      scope: "Group / cluster / rollout",
      evidence: "No persisted override set is attached to this version.",
    },
    {
      order: 4,
      layer: "Frozen rollout",
      scope: "Selected clusters",
      evidence: `Effective input is bound by ${version.specDigest}`,
    },
  ];
}

const columns: Column<PrecedenceRow>[] = [
  { key: "order", header: "Order", accessor: (row) => row.order },
  {
    key: "layer",
    header: "Layer",
    accessor: (row) => <span className="font-medium">{row.layer}</span>,
  },
  { key: "scope", header: "Scope", accessor: (row) => row.scope },
  {
    key: "evidence",
    header: "Effective evidence",
    accessor: (row) => (
      <div className="max-w-2xl space-y-2 whitespace-normal">
        <p>{row.evidence}</p>
        {row.content !== undefined && (
          <pre className="max-h-72 overflow-auto rounded-md border border-border bg-muted/30 p-3 text-xs">
            {typeof row.content === "string"
              ? row.content
              : JSON.stringify(row.content, null, 2)}
          </pre>
        )}
      </div>
    ),
  },
];

function BundleVersionDetailPage() {
  const { bundleId, versionId } = Route.useParams();
  const { projectId, projects, projectQuery, setProjectId } =
    useDeliveryWorkspace();
  const { data: user } = useCurrentUser();
  const allowed = can(user, "delivery_bundles", "read", {
    type: "project",
    id: projectId,
  });
  const version = useQuery({
    queryKey: queryKeys.delivery.bundleVersion(projectId, bundleId, versionId),
    queryFn: ({ signal }) =>
      getComponentBundleVersion(projectId, bundleId, versionId, signal),
    enabled: Boolean(projectId && bundleId && versionId && allowed),
  });
  return (
    <DeliveryShell
      projectId={projectId}
      projects={projects}
      setProjectId={setProjectId}
    >
      <DeliveryProjectGate
        projectId={projectId}
        loading={projectQuery.isLoading}
        error={projectQuery.isError}
        projectsCount={projects.length}
        permission="delivery_bundles:read"
        allowed={allowed}
        onRetry={() => void projectQuery.refetch()}
      >
        <PageShell>
          <Link
            to={withProjectQuery(
              `/dashboard/delivery/bundles/${bundleId}`,
              projectId,
            )}
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
          >
            <ArrowLeft className="h-4 w-4" /> Bundle versions
          </Link>
          <PageHeader
            eyebrow="Immutable configuration"
            title={version.data?.version ?? "Bundle version"}
            description="Source identity, renderer configuration, and deterministic precedence used to create frozen rollout plans."
          />
          {version.data && (
            <>
              <DetailGrid>
                <Detail label="Renderer" value={version.data.renderer} />
                <Detail label="Scope" value={version.data.scope} />
                <Detail
                  label="Verification"
                  value={
                    <DeliveryPhaseBadge
                      value={version.data.verificationStatus}
                    />
                  }
                />
                <Detail
                  label="Spec digest"
                  value={version.data.specDigest}
                  mono
                />
              </DetailGrid>
              <PageSection
                title="Values and patch precedence"
                description="Later layers take precedence. Sensitive-looking values are redacted and Kustomize patch bodies are intentionally not displayed."
              >
                <DataTable
                  data={precedenceRows(version.data)}
                  columns={columns}
                  keyExtractor={(row) => String(row.order)}
                  searchable={false}
                />
              </PageSection>
            </>
          )}
        </PageShell>
      </DeliveryProjectGate>
    </DeliveryShell>
  );
}

export const Route = createFileRoute(
  "/dashboard/delivery/bundles/$bundleId/versions/$versionId/",
)({ component: BundleVersionDetailPage });
