"use client";

import { useState, useEffect, useRef } from "react";
import {
  useK8sGetYaml,
  useK8sApplyYaml,
  useK8sDryRunYaml,
  useResourceSchema,
} from "@/lib/hooks";
import { YamlEditor } from "@/components/ui/yaml-editor";
import { ModalShell } from "@/components/ui/modal-shell";
import {
  GuidedResourceForm,
  type KubernetesManifest,
} from "@/components/resources/guided-resource-form";
import { Loader2, Pencil, Eye, AlertTriangle } from "lucide-react";
import { cn } from "@/lib/utils";
import * as apiClient from "@/lib/api";
import type { ResourceType } from "@/lib/api/resources";
import type { PermissionDecision } from "@/lib/permissions";
import { toastWarning } from "@/lib/toast";
import { ErrorState } from "@/components/ui/empty-state";
import {
  buildYamlDiff,
  classifyResourceApplyFailure,
  yamlTextMatches,
  YamlDiffPreview,
  type YamlApplyPreviewModel,
} from "@/components/ui/yaml-apply-preview";

export { classifyResourceApplyFailure } from "@/components/ui/yaml-apply-preview";

type ForceConflictPermission = Pick<
  PermissionDecision,
  "allowed" | "permission" | "reason" | "disabledReason"
>;

interface YamlViewDialogProps {
  open: boolean;
  onClose: () => void;
  clusterId: string;
  /** K8s API path (e.g. "api/v1/namespaces/default/pods/my-pod") */
  k8sPath: string;
  /** Display title */
  title: string;
  /** Start in edit mode */
  editMode?: boolean;
  /** If false, hide the edit toggle */
  allowEdit?: boolean;
  /** Exact cluster-scoped manage decision required for forced ownership. */
  forceConflictPermission?: ForceConflictPermission;
}

export function YamlViewDialog({
  open,
  onClose,
  clusterId,
  k8sPath,
  title,
  editMode: initialEditMode = false,
  allowEdit = true,
  forceConflictPermission,
}: YamlViewDialogProps) {
  if (!open) return null;

  return (
    <ModalShell
      title={title}
      onClose={onClose}
      size="xl"
      panelClassName="w-[90vw] h-[80vh] max-w-4xl flex flex-col overflow-hidden"
      bodyClassName="flex-1 min-h-0 p-0 space-y-0"
    >
      {/* ponytail: YamlPanel owns fetch/edit/dry-run and the View/Edit toggle; dialog is just chrome. */}
      <YamlPanel
        clusterId={clusterId}
        k8sPath={k8sPath}
        allowEdit={allowEdit}
        forceConflictPermission={forceConflictPermission}
        editMode={initialEditMode}
        active={open}
      />
    </ModalShell>
  );
}

interface YamlPanelProps {
  clusterId: string;
  /** K8s API path (e.g. "api/v1/namespaces/default/pods/my-pod") */
  k8sPath: string;
  /** If false, hide the edit toggle (read-only) */
  allowEdit?: boolean;
  /** Start in edit mode */
  editMode?: boolean;
  /** Exact cluster-scoped manage decision required for forced ownership. */
  forceConflictPermission?: ForceConflictPermission;
  /** When false, fetching is paused (used by the dialog when closed). Defaults true. */
  active?: boolean;
}

const K8S_PLURAL_RESOURCE_TYPE: Record<string, ResourceType> = {
  deployments: "deployments",
  statefulsets: "statefulsets",
  daemonsets: "daemonsets",
  jobs: "jobs",
  cronjobs: "cronjobs",
  services: "services",
  ingresses: "ingresses",
  gateways: "gateways",
  configmaps: "configmaps",
  secrets: "secrets",
  persistentvolumeclaims: "persistentvolumeclaims",
  namespaces: "namespaces",
  serviceaccounts: "serviceaccounts",
  roles: "k8s-roles",
  rolebindings: "k8s-rolebindings",
  networkpolicies: "networkpolicies",
  horizontalpodautoscalers: "hpa",
  poddisruptionbudgets: "poddisruptionbudgets",
};

