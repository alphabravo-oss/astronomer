import { LoadingPanel } from "@/components/charlie/loading-panel";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ShieldCheck } from "lucide-react";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status-badge";
import {
  getCharlieFinding,
  listCharlieFindings,
  transitionCharlieFinding,
} from "@/lib/api/charlie";
import { cn } from "@/lib/utils";
import { queryKeys } from "@/lib/query-keys";
import { useAuthStore } from "@/lib/store";
import { can } from "@/lib/permissions";
import { ActionButton } from "@/components/ui/action-button";
import { FindingDetail } from "./-finding-detail";
import { FilterField, QueryFailure, filterEmptyAction } from "./-shared";

export function Findings({
  selected,
  onSelect,
  params,
  set,
}: {
  selected: string | null;
  onSelect: (id: string) => void;
  params: URLSearchParams;
  set: (updates: Record<string, string | undefined>) => void;
}) {
  const qc = useQueryClient();
  const user = useAuthStore((state) => state.user);
  const canTriage = can(user, "charlie", "update");
  const q = useQuery({
    queryKey: queryKeys.charlie.findings,
    queryFn: listCharlieFindings,
    retry: false,
  });
  const d = useQuery({
    queryKey: queryKeys.charlie.finding(selected),
    queryFn: () => getCharlieFinding(selected!),
    enabled: !!selected,
    retry: false,
  });
  const action = useMutation({
    mutationFn: ({
      id,
      a,
    }: {
      id: string;
      a:
        | "acknowledge"
        | "start_remediation"
        | "request_verification"
        | "dismiss"
        | "resolve";
    }) => transitionCharlieFinding(id, a),
    onSuccess: (_data, variables) => {
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.findings });
      void qc.invalidateQueries({
        queryKey: queryKeys.charlie.finding(variables.id),
      });
    },
  });
  if (q.isError) return <QueryFailure label="Findings" query={q} />;
  const status = params.get("status") ?? "";
  const severity = params.get("severity") ?? "";
  const source = params.get("source") ?? "";
  const resource = params.get("resource") ?? "";
  const block = params.get("block") ?? "";
  const from = params.get("from") ?? "";
  const to = params.get("to") ?? "";
  const rows = q.data?.filter(
    (finding) =>
      (!status || finding.state === status) &&
      (!severity || finding.severity === severity) &&
      (!source || finding.source === source) &&
      (!resource ||
        `${finding.affectedResource.type}:${finding.affectedResource.id}`
          .toLowerCase()
          .includes(resource.toLowerCase())) &&
      (!block || finding.reasonNoAction === block) &&
      (!from || !finding.updatedAt || finding.updatedAt >= from) &&
      (!to || !finding.updatedAt || finding.updatedAt.slice(0, 10) <= to),
  );
  return (
    <div className="space-y-4">
      <div
        className="flex flex-wrap gap-3 rounded-lg border p-3"
        aria-label="Finding filters"
      >
        <FilterField
          label="Finding status"
          value={status}
          options={["open", "acknowledged", "dismissed", "resolved", "expired"]}
          onChange={(value) => set({ status: value || undefined })}
        />
        <FilterField
          label="Finding severity"
          value={severity}
          options={["medium", "warning", "high", "critical"]}
          onChange={(value) => set({ severity: value || undefined })}
        />
        <FilterField
          label="Finding source"
          value={source}
          onChange={(value) => set({ source: value || undefined })}
        />
        <FilterField
          label="Affected resource"
          value={resource}
          onChange={(value) => set({ resource: value || undefined })}
        />
        <FilterField
          label="Execution block"
          value={block}
          onChange={(value) => set({ block: value || undefined })}
        />
        <FilterField
          type="date"
          label="Finding from date"
          value={from}
          onChange={(value) => set({ from: value || undefined })}
        />
        <FilterField
          type="date"
          label="Finding to date"
          value={to}
          onChange={(value) => set({ to: value || undefined })}
        />
      </div>
      <div className="grid gap-4 md:grid-cols-[20rem_1fr]">
        <div className="space-y-2">
          {q.isLoading ? (
            <LoadingPanel title="Loading findings" />
          ) : rows?.length ? (
            rows.map((f) => (
              <ActionButton
                intent="bare"
                size="none"
                key={f.id}
                onClick={() => onSelect(f.id)}
                className={cn(
                  "block w-full whitespace-normal rounded-lg border p-3 text-left font-normal",
                  selected === f.id && "border-primary",
                )}
              >
                <div className="flex justify-between">
                  <b className="text-sm">{f.title}</b>
                  <StatusBadge
                    status={f.severity === "high" ? "error" : f.severity}
                    label={f.severity}
                  />
                </div>
                <p className="text-xs text-foreground/70">
                  {f.affectedResource.type}: {f.affectedResource.id}
                </p>
                <p className="text-xs text-foreground/70">
                  {f.repeatCount ?? 1} occurrence
                  {(f.repeatCount ?? 1) === 1 ? "" : "s"}
                </p>
              </ActionButton>
            ))
          ) : (
            <EmptyState
              icon={ShieldCheck}
              title="No findings match"
              description="Try changing the finding filters."
              {...filterEmptyAction(
                Boolean(
                  status ||
                  severity ||
                  source ||
                  resource ||
                  block ||
                  from ||
                  to,
                ),
                () =>
                  set({
                    status: undefined,
                    severity: undefined,
                    source: undefined,
                    resource: undefined,
                    block: undefined,
                    from: undefined,
                    to: undefined,
                  }),
              )}
            />
          )}
        </div>
        <div className="rounded-lg border p-4">
          {!selected ? (
            <EmptyState
              icon={ShieldCheck}
              title="Select a finding"
              description="Evidence is fetched from Charlie only when selected."
              terminal // action: the finding list to the left
            />
          ) : d.isLoading ? (
            <LoadingPanel title="Loading authorized finding detail" />
          ) : d.isError ? (
            <QueryFailure label="Finding" query={d} />
          ) : (
            d.data && (
              <FindingDetail
                finding={d.data}
                canTriage={canTriage}
                action={action}
              />
            )
          )}
        </div>
      </div>
    </div>
  );
}
