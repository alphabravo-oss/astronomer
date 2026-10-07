import { LoadingPanel } from "@/components/charlie/loading-panel";
import { useQuery } from "@tanstack/react-query";
import { Clock } from "lucide-react";
import { Link as RouterLink } from "@tanstack/react-router";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status-badge";
import { listCharlieFindings, listCharlieSessions } from "@/lib/api/charlie";
import { cn } from "@/lib/utils";
import { queryKeys } from "@/lib/query-keys";
import { ActionButton } from "@/components/ui/action-button";
import {
  FilterField,
  QueryFailure,
  filterEmptyAction,
  resourceHref,
} from "./-shared";

export function Investigations({
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
  const q = useQuery({
    queryKey: queryKeys.charlie.sessions,
    queryFn: listCharlieSessions,
    retry: false,
  });
  const findings = useQuery({
    queryKey: queryKeys.charlie.findings,
    queryFn: listCharlieFindings,
    retry: false,
  });
  if (q.isError) return <QueryFailure label="Investigations" query={q} />;
  if (findings.isError)
    return <QueryFailure label="Investigation findings" query={findings} />;
  const status = params.get("status") ?? "";
  const severity = params.get("severity") ?? "";
  const cluster = params.get("cluster") ?? "";
  const source = params.get("source") ?? "";
  const from = params.get("from") ?? "";
  const to = params.get("to") ?? "";
  // Investigations track: system/event sessions only — never private user chats.
  const rows = (
    q.data?.filter(
      (s) => s.visibility === "incident" && s.source === "event",
    ) ?? []
  )
    .map((session) => ({
      session,
      finding: findings.data?.find(
        (finding) => finding.sessionId === session.id,
      ),
    }))
    .filter(
      ({ session, finding }) =>
        (!status || session.state === status) &&
        (!severity || finding?.severity === severity) &&
        (!cluster ||
          (finding?.affectedResource.type === "agent_connection_record" &&
            finding.affectedResource.id
              .toLowerCase()
              .includes(cluster.toLowerCase()))) &&
        (!source || session.source === source || finding?.source === source) &&
        (!from || !session.createdAt || session.createdAt >= from) &&
        (!to || !session.createdAt || session.createdAt.slice(0, 10) <= to),
    );
  const detail = rows.find(({ session }) => session.id === selected);
  return (
    <div className="space-y-4">
      <div
        className="flex flex-wrap gap-3 rounded-lg border p-3"
        aria-label="Investigation filters"
      >
        <FilterField
          label="Investigation status"
          value={status}
          options={[
            "active",
            "waiting_approval",
            "completed",
            "failed",
            "aborted",
          ]}
          onChange={(value) => set({ status: value || undefined })}
        />
        <FilterField
          label="Investigation severity"
          value={severity}
          options={["medium", "warning", "high", "critical"]}
          onChange={(value) => set({ severity: value || undefined })}
        />
        <FilterField
          label="Agent connection record"
          value={cluster}
          onChange={(value) => set({ cluster: value || undefined })}
        />
        <FilterField
          label="Investigation source"
          value={source}
          options={["event", "user"]}
          onChange={(value) => set({ source: value || undefined })}
        />
        <FilterField
          type="date"
          label="From date"
          value={from}
          onChange={(value) => set({ from: value || undefined })}
        />
        <FilterField
          type="date"
          label="To date"
          value={to}
          onChange={(value) => set({ to: value || undefined })}
        />
        <p className="w-full text-xs text-foreground/70">
          Agent connection record matches only that exact resource type; other
          affected resources are not treated as clusters.
        </p>
      </div>
      {q.isLoading || findings.isLoading ? (
        <LoadingPanel title="Loading authorized investigations" />
      ) : rows.length ? (
        rows.map(({ session: s, finding }) => (
          <ActionButton
            intent="bare"
            size="none"
            key={s.id}
            onClick={() => onSelect(s.id)}
            className={cn(
              "block w-full whitespace-normal rounded-lg border p-3 text-left font-normal",
              selected === s.id && "border-primary",
            )}
          >
            <div className="flex flex-wrap items-center gap-2">
              <b>{s.intent}</b>
              <StatusBadge status="incident" label="Shared incident" />
              {finding && <StatusBadge status={finding.severity} />}
            </div>
            <span className="mt-1 block text-xs text-foreground/70">
              {s.resourceScopeSummary}
            </span>
          </ActionButton>
        ))
      ) : (
        <EmptyState
          icon={Clock}
          title="No investigations match"
          description="Shared incident investigations appear only while you can read every affected Astronomer resource."
          {...filterEmptyAction(
            Boolean(status || severity || cluster || source || from || to),
            () =>
              set({
                status: undefined,
                severity: undefined,
                cluster: undefined,
                source: undefined,
                from: undefined,
                to: undefined,
              }),
          )}
        />
      )}
      {detail && (
        <section
          className="rounded-lg border p-4"
          aria-label="Investigation detail"
        >
          <h2 className="font-medium">{detail.session.intent}</h2>
          <p className="mt-1 text-xs text-foreground/70">
            Shared incident metadata is visible because you can currently read
            every affected Astronomer resource. Evidence remains in Charlie and
            is fetched only through authorized detail requests.
          </p>
          {detail.finding && (
            <>
              <RouterLink
                className="mt-2 inline-block text-sm text-primary underline"
                to={resourceHref(
                  detail.finding.affectedResource.type,
                  detail.finding.affectedResource.id,
                )}
              >
                Open originating{" "}
                {detail.finding.affectedResource.type.replaceAll("_", " ")}
              </RouterLink>
              <p className="mt-3 text-sm">
                Repeated {detail.finding.repeatCount ?? 1} time
                {(detail.finding.repeatCount ?? 1) === 1 ? "" : "s"}.
              </p>
              <ol
                className="mt-2 border-l pl-4 text-xs text-foreground/70"
                aria-label="Investigation timeline"
              >
                {detail.finding.createdAt && (
                  <li>
                    First observed{" "}
                    {new Date(detail.finding.createdAt).toLocaleString()}
                  </li>
                )}
                {detail.finding.updatedAt && (
                  <li>
                    Last observed{" "}
                    {new Date(detail.finding.updatedAt).toLocaleString()}
                  </li>
                )}
              </ol>
            </>
          )}
        </section>
      )}
    </div>
  );
}
