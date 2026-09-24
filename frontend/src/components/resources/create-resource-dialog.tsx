import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import { CheckCircle2, Loader2, XCircle } from "lucide-react";

import { LazyGuidedResourceForm as GuidedResourceForm } from "@/components/resources/lazy-guided-resource-form";
import type { KubernetesManifest } from "@/components/resources/guided-resource-model";
import { ModalShell } from "@/components/ui/modal-shell";
import { YamlEditor } from "@/components/ui/yaml-editor";
import {
  useK8sCreateBatch,
  useResourceSchema,
} from "@/lib/hooks/kubernetes-proxy";
import type {
  K8sCreateBatchItem,
  K8sCreateBatchResult,
} from "@/lib/hooks/kubernetes-proxy";
import type { ResourceType } from "@/lib/api/resources";
import { useClusterDiscovery } from "@/components/layout/use-cluster-discovery-nav";
import {
  createManifestBatch,
  normalizeManifestDocuments,
} from "./create-resource-manifest";
import { k8sTemplates } from "@/lib/k8s-templates";
import { extractApiErrorMessage } from "@/lib/api/errors";
import { toastApiError, toastError, toastSuccess } from "@/lib/toast";
import { cn } from "@/lib/utils";

interface CreateResourceDialogProps {
  open: boolean;
  onClose: () => void;
  clusterId: string;
  /**
   * Resource type key from k8sTemplates (e.g. "deployment", "service").
   * Omit for a bare "Import YAML" flow with no starting template: the
   * dialog opens directly in YAML mode with the guided/YAML toggle hidden.
   */
  templateKey?: string;
  /** Display title */
  title: string;
  /** K8s API path to POST to. The live discovery contract is preferred. */
  apiPath?: string;
  resourceType?: ResourceType;
  /**
   * Starting YAML for the kind-less import flow (e.g. Clone), rendered in
   * YAML mode with the guided/YAML toggle hidden just like the bare import
   * flow. Ignored when `templateKey` is set.
   */
  initialYaml?: string;
}

const IMPORT_YAML_PLACEHOLDER =
  "# Paste one or more Kubernetes manifests, separated by ---\n";

const TEMPLATE_RESOURCE_TYPES: Record<string, ResourceType> = {
  deployment: "deployments",
  statefulset: "statefulsets",
  daemonset: "daemonsets",
  job: "jobs",
  cronjob: "cronjobs",
  service: "services",
  ingress: "ingresses",
  networkpolicy: "networkpolicies",
  configmap: "configmaps",
  secret: "secrets",
  namespace: "namespaces",
  persistentvolumeclaim: "persistentvolumeclaims",
  serviceaccount: "serviceaccounts",
  role: "k8s-roles",
  rolebinding: "k8s-rolebindings",
  hpa: "hpa",
  poddisruptionbudget: "poddisruptionbudgets",
  gateway: "gateways",
};

const EDITOR_MODES = ["guided", "yaml"] as const;

/** Arrow/Home/End navigation for the guided/yaml tablist. */
function nextEditorModeIndex(
  key: string,
  index: number,
  length: number,
): number | undefined {
  if (key === "ArrowRight" || key === "ArrowDown") return (index + 1) % length;
  if (key === "ArrowLeft" || key === "ArrowUp")
    return (index - 1 + length) % length;
  if (key === "Home") return 0;
  if (key === "End") return length - 1;
  return undefined;
}

export function CreateResourceDialog(props: CreateResourceDialogProps) {
  return props.open ? (
    <CreateResourceEditor
      key={`${props.clusterId}:${props.templateKey}`}
      {...props}
    />
  ) : null;
}

