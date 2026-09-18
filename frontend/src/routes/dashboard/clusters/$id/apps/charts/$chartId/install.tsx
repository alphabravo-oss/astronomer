import { useEffect, useMemo, useState } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowLeft,
  ArrowRight,
  Check,
  CheckCircle2,
  Circle,
  FileCode2,
  Loader2,
  PackageCheck,
  ShieldAlert,
  XCircle,
} from "lucide-react";

import { HelmValuesForm } from "@/components/catalog/helm-values-form";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { OverlayShell } from "@/components/ui/overlay-shell";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { Link } from "@/lib/link";
import { useParams, useRouter, useSearchParams } from "@/lib/navigation";
import { useClusterNamespaces } from "@/lib/hooks";
import {
  getCatalogOperation,
  getHelmChart,
  getHelmChartValues,
  getHelmChartVersions,
  installHelmChart,
  previewCatalogInstallation,
  type CatalogInstallationAccepted,
  type CatalogInstallationPreview,
  type CatalogOperation,
} from "@/lib/api/catalog";
import {
  dumpHelmValuesYAML,
  hasRenderableSchema,
  mergeSchemaDefaults,
  parseHelmValuesYAML,
  resolveSchemaRefs,
  type HelmValuesObject,
  type HelmValuesSchemaNode,
} from "@/lib/helm-values-schema";
import { queryKeys } from "@/lib/query-keys";
import { cn } from "@/lib/utils";
import { toastApiError } from "@/lib/toast";

const STEPS = ["Target", "Configure", "Review values", "Preflight & deploy"] as const;

