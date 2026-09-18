import { useEffect, useMemo, useState } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  ArrowLeft,
  Box,
  CalendarDays,
  CheckCircle2,
  Code2,
  ExternalLink,
  GitBranch,
  HardDrive,
  Layers3,
  Package,
  ShieldCheck,
  Star,
  Tags,
  TriangleAlert,
  UserRound,
} from "lucide-react";

import { CatalogIcon } from "@/components/catalog/catalog-icon";
import { CatalogSourceBadge } from "@/components/catalog/catalog-source-badge";
import { ActionButton } from "@/components/ui/action-button";
import { Link } from "@/lib/link";
import { useParams, useRouter } from "@/lib/navigation";
import {
  useCatalogApplications,
  useCatalogUserDiscovery,
  useHelmRepositories,
  useSetCatalogChartFavorite,
} from "@/lib/hooks/catalog";
import {
  getHelmChart,
  getHelmChartReadme,
  getHelmChartVersions,
} from "@/lib/api/catalog";
import { buildCatalogPresentationIndex } from "@/lib/catalogs/astronomer";
import { catalogSourcePresentation } from "@/lib/catalogs/source";
import { queryKeys } from "@/lib/query-keys";

function ChartDetailPage() {
  const { id: clusterId, chartId } = useParams() as {
    id: string;
    chartId: string;
  };
  const router = useRouter();
  const queryClient = useQueryClient();
  const chart = useQuery({
    queryKey: queryKeys.catalog.chart(clusterId, chartId),
    queryFn: () => getHelmChart(clusterId, chartId),
    enabled: Boolean(clusterId && chartId),
  });
  const versions = useQuery({
    queryKey: queryKeys.catalog.chartVersions(clusterId, chartId),
    queryFn: () => getHelmChartVersions(clusterId, chartId),
    enabled: Boolean(clusterId && chartId),
  });
  const [selectedVersion, setSelectedVersion] = useState("");
  const version =
    versions.data?.find((item) => item.id === selectedVersion) ??
    versions.data?.[0];
  const readme = useQuery({
    queryKey: queryKeys.catalog.chartReadme(clusterId, chartId, version?.version),
    queryFn: () => getHelmChartReadme(clusterId, chartId, version?.version),
    enabled: Boolean(clusterId && chartId && version?.version),
  });
  const repositories = useHelmRepositories(clusterId);
  const applications = useCatalogApplications();
  const discovery = useCatalogUserDiscovery();
  const setFavorite = useSetCatalogChartFavorite();
  const discoveryEntry = discovery.data?.find((item) => item.chartId === chartId);
  useEffect(() => {
    if (!chart.data) return;
    void queryClient.invalidateQueries({ queryKey: queryKeys.catalog.discovery });
  }, [chart.data, queryClient]);
  const presentation = useMemo(() => {
    if (!chart.data) return undefined;
    const repository = repositories.data?.find(
      (item) => item.id === chart.data?.repositoryId,
    );
    return buildCatalogPresentationIndex(applications.data).get(
      `${repository?.name ?? ""}/${chart.data.name}`,
    );
  }, [applications.data, chart.data, repositories.data]);
  const repository = repositories.data?.find(
    (item) => item.id === chart.data?.repositoryId,
  );
  const source = chart.data
    ? catalogSourcePresentation(
        repository,
        Boolean(presentation),
        {
          repositoryId: chart.data.repositoryId,
          repositoryName: repository?.name,
        },
        presentation?.supportTier === "Astronomer" &&
          presentation.slug === "constellation",
      )
    : undefined;
  const maintainers = normalizeMaintainers(chart.data?.maintainers);
  const keywords = presentation?.keywords.length
    ? presentation.keywords
    : chart.data?.keywords ?? [];
  const latestPublished = version?.createdAtUpstream || version?.createdAt;
  const lifecycle = presentation
    ? Object.entries(presentation.lifecycle)
        .filter(([, enabled]) => enabled)
        .map(([name]) => humanize(name))
    : ["Install", "Upgrade", "Uninstall"];

  if (chart.isLoading || versions.isLoading) {
    return <div className="p-8 text-sm text-muted-foreground">Loading application details…</div>;
  }
  if (!chart.data) {
    return <div className="p-8 text-sm text-status-error">This chart is unavailable in the selected project.</div>;
  }

  return (
    <div className="space-y-6 p-4">
      <Link href={`/dashboard/clusters/${clusterId}/apps/charts`} className="inline-flex items-center gap-1 text-sm text-primary hover:underline">
        <ArrowLeft className="h-4 w-4" /> Back to charts
      </Link>

      <header className="rounded-xl border border-border bg-card p-6 shadow-sm">
        <div className="flex flex-col gap-5 lg:flex-row lg:items-start lg:justify-between">
          <div className="flex min-w-0 gap-4">
            <CatalogIcon src={presentation?.iconUrl || chart.data.iconUrl} label={presentation?.displayName || chart.data.displayName} className="h-16 w-16 rounded-xl" imageClassName="h-14 w-14" />
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <h1 className="text-3xl font-semibold tracking-tight text-foreground">
                  {presentation?.displayName || chart.data.displayName}
                </h1>
                {source && <CatalogSourceBadge source={source} />}
              </div>
              <p className="mt-2 max-w-3xl text-sm leading-relaxed text-muted-foreground">
                {presentation?.description || chart.data.description || "No description supplied by the publisher."}
              </p>
              <div className="mt-3 flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
                <span>Repository: <strong className="text-foreground">{repository?.name || "Unknown"}</strong></span>
                <span>Chart: <strong className="font-mono text-foreground">{chart.data.name}</strong></span>
                {version?.appVersion && <span>App: <strong className="text-foreground">{version.appVersion}</strong></span>}
              </div>
              {keywords.length > 0 && (
                <div className="mt-4 flex flex-wrap gap-1.5">
                  {keywords.slice(0, 10).map((keyword) => (
                    <span key={keyword} className="rounded-full border border-border bg-muted/40 px-2 py-0.5 text-xs font-medium text-foreground">
                      {keyword}
                    </span>
                  ))}
                </div>
              )}
            </div>
          </div>
          <div className="flex min-w-56 flex-col gap-2">
            <label htmlFor="chart-version" className="text-xs font-medium text-muted-foreground">Version</label>
            <select id="chart-version" value={version?.id ?? ""} onChange={(event) => setSelectedVersion(event.target.value)} className="h-10 rounded-md border border-border bg-background px-3 text-sm">
              {(versions.data ?? []).map((item) => (
                <option key={item.id} value={item.id}>{item.version}{item.appVersion ? ` · app ${item.appVersion}` : ""}</option>
              ))}
            </select>
            <ActionButton
              intent="primary"
              className="mt-2 h-12 justify-center px-7 text-base font-semibold shadow-sm"
              icon={<Package className="h-5 w-5" />}
              onClick={() => router.push(`/dashboard/clusters/${clusterId}/apps/charts/${chartId}/install?version=${encodeURIComponent(version?.id ?? "")}`)}
              disabled={!version}
            >
              Install application
            </ActionButton>
            <ActionButton
              className="h-10 justify-center"
              icon={<Star className={discoveryEntry?.favorite ? "h-4 w-4 fill-current text-status-warning" : "h-4 w-4"} />}
              onClick={() =>
                setFavorite.mutate({
                  clusterId,
                  chartId,
                  favorite: !discoveryEntry?.favorite,
                })
              }
              loading={setFavorite.isPending}
            >
              {discoveryEntry?.favorite ? "Remove favorite" : "Add favorite"}
            </ActionButton>
          </div>
        </div>
      </header>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="min-w-0 space-y-6">
          <section className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <SummaryCard icon={<Layers3 className="h-4 w-4" />} label="Versions" value={`${versions.data?.length ?? 0} available`} detail={`Selected ${version?.version ?? "—"}`} />
            <SummaryCard icon={<GitBranch className="h-4 w-4" />} label="Application" value={version?.appVersion || "Not declared"} detail="Upstream app version" />
            <SummaryCard icon={<HardDrive className="h-4 w-4" />} label="Storage" value={presentation?.storage || "Chart-defined"} detail={presentation ? "Curated guidance" : "Review values before install"} />
            <SummaryCard icon={presentation?.privileged ? <TriangleAlert className="h-4 w-4" /> : <CheckCircle2 className="h-4 w-4" />} label="Privilege" value={presentation?.privileged ? "Elevated access" : "No elevated access declared"} detail="Preflight verifies before deploy" />
          </section>

          <section className="rounded-xl border border-border bg-card p-6">
            <div className="mb-4">
              <h2 className="text-lg font-semibold text-foreground">What happens when you install</h2>
              <p className="mt-1 text-sm text-muted-foreground">The target namespace is selected on the next page. Astronomer checks your effective RBAC there and derives any project ownership automatically.</p>
            </div>
            <div className="grid gap-3 md:grid-cols-3">
              <ProcessItem number="1" title="Choose target" detail="Select an authorized namespace and release name." />
              <ProcessItem number="2" title="Review configuration" detail="Use the generated form, then inspect the complete Helm values YAML." />
              <ProcessItem number="3" title="Reconcile with Flux" detail="Watch preflight, deployment events, and final readiness." />
            </div>
          </section>

          <section className="min-w-0 rounded-xl border border-border bg-card p-6">
            <h2 className="mb-4 text-lg font-semibold text-foreground">Publisher documentation</h2>
            {readme.isLoading ? (
              <p className="text-sm text-muted-foreground">Loading publisher documentation…</p>
            ) : readme.data ? (
              <div className="prose prose-sm max-w-none dark:prose-invert prose-headings:scroll-mt-20 prose-a:font-medium prose-a:text-primary prose-pre:overflow-auto prose-table:block prose-table:overflow-x-auto">
                <ReactMarkdown remarkPlugins={[remarkGfm]}>{readme.data}</ReactMarkdown>
              </div>
            ) : (
              <div className="rounded-lg border border-dashed border-border p-6 text-center">
                <Code2 className="mx-auto h-6 w-6 text-muted-foreground" />
                <p className="mt-2 text-sm font-medium text-foreground">No README was published for this version</p>
                <p className="mt-1 text-xs text-muted-foreground">Repository metadata and chart defaults remain available during configuration.</p>
              </div>
            )}
          </section>

          <section className="rounded-xl border border-border bg-card p-6">
            <div className="mb-4 flex items-end justify-between gap-3">
              <div><h2 className="text-lg font-semibold text-foreground">Version history</h2><p className="mt-1 text-sm text-muted-foreground">Choose a version above to load its documentation and install it.</p></div>
              <span className="text-xs font-medium text-muted-foreground">{versions.data?.length ?? 0} versions</span>
            </div>
            <div className="divide-y divide-border rounded-lg border border-border">
              {(versions.data ?? []).slice(0, 12).map((item, index) => (
                <button key={item.id} type="button" onClick={() => setSelectedVersion(item.id)} className="flex w-full items-center justify-between gap-4 px-4 py-3 text-left hover:bg-muted/30">
                  <div><div className="flex items-center gap-2"><span className="font-mono text-sm font-semibold text-foreground">{item.version}</span>{index === 0 && <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-primary">Latest</span>}{item.id === version?.id && <span className="text-xs font-medium text-status-success">Selected</span>}</div><p className="mt-0.5 text-xs text-muted-foreground">Application {item.appVersion || "version not declared"}</p></div>
                  <span className="whitespace-nowrap text-xs text-muted-foreground">{formatDate(item.createdAtUpstream || item.createdAt)}</span>
                </button>
              ))}
            </div>
          </section>
        </div>

        <aside className="space-y-4">
          <InfoCard title="Operational profile" icon={<Box className="h-4 w-4" />}>
            <InfoRow label="Support" value={presentation?.supportTier || "Community"} />
            <InfoRow label="Category" value={presentation?.category || chart.data.category} />
            <InfoRow label="Resources" value={presentation?.resourceProfile || "Not declared"} />
            <InfoRow label="Storage" value={presentation?.storage || "Not declared"} />
          </InfoCard>
          <InfoCard title="Install considerations" icon={<TriangleAlert className="h-4 w-4" />}>
            <InfoRow label="Privileged" value={presentation?.privileged ? "Required" : "No"} />
            <InfoRow label="Default enabled" value={presentation?.defaultEnabled ? "Yes" : "No"} />
            <InfoRow label="Kubernetes" value={String(presentation?.compatibility.kubernetes || "Not declared")} />
            <InfoRow label="Lifecycle" value={lifecycle.join(", ")} />
          </InfoCard>
          <InfoCard title="Publisher" icon={<UserRound className="h-4 w-4" />}>
            <InfoRow label="Maintainers" value={maintainers.length ? maintainers.map((item) => item.name).join(", ") : "Not supplied"} />
            <InfoRow label="Repository" value={repository?.name || "Unknown"} />
            <InfoRow label="Chart" value={chart.data.name} mono />
          </InfoCard>
          <InfoCard title="Release metadata" icon={<CalendarDays className="h-4 w-4" />}>
            <InfoRow label="Chart version" value={version?.version || "Unknown"} />
            <InfoRow label="App version" value={version?.appVersion || "Not declared"} />
            <InfoRow label="Published" value={formatDate(latestPublished)} />
            <InfoRow label="Deprecated" value={chart.data.deprecated ? "Yes" : "No"} />
          </InfoCard>
          <InfoCard title="Artifact trust" icon={<ShieldCheck className="h-4 w-4" />}>
            <InfoRow label="Verification" value={presentation?.verificationStatus || "Repository metadata"} />
            {version?.digest && <InfoRow label="Chart digest" value={version.digest} mono />}
            {presentation?.catalogDigest && <InfoRow label="Catalog digest" value={presentation.catalogDigest} mono />}
          </InfoCard>
          <InfoCard title="Links &amp; discovery" icon={<Tags className="h-4 w-4" />}>
            {(presentation?.documentationUrl || chart.data.homeUrl) ? <a href={presentation?.documentationUrl || chart.data.homeUrl || "#"} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-sm font-semibold text-primary hover:underline">Publisher site <ExternalLink className="h-3.5 w-3.5" /></a> : <span className="text-sm text-muted-foreground">No publisher link supplied.</span>}
            {maintainers.filter((item) => item.url).map((item) => <a key={`${item.name}-${item.url}`} href={item.url} target="_blank" rel="noreferrer" className="flex items-center gap-1 text-xs font-medium text-primary hover:underline">{item.name} <ExternalLink className="h-3 w-3" /></a>)}
          </InfoCard>
        </aside>
      </div>
    </div>
  );
}

function InfoCard({ title, icon, children }: { title: string; icon: React.ReactNode; children: React.ReactNode }) {
  return <section className="rounded-xl border border-border bg-card p-4"><h2 className="mb-3 flex items-center gap-2 text-sm font-semibold text-foreground">{icon}{title}</h2><div className="space-y-2">{children}</div></section>;
}

function InfoRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return <div className="flex items-start justify-between gap-3 text-xs"><span className="text-muted-foreground">{label}</span><span className={`${mono ? "max-w-44 break-all font-mono text-[10px]" : "text-right"} text-foreground`}>{value}</span></div>;
}

function SummaryCard({ icon, label, value, detail }: { icon: React.ReactNode; label: string; value: string; detail: string }) {
  return <section className="rounded-xl border border-border bg-card p-4"><div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">{icon}{label}</div><p className="mt-3 text-sm font-semibold text-foreground">{value}</p><p className="mt-1 text-xs text-muted-foreground">{detail}</p></section>;
}

function ProcessItem({ number, title, detail }: { number: string; title: string; detail: string }) {
  return <div className="flex gap-3 rounded-lg border border-border bg-muted/10 p-4"><span className="flex h-7 w-7 flex-none items-center justify-center rounded-full bg-primary/10 text-xs font-bold text-primary">{number}</span><div><h3 className="text-sm font-semibold text-foreground">{title}</h3><p className="mt-1 text-xs leading-relaxed text-muted-foreground">{detail}</p></div></div>;
}

function humanize(value: string): string {
  return value.replace(/[-_]/g, " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function formatDate(value?: string): string {
  if (!value) return "Not declared";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Not declared" : date.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

function normalizeMaintainers(value: unknown): Array<{ name: string; url: string }> {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (!item || typeof item !== "object") return [];
    const record = item as Record<string, unknown>;
    const name = typeof record.name === "string" ? record.name : "";
    const url = typeof record.url === "string" ? record.url : typeof record.email === "string" ? `mailto:${record.email}` : "";
    return name ? [{ name, url }] : [];
  });
}

export const Route = createFileRoute("/dashboard/clusters/$id/apps/charts/$chartId/")({
  validateSearch: (search: Record<string, unknown>) => search as { version?: string },
  component: ChartDetailPage,
});
