import { usePipelinePageParam } from "./-pipeline-page-param";
import { Select } from "@/components/ui/select";
import { toastApiError } from "@/lib/toast";
import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getLoggingPipeline, updateLoggingPipeline } from "@/lib/api/logging";
import { queryKeys } from "@/lib/query-keys";
import { useLoggingOutputs } from "@/lib/hooks/logging";
import { useClusterNamespaces } from "@/lib/hooks/clusters";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { QueryStates } from "@/components/ui/query-states";
import { PageHeader, PageShell } from "@/components/ui/page";
import { FormShell } from "@/components/ui/form-shell";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { PipelineNamespaces } from "./-pipeline-namespaces";
import { PipelineOutputs } from "./-pipeline-outputs";
import type { LoggingPipeline } from "@/types";

export function pipelineUpdate(
  original: LoggingPipeline,
  changes: Partial<LoggingPipeline>,
): LoggingPipeline {
  return {
    ...original,
    ...changes,
    rawFilters: changes.rawFilters ?? original.rawFilters ?? original.filters,
    labels: original.labels,
  };
}
export function PipelinePage({ edit = false }: { edit?: boolean }) {
  const { pipelineId } = useParams({ strict: false }) as { pipelineId: string };
  const query = useQuery({
    queryKey: queryKeys.logging.pipeline(pipelineId),
    queryFn: ({ signal }) => getLoggingPipeline(pipelineId, signal),
    throwOnError: false,
  });
  return (
    <PageShell>
      <QueryStates
        query={query}
        permission="logging:read"
        errorTitle="Pipeline unavailable"
      >
        {(pipeline) => (
          <PipelineContent
            key={`${pipeline.id}/${edit}`}
            pipeline={pipeline}
            edit={edit}
          />
        )}
      </QueryStates>
    </PageShell>
  );
}
function PipelineContent({
  pipeline,
  edit,
}: {
  pipeline: LoggingPipeline;
  edit: boolean;
}) {
  const [original] = useState(pipeline);
  const [draft, setDraft] = useState(pipeline);
  const stale = original.updatedAt !== pipeline.updatedAt;
  const navigate = useNavigate();
  const { page } = usePipelinePageParam();
  const client = useQueryClient();
  const outputs = useLoggingOutputs(pipeline.clusterId);
  const namespaces = useClusterNamespaces(pipeline.clusterId ?? "");
  const write = usePermissionDecision(
    "logging",
    "update",
    pipeline.clusterId
      ? { type: "cluster", id: pipeline.clusterId }
      : { type: "global" },
  );
  const mutation = useMutation({
    onError: (error) => toastApiError("Pipeline save failed", error),
    mutationFn: () =>
      updateLoggingPipeline(pipeline.id, pipelineUpdate(original, draft)),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: queryKeys.logging.all });
    },
  });
  useEffect(() => {
    if (mutation.isSuccess)
      void navigate({
        to: `/dashboard/logging/pipelines/${pipeline.id}`,
        search: { pipelinePage: page },
      });
  }, [mutation.isSuccess, navigate, pipeline.id, page]);
  const missing = draft.outputIds.filter(
    (id) =>
      !outputs.data?.some(
        (output) => output.id === id && output.clusterId === pipeline.clusterId,
      ),
  );
  const dirty =
    JSON.stringify(draft) !== JSON.stringify(original) && !mutation.isSuccess;
  const toggle = (key: "namespaces" | "outputIds", value: string) =>
    setDraft((current) => ({
      ...current,
      [key]: current[key].includes(value)
        ? current[key].filter((item) => item !== value)
        : [...current[key], value],
    }));
  return (
    <>
      <PageHeader
        title={edit ? `Edit ${pipeline.name}` : pipeline.name}
        description={`Cluster ${pipeline.clusterId ?? "unavailable"}`}
        actions={
          pipeline.clusterId ? (
            <Link
              to="/dashboard/clusters/$id/logging"
              params={{ id: pipeline.clusterId }}
              search={{ view: "collection", pipelinePage: page }}
            >
              Back to pipelines
            </Link>
          ) : (
            <Link to="/dashboard/logging">Back to logging</Link>
          )
        }
      />
      <FormShell
        isDirty={edit && dirty}
        onSubmit={(event) => {
          event.preventDefault();
          if (write.allowed && !missing.length && !stale) mutation.mutate();
        }}
        className="space-y-4"
      >
        {edit && stale && (
          <p role="alert">
            This pipeline changed after editing started. Your draft is retained.
            Reopen the editor to review the latest version before saving.
          </p>
        )}
        {edit ? (
          <label className="block text-sm">
            Name
            <Input
              value={draft.name}
              onChange={(event) =>
                setDraft({ ...draft, name: event.target.value })
              }
            />
          </label>
        ) : null}
        {edit ? (
          <>
            <PipelineNamespaces
              query={namespaces}
              selected={draft.namespaces}
              onToggle={(value) => toggle("namespaces", value)}
            />
            <PipelineOutputs
              query={outputs}
              clusterId={pipeline.clusterId ?? ""}
              selected={draft.outputIds}
              onToggle={(value) => toggle("outputIds", value)}
            />
          </>
        ) : (
          <dl className="space-y-3 text-sm">
            <dt>Namespaces</dt>
            <dd>
              {pipeline.namespaces.join(", ") || "All authorized namespaces"}
            </dd>
            <dt>Destinations</dt>
            <dd>
              {pipeline.outputIds.map((id, index) => (
                <p key={id}>
                  {outputs.data?.find((output) => output.id === id)?.name ??
                    pipeline.outputNames[index] ??
                    `Unavailable destination (${id})`}
                </p>
              ))}
            </dd>
          </dl>
        )}
        {missing.length > 0 && (
          <p role="alert">
            Some destinations are unavailable or have not loaded:{" "}
            {missing.join(", ")}. They remain preserved; load them before
            saving.
          </p>
        )}
        <PipelineFilters
          value={draft.rawFilters ?? draft.filters}
          readOnly={!edit}
          onChange={(rawFilters) => setDraft({ ...draft, rawFilters })}
        />
        <p className="text-sm">
          Labels and unsupported filter variants are preserved unchanged.
        </p>
        <details open>
          <summary>Filters and labels</summary>
          <pre className="overflow-auto whitespace-pre-wrap break-all text-xs">
            {JSON.stringify(
              {
                filters: pipeline.rawFilters ?? pipeline.filters,
                labels: pipeline.labels ?? {},
              },
              null,
              2,
            )}
          </pre>
        </details>
        <div className="flex items-center gap-2 text-sm">
          Enabled
          <Switch
            aria-label="Pipeline enabled"
            checked={edit ? draft.enabled : pipeline.enabled}
            disabled={!edit}
            onCheckedChange={(enabled) => setDraft({ ...draft, enabled })}
          />
        </div>
        {mutation.isError && (
          <p role="alert" className="text-destructive">
            Saving failed. Your edits are retained. {mutation.error.message}
          </p>
        )}
        {edit ? (
          <ActionButton
            type="submit"
            intent="primary"
            loading={mutation.isPending}
            disabled={
              stale ||
              !write.allowed ||
              !draft.name.trim() ||
              namespaces.isError ||
              !namespaces.data ||
              outputs.isError ||
              missing.length > 0
            }
          >
            Save pipeline
          </ActionButton>
        ) : (
          write.allowed && (
            <Link
              to={String(`/dashboard/logging/pipelines/${pipeline.id}/edit`)}
              search={{ pipelinePage: page }}
            >
              Edit pipeline
            </Link>
          )
        )}
      </FormShell>
    </>
  );
}