function InstallChartPage() {
  const { id: clusterId, chartId } = useParams() as { id: string; chartId: string };
  const search = useSearchParams();
  const router = useRouter();
  const queryClient = useQueryClient();
  const requestedVersionId = search?.get("version") ?? "";
  const namespaces = useClusterNamespaces(clusterId);
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
  const [step, setStep] = useState(0);
  const [versionId, setVersionId] = useState(requestedVersionId);
  const [releaseName, setReleaseName] = useState("");
  const [namespace, setNamespace] = useState("");
  const [valuesYAML, setValuesYAML] = useState("");
  const [schemaValues, setSchemaValues] = useState<HelmValuesObject>({});
  const [yamlError, setYAMLError] = useState<string | null>(null);
  const [preview, setPreview] = useState<CatalogInstallationPreview | null>(null);
  const [accepted, setAccepted] = useState<CatalogInstallationAccepted | null>(null);
  const selectedVersion =
    versions.data?.find((version) => version.id === versionId) ?? versions.data?.[0];
  const values = useQuery({
    queryKey: queryKeys.catalog.installChartValues(clusterId, chartId, selectedVersion?.version),
    queryFn: () => getHelmChartValues(clusterId, chartId, selectedVersion?.version),
    enabled: Boolean(clusterId && chartId && selectedVersion?.version),
  });
  const schema = useMemo(() => {
    const resolved = resolveSchemaRefs(values.data?.valuesSchema ?? {});
    return hasRenderableSchema(resolved) ? (resolved as HelmValuesSchemaNode) : null;
  }, [values.data?.valuesSchema]);

  useEffect(() => {
    if (!versions.data?.length) return;
    if (!versions.data.some((version) => version.id === versionId)) {
      setVersionId(versions.data[0].id);
    }
  }, [versionId, versions.data]);

  useEffect(() => {
    if (!chart.data || releaseName) return;
    setReleaseName(chart.data.name);
  }, [chart.data, releaseName]);

  useEffect(() => {
    if (!namespaces.data?.length || namespace) return;
    setNamespace(
      namespaces.data.some((item) => item.name === "default")
        ? "default"
        : namespaces.data[0].name,
    );
  }, [namespace, namespaces.data]);

  useEffect(() => {
    if (!values.data) return;
    const yaml = values.data.defaultValues || "{}\n";
    const parsed = parseHelmValuesYAML(yaml) ?? {};
    setValuesYAML(yaml);
    setSchemaValues(
      schema ? (mergeSchemaDefaults(schema, parsed) as HelmValuesObject) : parsed,
    );
    setYAMLError(null);
    setPreview(null);
  }, [schema, values.data]);

  const previewMutation = useMutation({
    mutationFn: () =>
      previewCatalogInstallation({
        cluster_id: clusterId,
        chart_version_id: selectedVersion?.id ?? "",
        namespace,
        values_override: valuesYAML,
      }),
    onSuccess: setPreview,
    onError: (error) => toastApiError("Preflight failed", error),
  });
  const installMutation = useMutation({
    mutationFn: () =>
      installHelmChart({
        cluster_id: clusterId,
        chart_version_id: selectedVersion?.id ?? "",
        release_name: releaseName.trim(),
        namespace,
        values_override: valuesYAML,
      }),
    onSuccess: (result) => {
      setAccepted(result);
      void queryClient.invalidateQueries({ queryKey: queryKeys.catalog.operations });
    },
    onError: (error) => toastApiError("Installation failed", error),
  });

  const handleSchemaChange = (next: HelmValuesObject) => {
    setSchemaValues(next);
    setValuesYAML(dumpHelmValuesYAML(next));
    setYAMLError(null);
    setPreview(null);
  };
  const handleYAMLChange = (next: string) => {
    setValuesYAML(next);
    setPreview(null);
    const parsed = parseHelmValuesYAML(next);
    if (parsed == null) {
      setYAMLError("Values must be valid YAML with an object at the document root.");
      return;
    }
    setYAMLError(null);
    if (schema) setSchemaValues(mergeSchemaDefaults(schema, parsed) as HelmValuesObject);
  };
  const targetReady = Boolean(
    selectedVersion &&
      releaseName.trim() &&
      namespace &&
      namespaces.data?.some((item) => item.name === namespace),
  );
  const canContinue = step === 0 ? targetReady : step === 1 ? !yamlError : true;

  return (
    <div className="space-y-6 p-4">
      <Link href={`/dashboard/clusters/${clusterId}/apps/charts/${chartId}`} className="inline-flex items-center gap-1 text-sm text-primary hover:underline">
        <ArrowLeft className="h-4 w-4" /> Back to application details
      </Link>
      <header>
        <h1 className="text-2xl font-semibold text-foreground">Install {chart.data?.displayName || chart.data?.name || "application"}</h1>
        <p className="mt-1 text-sm text-muted-foreground">Choose a namespace on this cluster, configure the Flux-managed release, and review the exact values before anything changes.</p>
      </header>

      <ol className="grid grid-cols-2 gap-2 rounded-xl border border-border bg-card p-3 lg:grid-cols-4">
        {STEPS.map((label, index) => (
          <li key={label} className={cn("flex items-center gap-2 rounded-lg px-3 py-2 text-sm", index === step ? "bg-primary/10 font-semibold text-primary" : index < step ? "text-status-success" : "text-muted-foreground")}>
            <span className={cn("flex h-6 w-6 items-center justify-center rounded-full border text-xs", index === step ? "border-primary" : index < step ? "border-status-success bg-status-success/10" : "border-border")}>
              {index < step ? <Check className="h-3.5 w-3.5" /> : index + 1}
            </span>
            {label}
          </li>
        ))}
      </ol>

      <main className="min-h-[28rem] rounded-xl border border-border bg-card p-6">
        {step === 0 && (
          <section className="mx-auto max-w-3xl space-y-6">
            <div><h2 className="text-lg font-semibold text-foreground">Choose the deployment target</h2><p className="mt-1 text-sm text-muted-foreground">Astronomer shows namespaces visible to you and verifies install permission against the selected namespace. Project ownership, quotas, and policy are derived automatically.</p></div>
            <div className="grid gap-5 md:grid-cols-2">
              <Field label="Cluster"><Input value={clusterId} disabled /></Field>
              <Field label="Chart version">
                <Select value={selectedVersion?.id ?? ""} onChange={(event) => { setVersionId(event.target.value); setPreview(null); }}>
                  {(versions.data ?? []).map((version) => <option key={version.id} value={version.id}>{version.version}{version.appVersion ? ` · app ${version.appVersion}` : ""}</option>)}
                </Select>
              </Field>
              <Field label="Release name"><Input value={releaseName} onChange={(event) => setReleaseName(event.target.value)} placeholder="my-release" /></Field>
              <Field label="Namespace" description="Only namespaces visible to your current RBAC identity are listed. Final write authorization is checked by the server.">
                <Select value={namespace} onChange={(event) => setNamespace(event.target.value)}>
                  <option value="">Select a namespace…</option>
                  {(namespaces.data ?? []).map((item) => <option key={item.name} value={item.name}>{item.name}</option>)}
                </Select>
              </Field>
            </div>
            {namespaces.data && namespaces.data.length === 0 && (
              <div className="flex gap-2 rounded-lg border border-status-warning/30 bg-status-warning/10 p-4 text-sm text-status-warning">
                <AlertTriangle className="mt-0.5 h-4 w-4 flex-none" /> No deployable namespaces are visible to your account on this cluster.
              </div>
            )}
          </section>
        )}

        {step === 1 && (
          <section className="mx-auto max-w-4xl space-y-5">
            <div><h2 className="text-lg font-semibold text-foreground">Configure common values</h2><p className="mt-1 text-sm text-muted-foreground">This form is generated from the publisher's standard Helm JSON Schema. The next step exposes the complete YAML document.</p></div>
            {values.isLoading ? <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" /> Loading chart values…</div> : schema ? (
              <HelmValuesForm schema={schema} value={schemaValues} onChange={handleSchemaChange} />
            ) : (
              <div className="rounded-lg border border-dashed border-border p-8 text-center"><FileCode2 className="mx-auto h-7 w-7 text-muted-foreground" /><p className="mt-3 text-sm font-medium text-foreground">This chart does not publish a usable values schema</p><p className="mt-1 text-xs text-muted-foreground">Continue to edit every value safely in the complete YAML review.</p></div>
            )}
          </section>
        )}

        {step === 2 && (
          <section className="space-y-5">
            <div className="flex flex-col gap-2 lg:flex-row lg:items-end lg:justify-between"><div><h2 className="text-lg font-semibold text-foreground">Review effective values</h2><p className="mt-1 text-sm text-muted-foreground">This is the actual values document Astronomer will submit to Flux. Edit settings not exposed by the form here.</p></div><div className="text-xs text-muted-foreground"><strong className="text-foreground">{releaseName}</strong> in <strong className="text-foreground">{namespace}</strong> · chart {selectedVersion?.version}</div></div>
            <Textarea aria-label="Effective Helm values" value={valuesYAML} onChange={(event) => handleYAMLChange(event.target.value)} className="min-h-[30rem] resize-y font-mono text-xs leading-relaxed" />
            {yamlError && <div className="flex items-center gap-2 rounded-md border border-status-error/30 bg-status-error/10 px-3 py-2 text-xs text-status-error"><XCircle className="h-4 w-4" />{yamlError}</div>}
          </section>
        )}

        {step === 3 && (
          <section className="mx-auto max-w-4xl space-y-5">
            <div><h2 className="text-lg font-semibold text-foreground">Preflight and deploy</h2><p className="mt-1 text-sm text-muted-foreground">Astronomer verifies catalog trust, compatibility, privilege, resources, storage, and target ownership before accepting the Flux operation.</p></div>
            <div className="grid gap-3 rounded-lg border border-border bg-muted/15 p-4 text-sm md:grid-cols-2"><Summary label="Cluster" value={clusterId} /><Summary label="Namespace" value={namespace} /><Summary label="Release" value={releaseName} /><Summary label="Version" value={selectedVersion?.version || ""} /></div>
            {!preview ? (
              <div className="rounded-lg border border-dashed border-border p-8 text-center"><ShieldAlert className="mx-auto h-8 w-8 text-muted-foreground" /><p className="mt-3 text-sm font-medium text-foreground">Ready for server-side preflight</p><p className="mt-1 text-xs text-muted-foreground">No cluster change occurs during this check.</p><ActionButton className="mt-4" intent="primary" loading={previewMutation.isPending} onClick={() => previewMutation.mutate()}>Run preflight</ActionButton></div>
            ) : (
              <div className="space-y-3">
                <div className={cn("rounded-lg border p-4", preview.allowed ? "border-status-success/30 bg-status-success/5" : "border-status-error/30 bg-status-error/5")}><div className="flex items-center gap-2 font-semibold text-foreground">{preview.allowed ? <CheckCircle2 className="h-5 w-5 text-status-success" /> : <XCircle className="h-5 w-5 text-status-error" />}{preview.allowed ? "Ready to deploy" : "Deployment blocked"}</div></div>
                {preview.checks.map((check) => <div key={check.code} className="flex items-start gap-3 rounded-lg border border-border p-3"><CheckIcon status={check.status} /><div><p className="text-sm font-medium text-foreground">{check.title}</p><p className="text-xs text-muted-foreground">{check.description}</p></div></div>)}
                <div className="rounded-lg border border-border bg-muted/20 p-3 font-mono text-[10px] text-muted-foreground"><div>catalog {preview.catalog_digest}</div><div>artifact {preview.artifact_digest}</div><div>values {preview.values_digest}</div></div>
                <div className="flex justify-end"><ActionButton intent="primary" className="h-11 px-6 text-sm font-semibold" loading={installMutation.isPending} disabled={!preview.allowed} icon={<PackageCheck className="h-4 w-4" />} onClick={() => installMutation.mutate()}>Deploy with Flux</ActionButton></div>
              </div>
            )}
          </section>
        )}
      </main>

      <footer className="flex items-center justify-between">
        <ActionButton onClick={() => setStep((current) => Math.max(0, current - 1))} disabled={step === 0 || installMutation.isPending} icon={<ArrowLeft className="h-4 w-4" />}>Back</ActionButton>
        {step < STEPS.length - 1 && <ActionButton intent="primary" onClick={() => setStep((current) => Math.min(STEPS.length - 1, current + 1))} disabled={!canContinue} icon={<ArrowRight className="h-4 w-4" />}>Continue</ActionButton>}
      </footer>

      {accepted && <DeploymentProgress accepted={accepted} clusterId={clusterId} onClose={() => router.push(`/dashboard/clusters/${clusterId}/apps/installed`)} />}
    </div>
  );
}

