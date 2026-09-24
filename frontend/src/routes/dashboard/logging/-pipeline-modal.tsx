import { useAppForm, useStore } from "@/lib/form";
import {
  useCreateLoggingPipeline,
  useLoggingOutputs,
} from "@/lib/hooks/logging";
import { useClusterNamespaces } from "@/lib/hooks/clusters";
import { RemoteClusterPicker } from "@/components/clusters/remote-cluster-picker";
import { PipelineOutputs } from "./-pipeline-outputs";
import { outputsForCluster, validOutputSelection } from "./-output-scope";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import { PipelineNamespaces } from "./-pipeline-namespaces";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Plus, X } from "lucide-react";
import { toastError } from "@/lib/toast";

export function CreatePipelineModal({
  onClose,
  clusterId,
}: {
  onClose: () => void;
  clusterId?: string;
}) {
  const createPipeline = useCreateLoggingPipeline();

  const pipelineForm = useAppForm({
    defaultValues: {
      name: "",
      description: "",
      clusterId: clusterId || "",
      namespaces: [] as string[],
      outputIds: [] as string[],
      labelKey: "",
      labelValue: "",
      labels: {} as Record<string, string>,
      enabled: true,
    },
    validators: {
      // Old pre-submit checks, ported 1:1 (same messages, same order).
      onSubmit: ({ value }) =>
        !value.name
          ? "Name is required"
          : !value.clusterId
            ? "Select a cluster"
            : namespacesQuery.isError || !namespacesQuery.data
              ? "Load the cluster namespaces before creating a pipeline"
              : !validOutputSelection(value.outputIds, outputList)
                ? "Select available outputs belonging to this cluster"
                : undefined,
    },
    // Same UX as before: the failed check surfaces as a toast, not inline.
    onSubmitInvalid: ({ formApi }) => {
      const err = formApi.state.errors.find((e) => typeof e === "string");
      if (err) toastError(err);
    },
    onSubmit: async ({ value }) => {
      const filters = Object.entries(value.labels).map(([field, pattern]) => ({
        type: "include" as const,
        field,
        pattern,
      }));

      try {
        await createPipeline.mutateAsync({
          name: value.name,
          description: value.description || undefined,
          clusterId: value.clusterId || undefined,
          namespaces: value.namespaces,
          outputIds: value.outputIds,
          filters,
          enabled: value.enabled,
        });
        onClose();
      } catch {
        // Error handled by mutation
      }
    },
  });

  // Chips / KV rows render off the whole value object — same re-render
  // behavior as the previous useState form.
  const form = useStore(pipelineForm.store, (s) => s.values);
  const outputsQuery = useLoggingOutputs(form.clusterId || undefined, {
    enabled: !!form.clusterId,
  });
  const outputList = outputsForCluster(
    outputsQuery.isError ? undefined : outputsQuery.data,
    form.clusterId,
  );

  const namespacesQuery = useClusterNamespaces(form.clusterId);

  const toggleNamespace = (ns: string) => {
    pipelineForm.setFieldValue(
      "namespaces",
      form.namespaces.includes(ns)
        ? form.namespaces.filter((n) => n !== ns)
        : [...form.namespaces, ns],
    );
  };

  const toggleOutput = (id: string) => {
    pipelineForm.setFieldValue(
      "outputIds",
      form.outputIds.includes(id)
        ? form.outputIds.filter((o) => o !== id)
        : [...form.outputIds, id],
    );
  };

  const addLabel = () => {
    if (form.labelKey && form.labelValue) {
      pipelineForm.setFieldValue("labels", {
        ...form.labels,
        [form.labelKey]: form.labelValue,
      });
      pipelineForm.setFieldValue("labelKey", "");
      pipelineForm.setFieldValue("labelValue", "");
    }
  };

  const removeLabel = (key: string) => {
    const labels = { ...form.labels };
    delete labels[key];
    pipelineForm.setFieldValue("labels", labels);
  };

  return (
    <ModalShell
      title="Create Logging Pipeline"
      onClose={onClose}
      size="lg"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void pipelineForm.handleSubmit()}
            disabled={
              !form.name ||
              !form.clusterId ||
              namespacesQuery.isError ||
              !namespacesQuery.data ||
              !validOutputSelection(form.outputIds, outputList)
            }
            loading={createPipeline.isPending}
          >
            Create Pipeline
          </ActionButton>
        </>
      }
      footerClassName="flex items-center justify-end gap-2"
    >
      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-1.5">
          <label
            htmlFor="logging-pipeline-name"
            className="text-sm font-medium text-foreground"
          >
            Name
          </label>
          <pipelineForm.Field name="name">
            {(field) => (
              <Input
                id="logging-pipeline-name"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="Production Log Pipeline"
              />
            )}
          </pipelineForm.Field>
        </div>
        {!clusterId && (
          <div className="space-y-1.5">
            <label
              htmlFor="logging-pipeline-cluster"
              className="text-sm font-medium text-foreground"
            >
              Cluster
            </label>
            <pipelineForm.Field name="clusterId">
              {(field) => (
                <RemoteClusterPicker
                  id="logging-pipeline-cluster"
                  value={field.state.value}
                  onChange={(value) => {
                    field.handleChange(value);
                    pipelineForm.setFieldValue("namespaces", []);
                    pipelineForm.setFieldValue("outputIds", []);
                  }}
                  onBlur={field.handleBlur}
                />
              )}
            </pipelineForm.Field>
          </div>
        )}
      </div>

      <div className="space-y-1.5">
        <label
          htmlFor="logging-pipeline-description"
          className="text-sm font-medium text-foreground"
        >
          Description
        </label>
        <pipelineForm.Field name="description">
          {(field) => (
            <Textarea
              id="logging-pipeline-description"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="Describe this pipeline's purpose"
              className="min-h-[80px]"
            />
          )}
        </pipelineForm.Field>
      </div>

      {form.clusterId && (
        <PipelineNamespaces
          query={namespacesQuery}
          selected={form.namespaces}
          onToggle={toggleNamespace}
        />
      )}

      <div className="space-y-2">
        <p className="text-sm font-medium text-foreground">Label Selectors</p>
        <div className="flex gap-2">
          <Input
            aria-label="Label key"
            value={form.labelKey}
            onChange={(e) =>
              pipelineForm.setFieldValue("labelKey", e.target.value)
            }
            placeholder="Label key"
            className="h-8 font-mono text-xs"
          />
          <Input
            aria-label="Label value"
            value={form.labelValue}
            onChange={(e) =>
              pipelineForm.setFieldValue("labelValue", e.target.value)
            }
            placeholder="Value"
            className="h-8 font-mono text-xs"
          />
          <ActionButton
            size="icon"
            onClick={addLabel}
            disabled={!form.labelKey || !form.labelValue}
            icon={<Plus className="h-3.5 w-3.5" />}
            title="Add label"
          />
        </div>
        {Object.entries(form.labels).length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {Object.entries(form.labels).map(([k, v]) => (
              <span
                key={k}
                className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-sm bg-muted text-muted-foreground font-mono"
              >
                {k}={v}
                <button
                  type="button"
                  onClick={() => removeLabel(k)}
                  className="hover:text-foreground"
                >
                  <X className="h-3 w-3" />
                </button>
              </span>
            ))}
          </div>
        )}
      </div>

      <PipelineOutputs
        query={outputsQuery}
        clusterId={form.clusterId}
        selected={form.outputIds}
        onToggle={toggleOutput}
      />

      <label className="flex items-center gap-2 cursor-pointer">
        <pipelineForm.Field name="enabled">
          {(field) => (
            <Input
              type="checkbox"
              checked={field.state.value}
              onChange={(e) => field.handleChange(e.target.checked)}
              onBlur={field.handleBlur}
              className="rounded-sm border-border text-primary focus:ring-ring"
            />
          )}
        </pipelineForm.Field>
        <span className="text-sm text-foreground">Enabled</span>
      </label>
    </ModalShell>
  );
}
