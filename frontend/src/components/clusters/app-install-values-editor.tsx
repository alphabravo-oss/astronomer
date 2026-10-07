import { HelmValuesForm } from "@/components/catalog/helm-values-form";
import { BareButton } from "@/components/form/bare-button";
import { QueryStates } from "@/components/ui/query-states";
import type {
  getChartDefaultValues,
  previewCatalogApplication,
} from "@/lib/api/cluster-apps";
import {
  dumpHelmValuesYAML,
  parseHelmValuesYAML,
  type HelmValuesObject,
  type HelmValuesSchemaNode,
} from "@/lib/helm-values-schema";
import { cn } from "@/lib/utils";
import type { UseQueryResult } from "@tanstack/react-query";
import { Braces, FileCode2, GitCompare } from "lucide-react";
import { CatalogApplicationReview } from "./catalog-application-review";
type EditorMode = "form" | "yaml" | "review";
export function AppInstallValuesEditor({
  defaultValues,
  isUpgrade,
  valuesSchema,
  editorMode,
  switchEditorMode,
  preview,
  chartName,
  version,
  namespace,
  releaseName,
  schemaValues,
  valuesYaml,
  onValuesChange,
  setYamlError,
  yamlError,
}: {
  defaultValues: UseQueryResult<
    Awaited<ReturnType<typeof getChartDefaultValues>>
  >;
  isUpgrade: boolean;
  valuesSchema: HelmValuesSchemaNode | null;
  editorMode: EditorMode;
  switchEditorMode: (mode: EditorMode) => void;
  preview: UseQueryResult<
    Awaited<ReturnType<typeof previewCatalogApplication>>
  >;
  chartName: string;
  version: string;
  namespace: string;
  releaseName: string;
  schemaValues: HelmValuesObject | null;
  valuesYaml: string;
  onValuesChange: (value: string) => void;
  setYamlError: (value: string | null) => void;
  yamlError: string | null;
}) {
  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <label className="text-xs font-medium text-muted-foreground">
          Values
          {defaultValues.isLoading && (
            <span className="ml-2 text-2xs" aria-busy="true">
              hydrating defaults…
            </span>
          )}
        </label>
        {defaultValues.isError && (
          <QueryStates
            query={defaultValues}
            permission="catalog:read"
            errorTitle="Could not load chart defaults"
          >
            {null}
          </QueryStates>
        )}
        {isUpgrade && !defaultValues.isError && defaultValues.data && (
          <BareButton
            tooltip="Replace with the upstream chart's default values for the selected version"
            onClick={() => onValuesChange(defaultValues.data!.defaultValues)}
            className="text-11 text-muted-foreground hover:text-foreground underline inline-block font-normal"
          >
            Reset to chart defaults
          </BareButton>
        )}
      </div>
      {valuesSchema && (
        <div className="inline-flex rounded-md border border-border bg-muted/30 p-1">
          <BareButton
            type="button"
            aria-pressed={editorMode === "form"}
            onClick={() => switchEditorMode("form")}
            className={cn(
              "inline-flex items-center gap-1 rounded-sm px-2.5 py-1 text-xs font-medium transition-colors",
              editorMode === "form"
                ? "bg-background text-foreground shadow-xs"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            <Braces className="h-3.5 w-3.5" /> Form
          </BareButton>
          <BareButton
            type="button"
            aria-pressed={editorMode === "yaml"}
            onClick={() => switchEditorMode("yaml")}
            className={cn(
              "inline-flex items-center gap-1 rounded-sm px-2.5 py-1 text-xs font-medium transition-colors",
              editorMode === "yaml"
                ? "bg-background text-foreground shadow-xs"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            <FileCode2 className="h-3.5 w-3.5" /> YAML
          </BareButton>
        </div>
      )}
      <BareButton
        type="button"
        aria-pressed={editorMode === "review"}
        onClick={() => switchEditorMode("review")}
        className={cn(
          "inline-flex items-center gap-1 rounded-sm border border-border px-2.5 py-1 text-xs font-medium transition-colors",
          editorMode === "review"
            ? "bg-background text-foreground shadow-xs"
            : "text-muted-foreground hover:text-foreground",
        )}
      >
        <GitCompare className="h-3.5 w-3.5" /> Review
      </BareButton>
      {editorMode === "review" ? (
        <CatalogApplicationReview
          preview={preview.data}
          loading={preview.isLoading}
          error={preview.error}
          chartName={chartName}
          version={version}
          namespace={namespace}
          releaseName={releaseName}
        />
      ) : valuesSchema && editorMode === "form" && schemaValues ? (
        <div className="rounded-lg border border-border bg-muted/20 p-4">
          <HelmValuesForm
            schema={valuesSchema}
            value={schemaValues}
            onChange={(next) => {
              onValuesChange(dumpHelmValuesYAML(next));
              setYamlError(null);
            }}
          />
        </div>
      ) : (
        <textarea
          aria-label="Values (YAML)"
          value={valuesYaml}
          onChange={(e) => {
            onValuesChange(e.target.value);
            setYamlError(null);
          }}
          onBlur={() => {
            if (valuesYaml.trim() && parseHelmValuesYAML(valuesYaml) == null)
              setYamlError("Values must be valid YAML containing an object.");
          }}
          rows={16}
          spellCheck={false}
          className="w-full px-3 py-2 rounded-md border border-border bg-background text-xs font-mono focus:outline-hidden focus:ring-1 focus:ring-ring resize-y"
          placeholder="# values.yaml — overrides applied on top of chart defaults"
        />
      )}
      {yamlError && (
        <p role="alert" className="text-xs text-status-error">
          {yamlError}
        </p>
      )}
      <p className="text-11 text-muted-foreground">
        Use chart fields that reference an existing Kubernetes Secret for
        credentials. Verified Apps reject inline Vault placeholders so the
        durable delivery bundle never persists a hidden secret template.
      </p>
    </div>
  );
}