export function resourceTypeFromK8sPath(
  path: string,
): ResourceType | undefined {
  const segments = path.split("/").filter(Boolean);
  return K8S_PLURAL_RESOURCE_TYPE[segments.at(-2) ?? ""];
}

/**
 * Embeddable YAML view/edit/dry-run panel. Used both as the body of YamlViewDialog
 * and as the YAML tab of ResourceDetail.
 */
export function YamlPanel({
  clusterId,
  k8sPath,
  allowEdit = true,
  editMode: initialEditMode = false,
  forceConflictPermission,
  active = true,
}: YamlPanelProps) {
  const [editMode, setEditMode] = useState(initialEditMode);
  const [editorMode, setEditorMode] = useState<"guided" | "yaml">("yaml");
  const [editedYaml, setEditedYaml] = useState("");
  const [guidedManifest, setGuidedManifest] = useState<KubernetesManifest>({});
  const [guidedValid, setGuidedValid] = useState(false);
  const [preview, setPreview] = useState<YamlApplyPreviewModel | null>(null);
  const resourceType = resourceTypeFromK8sPath(k8sPath);
  const schemaQuery = useResourceSchema(
    clusterId,
    resourceType ?? "deployments",
    active && !!resourceType,
  );

  const {
    data: yaml,
    isLoading,
    error,
    refetch,
  } = useK8sGetYaml(clusterId, k8sPath, active);
  const applyYaml = useK8sApplyYaml();
  const dryRunYaml = useK8sDryRunYaml();

  // Read the current edit mode through a ref inside the sync effect so the
  // effect stays keyed on [yaml] only. A background refetch (refetchOnWindowFocus
  // after an alt-tab, or a k8s.all cache invalidation from any mutation) delivers
  // a new server YAML string; without this guard the effect would overwrite the
  // editor and silently discard the operator's in-progress edits.
  const editModeRef = useRef(editMode);
  editModeRef.current = editMode;

  // Seed the editor from fetched YAML, but never while the operator is editing.
  useEffect(() => {
    if (yaml && !editModeRef.current) {
      setEditedYaml(yaml);
      setPreview(null);
      void import("js-yaml").then((yamlModule) => {
        const parsed = yamlModule.load(yaml);
        if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
          setGuidedManifest(parsed as KubernetesManifest);
        }
      });
    }
  }, [yaml]);

  // Reset state when (re)activated
  useEffect(() => {
    if (active) {
      setEditMode(initialEditMode);
      setEditorMode("yaml");
      setPreview(null);
      refetch();
    }
  }, [active, initialEditMode, refetch]);

  const handleSave = (yamlStr: string) => {
    if (!preview || preview.previewFor !== yamlStr) {
      toastWarning("Run dry run and review the diff before saving.");
      void handleDryRun(yamlStr);
      return;
    }
    applyYaml.mutate(
      { clusterId, path: k8sPath, yaml: yamlStr },
      { onSuccess: finishApply },
    );
  };

  const finishApply = () => {
    refetch();
    setEditMode(false);
    setPreview(null);
  };

  const forceApply = () => {
    applyYaml.mutate(
      { clusterId, path: k8sPath, yaml: editedYaml, force: true },
      { onSuccess: finishApply },
    );
  };

  const retryApply = () => {
    applyYaml.mutate(
      { clusterId, path: k8sPath, yaml: editedYaml },
      { onSuccess: finishApply },
    );
  };

  const manifestYaml = async () => {
    const yamlModule = await import("js-yaml");
    const next = yamlModule.dump(guidedManifest, {
      lineWidth: 100,
      noRefs: true,
      noCompatMode: true,
    });
    setEditedYaml(next);
    return next;
  };

  const changeEditorMode = async (next: "guided" | "yaml") => {
    if (next === editorMode) return;
    const yamlModule = await import("js-yaml");
    if (next === "guided") {
      try {
        const parsed = yamlModule.load(editedYaml || yaml || "");
        if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
          throw new Error("YAML must contain one Kubernetes object.");
        }
        setGuidedManifest(parsed as KubernetesManifest);
      } catch {
        toastWarning("Fix the YAML syntax before switching to guided editing.");
        return;
      }
    } else {
      await manifestYaml();
    }
    setPreview(null);
    setEditorMode(next);
  };

  const dryRunGuided = async () => {
    const next = await manifestYaml();
    await handleDryRun(next);
  };

  const saveGuided = async () => {
    const next = await manifestYaml();
    handleSave(next);
  };

  const applyFailure = applyYaml.isError
    ? classifyResourceApplyFailure(applyYaml.error)
    : null;

  const handleDryRun = async (yamlStr: string) => {
    setPreview(null);
    try {
      const [yamlModule, latestYaml, normalizedObject] = await Promise.all([
        import("js-yaml"),
        apiClient.k8sGetYaml(clusterId, k8sPath),
        dryRunYaml.mutateAsync({ clusterId, path: k8sPath, yaml: yamlStr }),
      ]);
      const normalizedYaml = yamlModule.dump(normalizedObject, {
        lineWidth: -1,
        noRefs: true,
      });
      const warnings: string[] = [];
      if (yaml && !yamlTextMatches(latestYaml, yaml)) {
        warnings.push(
          "The live object changed after this editor opened. Review the diff carefully before applying.",
        );
      }
      setPreview({
        previewFor: yamlStr,
        changed: !yamlTextMatches(latestYaml, normalizedYaml),
        diff: buildYamlDiff(latestYaml, normalizedYaml),
        warnings,
      });
    } catch {
      setPreview(null);
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      {allowEdit && (
        <div className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-b border-border px-3 py-2">
          <p className="text-xs text-muted-foreground">
            Managed fields are omitted from YAML. Normal apply preserves other
            field managers and reports ownership conflicts.
          </p>
          <div className="flex items-center bg-muted rounded p-0.5">
            <button
              onClick={() => setEditMode(false)}
              className={cn(
                "inline-flex items-center gap-1 px-2 py-1 rounded text-xs font-medium transition-colors",
                !editMode
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              <Eye className="h-3 w-3" /> View
            </button>
            <button
              onClick={() => setEditMode(true)}
              className={cn(
                "inline-flex items-center gap-1 px-2 py-1 rounded text-xs font-medium transition-colors",
                editMode
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              <Pencil className="h-3 w-3" /> Edit
            </button>
          </div>
        </div>
      )}
      {editMode && resourceType && (
        <div
          className="flex shrink-0 items-center gap-1 border-b border-border px-3 py-1.5"
          role="tablist"
          aria-label="Resource edit mode"
        >
          {(["guided", "yaml"] as const).map((item) => (
            <button
              key={item}
              type="button"
              role="tab"
              aria-selected={editorMode === item}
              onClick={() => void changeEditorMode(item)}
              className={cn(
                "rounded px-2.5 py-1 text-xs font-medium capitalize",
                editorMode === item
                  ? "bg-muted text-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {item}
            </button>
          ))}
          <span className="ml-auto text-xs text-muted-foreground">
            {schemaQuery.isLoading
              ? "Loading live schema…"
              : schemaQuery.data?.schemaAvailable
                ? `Schema: ${schemaQuery.data.schemaName || schemaQuery.data.resource.kind}`
                : "Template validation active"}
          </span>
        </div>
      )}
      <div className="flex-1 min-h-0">
        {isLoading ? (
          <div className="flex items-center justify-center h-full">
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
          </div>
        ) : error ? (
          <ErrorState
            title="Failed to load YAML"
            description={(error as Error).message}
            onRetry={() => void refetch()}
            className="h-full py-12"
          />
        ) : (
          <div className="flex h-full flex-col">
            <div className="min-h-0 flex-1">
              {editMode && editorMode === "guided" && resourceType ? (
                <div className="flex h-full min-h-0 flex-col">
                  <div className="min-h-0 flex-1 overflow-y-auto">
                    <GuidedResourceForm
                      value={guidedManifest}
                      onChange={(next) => {
                        setGuidedManifest(next);
                        setPreview(null);
                      }}
                      schema={schemaQuery.data?.schema ?? {}}
                      definitions={schemaQuery.data?.definitions ?? {}}
                      onValidationChange={setGuidedValid}
                      identityReadOnly
                    />
                  </div>
                  <div className="flex shrink-0 justify-end gap-2 border-t border-border px-4 py-2">
                    <button
                      type="button"
                      onClick={() => void dryRunGuided()}
                      disabled={!guidedValid || dryRunYaml.isPending}
                      className="rounded-md border border-border px-3 py-1.5 text-xs font-medium text-foreground hover:bg-accent disabled:opacity-50"
                    >
                      {dryRunYaml.isPending
                        ? "Previewing…"
                        : "Dry run & preview"}
                    </button>
                    <button
                      type="button"
                      onClick={() => void saveGuided()}
                      disabled={
                        !guidedValid ||
                        applyYaml.isPending ||
                        !preview ||
                        preview.previewFor !== editedYaml
                      }
                      className="rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                    >
                      {applyYaml.isPending ? "Applying…" : "Apply"}
                    </button>
                  </div>
                </div>
              ) : (
                <YamlEditor
                  value={editMode ? editedYaml : yaml || ""}
                  onChange={
                    editMode
                      ? (next) => {
                          setEditedYaml(next);
                          if (preview?.previewFor !== next) setPreview(null);
                        }
                      : undefined
                  }
                  readOnly={!editMode}
                  onDryRun={editMode ? handleDryRun : undefined}
                  onSave={editMode ? handleSave : undefined}
                  saving={applyYaml.isPending}
                  dryRunning={dryRunYaml.isPending}
                  saveBlocked={
                    editMode && (!preview || preview.previewFor !== editedYaml)
                  }
                  className="h-full"
                />
              )}
            </div>
            {editMode && preview && <YamlDiffPreview preview={preview} />}
            {editMode && applyFailure === "conflict" ? (
              <div
                role="alert"
                className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-status-warning/30 bg-status-warning/10 px-4 py-3"
              >
                <div className="flex items-start gap-2 text-xs text-status-warning">
                  <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
                  <span>
                    Another field manager owns part of this object. Taking
                    ownership requires the resource <code>manage</code>
                    permission and is audited.
                    {!forceConflictPermission?.allowed && (
                      <span className="mt-1 block text-muted-foreground">
                        {forceConflictPermission?.disabledReason ??
                          forceConflictPermission?.reason ??
                          "You do not have permission to take ownership. Ask a cluster administrator to resolve the conflict."}
                      </span>
                    )}
                  </span>
                  </div>
                {forceConflictPermission?.allowed && (
                  <button
                    type="button"
                    onClick={forceApply}
                    disabled={applyYaml.isPending}
                    className="rounded-md border border-status-warning/40 px-3 py-1.5 text-xs font-medium text-status-warning hover:bg-status-warning/10 disabled:opacity-50"
                  >
                    Take ownership and apply
                  </button>
                )}
              </div>
            ) : editMode && applyFailure === "forbidden" ? (
              <div
                role="alert"
                className="shrink-0 border-t border-status-error/30 bg-status-error/10 px-4 py-3 text-xs text-status-error"
              >
                Permission required. Your edits are preserved, but this account
                cannot apply the resource. Request the required cluster access
                before trying again.
              </div>
            ) : editMode && applyFailure === "retryable" ? (
              <div
                role="alert"
                className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-status-warning/30 bg-status-warning/10 px-4 py-3"
              >
                <span className="text-xs text-status-warning">
                  The resource could not be applied because the cluster or API
                  was temporarily unavailable. Your edits and reviewed preview
                  are preserved.
                </span>
                <button
                  type="button"
                  onClick={retryApply}
                  disabled={applyYaml.isPending}
                  className="rounded-md border border-status-warning/40 px-3 py-1.5 text-xs font-medium text-status-warning hover:bg-status-warning/10 disabled:opacity-50"
                >
                  Retry apply
                </button>
              </div>
            ) : editMode && applyFailure === "terminal" ? (
              <div
                role="alert"
                className="shrink-0 border-t border-status-error/30 bg-status-error/10 px-4 py-3 text-xs text-status-error"
              >
                Apply failed. Your edits are preserved. Review the API error,
                update the resource, and run the dry-run preview again.
              </div>
            ) : null}
          </div>
        )}
      </div>
    </div>
  );
}
