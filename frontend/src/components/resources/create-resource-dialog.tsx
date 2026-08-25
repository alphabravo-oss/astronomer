"use client";

import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import { Loader2 } from "lucide-react";

import {
  GuidedResourceForm,
  type KubernetesManifest,
} from "@/components/resources/guided-resource-form";
import { ModalShell } from "@/components/ui/modal-shell";
import { YamlEditor } from "@/components/ui/yaml-editor";
import { useK8sCreate, useResourceSchema } from "@/lib/hooks";
import type { ResourceSchemaView, ResourceType } from "@/lib/api/resources";
import { k8sTemplates } from "@/lib/k8s-templates";
import { toastApiError, toastError } from "@/lib/toast";
import { cn } from "@/lib/utils";

interface CreateResourceDialogProps {
  open: boolean;
  onClose: () => void;
  clusterId: string;
  /** Resource type key from k8sTemplates (e.g. "deployment", "service") */
  templateKey: string;
  /** Display title */
  title: string;
  /** K8s API path to POST to. The live discovery contract is preferred. */
  apiPath?: string;
  resourceType?: ResourceType;
}

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

const KIND_TO_PLURAL: Record<string, string> = {
  Deployment: "deployments",
  StatefulSet: "statefulsets",
  DaemonSet: "daemonsets",
  Job: "jobs",
  CronJob: "cronjobs",
  Service: "services",
  Ingress: "ingresses",
  Gateway: "gateways",
  NetworkPolicy: "networkpolicies",
  ConfigMap: "configmaps",
  Secret: "secrets",
  Namespace: "namespaces",
  PersistentVolumeClaim: "persistentvolumeclaims",
  PodDisruptionBudget: "poddisruptionbudgets",
  HorizontalPodAutoscaler: "horizontalpodautoscalers",
  ServiceAccount: "serviceaccounts",
  Role: "roles",
  RoleBinding: "rolebindings",
};

export function createPathForManifest(
  body: KubernetesManifest,
  schema?: ResourceSchemaView,
  explicitPath?: string,
): string | undefined {
  if (explicitPath) return explicitPath.replace(/^\//, "");
  const metadata = body.metadata as { namespace?: string } | null | undefined;
  const namespace = metadata?.namespace || "default";
  if (schema?.resource.apiBase && schema.resource.plural) {
    const base = schema.resource.apiBase.replace(/^\//, "");
    return schema.resource.namespaced
      ? `${base}/namespaces/${namespace}/${schema.resource.plural}`
      : `${base}/${schema.resource.plural}`;
  }
  const apiVersion = typeof body.apiVersion === "string" ? body.apiVersion : "";
  const kind = typeof body.kind === "string" ? body.kind : "";
  const plural = KIND_TO_PLURAL[kind];
  if (!apiVersion || !plural) return undefined;
  const base = apiVersion.includes("/")
    ? `apis/${apiVersion}`
    : `api/${apiVersion}`;
  return kind === "Namespace"
    ? `${base}/${plural}`
    : `${base}/namespaces/${namespace}/${plural}`;
}

export function CreateResourceDialog({
  open,
  onClose,
  clusterId,
  templateKey,
  title,
  apiPath,
  resourceType,
}: CreateResourceDialogProps) {
  const resolvedResourceType =
    resourceType ?? TEMPLATE_RESOURCE_TYPES[templateKey];
  const [mode, setMode] = useState<"guided" | "yaml">("guided");
  const [yamlContent, setYamlContent] = useState("");
  const [manifest, setManifest] = useState<KubernetesManifest>({});
  const [guidedValid, setGuidedValid] = useState(false);
  const [parseError, setParseError] = useState<string | null>(null);
  const modeRequestRef = useRef(0);
  const k8sCreate = useK8sCreate();
  const schemaQuery = useResourceSchema(
    clusterId,
    resolvedResourceType ?? "deployments",
    open && !!resolvedResourceType,
  );

  useEffect(() => {
    if (!open) return;
    modeRequestRef.current += 1;
    const template = k8sTemplates[templateKey] || "";
    setYamlContent(template);
    setMode("guided");
    setParseError(null);
    void import("js-yaml").then((yaml) => {
      const parsed = yaml.load(template);
      setManifest(
        parsed && typeof parsed === "object"
          ? (parsed as KubernetesManifest)
          : {},
      );
    });
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
        const parsed = yaml.load(yamlContent);
        if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
          throw new Error("YAML must contain one Kubernetes object.");
        }
        setManifest(parsed as KubernetesManifest);
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
          noCompatMode: true,
        }),
      );
      setParseError(null);
    }
    setMode(next);
  };

  const editorModes = ["guided", "yaml"] as const;
  const handleModeKeyDown = (
    event: KeyboardEvent<HTMLButtonElement>,
    index: number,
  ) => {
    let nextIndex: number | undefined;
    if (event.key === "ArrowRight" || event.key === "ArrowDown") {
      nextIndex = (index + 1) % editorModes.length;
    } else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
      nextIndex = (index - 1 + editorModes.length) % editorModes.length;
    } else if (event.key === "Home") {
      nextIndex = 0;
    } else if (event.key === "End") {
      nextIndex = editorModes.length - 1;
    } else {
      return;
    }
    event.preventDefault();
    const next = editorModes[nextIndex];
    void changeMode(next);
    const tabs =
      event.currentTarget.parentElement?.querySelectorAll<HTMLElement>(
        '[role="tab"]',
      );
    tabs?.[nextIndex]?.focus();
  };

  const handleCreate = async () => {
    const yaml = await import("js-yaml");
    try {
      const body =
        mode === "guided"
          ? manifest
          : (yaml.load(yamlContent) as KubernetesManifest);
      if (!body || typeof body !== "object" || Array.isArray(body)) {
        throw new Error("YAML must contain one Kubernetes object.");
      }
      const path = createPathForManifest(body, schema, apiPath);
      if (!path) {
        toastError(
          `Cannot determine the API endpoint for ${String(body.kind || templateKey)}.`,
        );
        return;
      }
      k8sCreate.mutate({ clusterId, path, body }, { onSuccess: onClose });
    } catch (error) {
      toastApiError("Invalid resource definition", error);
    }
  };

  const createDisabled =
    k8sCreate.isPending ||
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
              : "YAML mode preserves exact Kubernetes object keys."}
          </p>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={onClose}
              className="h-8 rounded px-3 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={handleCreate}
              disabled={createDisabled}
              className="inline-flex h-8 items-center gap-1.5 rounded bg-primary px-4 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-50"
            >
              {k8sCreate.isPending && (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              )}
              Create
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
        {editorModes.map((item, index) => (
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

      <div
        id={`resource-editor-panel-${mode}`}
        role="tabpanel"
        aria-labelledby={`resource-editor-tab-${mode}`}
        tabIndex={0}
        className="min-h-0 flex-1 overflow-hidden focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        {mode === "guided" ? (
          <GuidedResourceForm
            value={manifest}
            onChange={setManifest}
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
            }}
            className="h-full"
          />
        )}
      </div>
    </ModalShell>
  );
}