function PipelineFilters({
  value,
  readOnly,
  onChange,
}: {
  value: unknown;
  readOnly: boolean;
  onChange: (value: unknown) => void;
}) {
  if (!Array.isArray(value))
    return (
      <p className="text-sm">
        This pipeline uses a custom filter configuration, preserved unchanged.
      </p>
    );
  return (
    <div className="space-y-3">
      {value.map((filter, index) => {
        if (
          !filter ||
          typeof filter !== "object" ||
          !["include", "exclude"].includes(filter.type) ||
          typeof filter.field !== "string" ||
          typeof filter.pattern !== "string"
        )
          return (
            <p key={index} className="text-sm">
              Custom filter {index + 1} is preserved unchanged.
            </p>
          );
        const update = (key: string, next: string) =>
          onChange(
            value.map((item, position) =>
              position === index ? { ...item, [key]: next } : item,
            ),
          );
        return (
          <fieldset
            key={index}
            className="grid gap-2 sm:grid-cols-3"
            disabled={readOnly}
          >
            <legend>Filter {index + 1}</legend>
            <label>
              Type
              <Select
                value={filter.type}
                onChange={(event) => update("type", event.target.value)}
              >
                <option value="include">Include</option>
                <option value="exclude">Exclude</option>
              </Select>
            </label>
            <label>
              Field
              <Input
                value={filter.field}
                onChange={(event) => update("field", event.target.value)}
              />
            </label>
            <label>
              Pattern
              <Input
                value={filter.pattern}
                onChange={(event) => update("pattern", event.target.value)}
              />
            </label>
          </fieldset>
        );
      })}
    </div>
  );
}