function DeploymentProgress({ accepted, clusterId, onClose }: { accepted: CatalogInstallationAccepted; clusterId: string; onClose: () => void }) {
  const operationId = accepted.operation.id ?? "";
  const operation = useQuery({
    queryKey: queryKeys.catalog.operation(operationId),
    queryFn: () => getCatalogOperation(operationId),
    enabled: Boolean(operationId),
    refetchInterval: (query) => {
      const status = (query.state.data as CatalogOperation | undefined)?.status;
      return status === "completed" || status === "failed" || status === "superseded" ? false : 2_000;
    },
  });
  const current = operation.data ?? accepted.operation;
  const done = current.status === "completed";
  const failed = current.status === "failed" || current.status === "superseded";
  const events = current.events ?? [];
  const handleClose = () => {
    if (done || failed) onClose();
  };
  return (
    <OverlayShell
      onClose={handleClose}
      closeOnBackdrop={done || failed}
      rootClassName="p-4"
    >
      <div className="relative max-h-[90vh] w-full max-w-2xl overflow-y-auto rounded-2xl border border-border bg-card p-6 shadow-2xl" role="dialog" aria-modal="true" aria-label="Deployment progress">
        <div className="flex items-start gap-4"><div className={cn("flex h-12 w-12 items-center justify-center rounded-full", done ? "bg-status-success/10 text-status-success" : failed ? "bg-status-error/10 text-status-error" : "bg-primary/10 text-primary")}>{done ? <CheckCircle2 className="h-7 w-7" /> : failed ? <XCircle className="h-7 w-7" /> : <Loader2 className="h-7 w-7 animate-spin" />}</div><div><h2 className="text-xl font-semibold text-foreground">{done ? "Application deployed" : failed ? "Deployment needs attention" : "Deploying application"}</h2><p className="mt-1 text-sm text-muted-foreground">{accepted.installation.releaseName} · {accepted.installation.namespace}</p></div></div>
        <div className="mt-6 space-y-2">
          {events.length ? events.map((event, index) => <div key={event.id ?? `${event.stage}-${index}`} className="flex gap-3 rounded-lg border border-border bg-muted/10 p-3"><span className="mt-0.5"><CheckCircle2 className={cn("h-4 w-4", event.level === "error" ? "text-status-error" : "text-status-success")} /></span><div><div className="flex flex-wrap items-center gap-2"><span className="text-xs font-semibold uppercase tracking-wide text-foreground">{event.stage}</span><span className="text-[10px] text-muted-foreground">{event.createdAt ? new Date(event.createdAt).toLocaleTimeString() : ""}</span></div><p className="mt-0.5 text-sm text-muted-foreground">{event.message}</p></div></div>) : <div className="flex items-center gap-3 rounded-lg border border-border p-4 text-sm text-muted-foreground"><Circle className="h-4 w-4" />Operation accepted; waiting for the first Flux delivery event…</div>}
        </div>
        {current.errorMessage && <div className="mt-4 rounded-lg border border-status-error/30 bg-status-error/10 p-3 text-sm text-status-error">{current.errorMessage}</div>}
        <div className="mt-6 flex flex-wrap items-center justify-between gap-3"><Link href={`/dashboard/clusters/${clusterId}/apps/operations?operation=${encodeURIComponent(operationId)}`} className="text-sm font-medium text-primary hover:underline">Open operation details</Link><ActionButton intent={done ? "primary" : "default"} disabled={!done && !failed} onClick={onClose}>{done ? "View installed app" : failed ? "Close" : "Deployment in progress…"}</ActionButton></div>
      </div>
    </OverlayShell>
  );
}

function Field({ label, description, children }: { label: string; description?: string; children: React.ReactNode }) { return <label className="space-y-1.5"><span className="text-sm font-medium text-foreground">{label}</span>{children}{description && <span className="block text-xs text-muted-foreground">{description}</span>}</label>; }
function Summary({ label, value }: { label: string; value: string }) { return <div><div className="text-xs text-muted-foreground">{label}</div><div className="mt-0.5 font-medium text-foreground">{value}</div></div>; }
function CheckIcon({ status }: { status: string }) { if (status === "ready") return <CheckCircle2 className="mt-0.5 h-4 w-4 flex-none text-status-success" />; if (status === "blocking") return <XCircle className="mt-0.5 h-4 w-4 flex-none text-status-error" />; return <AlertTriangle className="mt-0.5 h-4 w-4 flex-none text-status-warning" />; }

export const Route = createFileRoute("/dashboard/clusters/$id/apps/charts/$chartId/install")({
  validateSearch: (search: Record<string, unknown>) => search as { version?: string },
  component: InstallChartPage,
});