function CreateResourceEditor({
  open,
  onClose,
  clusterId,
  templateKey,
  title,
  apiPath,
  resourceType,
  initialYaml,
}: CreateResourceDialogProps) {
  const resolvedResourceType =
    resourceType ??
    (templateKey ? TEMPLATE_RESOURCE_TYPES[templateKey] : undefined);
  const [mode, setMode] = useState<"guided" | "yaml">(
    templateKey ? "guided" : "yaml",
  );
  const [yamlContent, setYamlContent] = useState(
    templateKey
      ? k8sTemplates[templateKey] || ""
      : (initialYaml ?? IMPORT_YAML_PLACEHOLDER),
  );
  const [manifest, setManifest] = useState<KubernetesManifest>({});
  const [guidedValid, setGuidedValid] = useState(false);
  const [parseError, setParseError] = useState<string | null>(null);
  const [applyResults, setApplyResults] = useState<K8sCreateBatchResult[]>([]);
  const [editorDirty, setEditorDirty] = useState(false);
  const modeRequestRef = useRef(0);
  const k8sCreateBatch = useK8sCreateBatch();
  const discovery = useClusterDiscovery(clusterId);
  const schemaQuery = useResourceSchema(
    clusterId,
    resolvedResourceType ?? "deployments",
    open && !!resolvedResourceType,
  );

  useEffect(() => {
    if (!open || !templateKey) return;
    modeRequestRef.current += 1;
    const template = k8sTemplates[templateKey] || "";
    let cancelled = false;
    void import("js-yaml").then((yaml) => {
      if (cancelled) return;
      const parsed = yaml.load(template);
      setManifest(
        parsed && typeof parsed === "object"
          ? (parsed as KubernetesManifest)
          : {},
      );
    });
    return () => {
      cancelled = true;
      modeRequestRef.current += 1;
    };
  }, [open, templateKey]);

  const schema = schemaQuery.data;
  const hasGuidedTemplate = useMemo(
    () => !!resolvedResourceType && Object.keys(manifest).length > 0,
    [manifest, resolvedResourceType],
  );

  if (!open) return null;

  const changeMode = async (next: "guided" | "yaml") => {
    const request = ++modeRequestRef.current;
    // Selecting the current mode also cancels a superseded async transition.
    if (next === mode) return;
    const yaml = await import("js-yaml");
    if (request !== modeRequestRef.current) return;
    if (next === "guided") {
      try {
        const documents: unknown[] = [];
        yaml.loadAll(yamlContent, (document) => documents.push(document));
        const parsed = normalizeManifestDocuments(documents);
        if (parsed.length !== 1) {
          throw new Error(
            "Guided mode supports one object. Keep multi-document definitions in YAML mode.",
          );
        }
        setManifest(parsed[0]);
        setParseError(null);
      } catch (error) {
        setParseError(error instanceof Error ? error.message : "Invalid YAML");
        return;
      }
    } else {
      setYamlContent(
        yaml.dump(manifest, {
          noRefs: true,
          lineWidth: 100,
        }),
      );
      setParseError(null);
    }
    setEditorDirty(true);
    setMode(next);
  };

  const handleModeKeyDown = (
    event: KeyboardEvent<HTMLButtonElement>,
    index: number,
  ) => {
    const nextIndex = nextEditorModeIndex(
      event.key,
      index,
      EDITOR_MODES.length,
    );
    if (nextIndex === undefined) return;
    event.preventDefault();
    void changeMode(EDITOR_MODES[nextIndex]);
    const tabs =
      event.currentTarget.parentElement?.querySelectorAll<HTMLElement>(
        '[role="tab"]',
      );
    tabs?.[nextIndex]?.focus();
  };

  const handleCreate = async () => {
    const yaml = await import("js-yaml");
    try {
      let items: K8sCreateBatchItem[];
      const failed = applyResults.filter((result) => !result.ok);
      if (failed.length > 0 && !editorDirty) {
        items = failed.map(({ id, path, body, label }) => ({
          id,
          path,
          body,
          label,
        }));
      } else {
        const documents: unknown[] = [];
        if (mode === "guided") {
          documents.push(manifest);
        } else {
          yaml.loadAll(yamlContent, (document) => documents.push(document));
        }
        items = createManifestBatch(
          documents,
          schema,
          apiPath,
          discovery,
        ).filter(
          (item) =>
            !applyResults.some(
              (previous) =>
                previous.ok &&
                previous.path === item.path &&
                previous.label === item.label,
            ),
        );
      }

      const next = await k8sCreateBatch.mutateAsync({ clusterId, items });
      const merged = [...applyResults.filter((result) => result.ok), ...next];
      setApplyResults(merged);
      setEditorDirty(false);
      const failedCount = merged.filter((result) => !result.ok).length;
      if (failedCount === 0) {
        toastSuccess(
          `${merged.length} Kubernetes ${merged.length === 1 ? "resource" : "resources"} created`,
        );
      } else {
        toastError(
          `${failedCount} of ${merged.length} ${failed.length > 0 ? "retried" : "submitted"} resources failed`,
        );
      }
    } catch (error) {
      toastApiError("Invalid resource definition", error);
    }
  };

  const allApplied =
    !editorDirty &&
    applyResults.length > 0 &&
    applyResults.every((result) => result.ok);
  const createDisabled =
    k8sCreateBatch.isPending ||
    (mode === "guided" && (!guidedValid || !hasGuidedTemplate)) ||
    (mode === "yaml" && !!parseError);

  return (
    <ModalShell
      title={title}
      onClose={onClose}
      size="xl"
      panelClassName="w-[94vw] h-[88vh] max-w-5xl flex flex-col overflow-hidden"
      bodyClassName="flex-1 min-h-0 p-0 space-y-0"
      footer={
        <div className="flex w-full flex-wrap items-center justify-between gap-3">
          <p className="text-xs text-muted-foreground">
            {mode === "guided"
              ? "Validated fields are projected to the same Kubernetes object shown in YAML mode."
              : "YAML mode preserves exact keys and accepts up to 50 ordered documents."}
          </p>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={onClose}
              className="h-8 rounded-sm px-3 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={allApplied ? onClose : handleCreate}
              disabled={createDisabled}
              className="inline-flex h-8 items-center gap-1.5 rounded-sm bg-primary px-4 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-50"
            >
              {k8sCreateBatch.isPending && (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              )}
              {allApplied
                ? "Done"
                : applyResults.some((result) => !result.ok)
                  ? "Retry failed"
                  : "Create"}
            </button>
          </div>
        </div>
      }
    >
      <div
        className="flex border-b border-border px-4 pt-2"
        role="tablist"
        aria-label="Resource editor mode"
      >
        {templateKey &&
          EDITOR_MODES.map((item, index) => (
            <button
              key={item}
              id={`resource-editor-tab-${item}`}
              type="button"
              role="tab"
              aria-selected={mode === item}
              aria-controls={`resource-editor-panel-${item}`}
              tabIndex={mode === item ? 0 : -1}
              onClick={() => void changeMode(item)}
              onKeyDown={(event) => handleModeKeyDown(event, index)}
              className={cn(
                "border-b-2 px-4 py-2 text-sm font-medium capitalize",
                mode === item
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground",
              )}
            >
              {item}
            </button>
          ))}
        <div className="ml-auto self-center pb-2 text-xs text-muted-foreground">
          {schemaQuery.isLoading
            ? "Loading live cluster schema…"
            : schema?.schemaAvailable
              ? `Schema: ${schema.schemaName || schema.resource.kind}`
              : "Live schema unavailable; template validation active"}
        </div>
      </div>

      {parseError && (
        <div
          className="border-b border-status-error/30 bg-status-error/10 px-5 py-2 text-xs text-status-error"
          role="alert"
        >
          {parseError}
        </div>
      )}

      {applyResults.length > 0 && (
        <div
          className="max-h-40 overflow-y-auto border-b border-border bg-muted/20"
          aria-label="Resource creation results"
        >
          {applyResults.map((result) => (
            <div
              key={result.id}
              className="flex items-start gap-2 border-b border-border/60 px-5 py-2 text-xs last:border-b-0"
            >
              {result.ok ? (
                <CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-success" />
              ) : (
                <XCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-error" />
              )}
              <span className="min-w-0 flex-1">
                <span className="font-mono text-foreground">
                  {result.label}
                </span>
                {!result.ok && (
                  <span className="ml-2 text-status-error">
                    {extractApiErrorMessage(result.error) ?? "Create failed"}
                  </span>
                )}
              </span>
              <span
                className={
                  result.ok ? "text-status-success" : "text-status-error"
                }
              >
                {result.ok ? "Created" : "Failed"}
              </span>
            </div>
          ))}
        </div>
      )}

      <div
        id={`resource-editor-panel-${mode}`}
        role="tabpanel"
        aria-labelledby={`resource-editor-tab-${mode}`}
        tabIndex={0}
        className="min-h-0 flex-1 overflow-hidden focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
      >
        {mode === "guided" ? (
          <GuidedResourceForm
            value={manifest}
            onChange={(next) => {
              setManifest(next);
              setEditorDirty(true);
            }}
            schema={schema?.schema ?? {}}
            definitions={schema?.definitions ?? {}}
            onValidationChange={setGuidedValid}
          />
        ) : (
          <YamlEditor
            value={yamlContent}
            onChange={(next) => {
              setYamlContent(next);
              setParseError(null);
              setEditorDirty(true);
            }}
            className="h-full"
          />
        )}
      </div>
    </ModalShell>
  );
}
