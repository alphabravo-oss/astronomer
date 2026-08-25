import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  Clock3,
  Play,
  Save,
  Search,
  Share2,
  Square,
  Trash2,
} from "lucide-react";
import {
  createLoggingSavedSearch,
  deleteLoggingSavedSearch,
  getLoggingSavedSearches,
  queryLoggingOutput,
  updateLoggingSavedSearch,
  type LoggingQueryResult,
  type LoggingSavedSearch,
} from "@/lib/api/logging";
import {
  buildSharedLoggingURL,
  parseSharedLoggingFilters,
  type SharedLoggingFilters,
} from "@/lib/logging-share";
import { toastSuccess } from "@/lib/toast";
import { ActionButton } from "@/components/ui/action-button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { ModalShell } from "@/components/ui/modal-shell";
import { Textarea } from "@/components/ui/textarea";
import type { LoggingOutput } from "@/types";

function outputTypeOf(row: LoggingOutput): string {
  return row.outputType || row.type || "";
}

function namespaceList(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function initialSharedFilters(outputId: string): SharedLoggingFilters | null {
  if (typeof window === "undefined") return null;
  const filters = parseSharedLoggingFilters(window.location.href);
  return filters?.outputId === outputId ? filters : null;
}

function useLoggingQueryState(
  output: LoggingOutput,
  initial: SharedLoggingFilters | null,
) {
  const [query, setQuery] = useState(initial?.query || "");
  const [namespaces, setNamespaces] = useState(
    initial?.namespaces.join(", ") || "",
  );
  const [limit, setLimit] = useState(initial?.limit || 100);
  const [start, setStart] = useState(toUTCDateTimeInput(initial?.start));
  const [end, setEnd] = useState(toUTCDateTimeInput(initial?.end));
  const [result, setResult] = useState<LoggingQueryResult | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [liveTail, setLiveTail] = useState(false);
  const inFlight = useRef(false);
  const namespaceValues = useMemo(
    () => namespaceList(namespaces),
    [namespaces],
  );

  const runQuery = useCallback(
    async (tail = liveTail) => {
      if (inFlight.current) return;
      inFlight.current = true;
      setLoading(true);
      setError("");
      try {
        const now = new Date();
        setResult(
          await queryLoggingOutput(output.id, {
            query,
            limit,
            namespaces: namespaceValues,
            start: tail
              ? new Date(now.getTime() - 5 * 60_000).toISOString()
              : fromUTCDateTimeInput(start),
            end: tail ? now.toISOString() : fromUTCDateTimeInput(end),
            direction: "backward",
          }),
        );
      } catch (queryError) {
        setError(
          queryError instanceof Error ? queryError.message : "Log query failed",
        );
      } finally {
        inFlight.current = false;
        setLoading(false);
      }
    },
    [end, limit, liveTail, namespaceValues, output.id, query, start],
  );

  useEffect(() => {
    if (!liveTail) return;
    void runQuery(true);
    const timer = window.setInterval(() => void runQuery(true), 5_000);
    return () => window.clearInterval(timer);
  }, [liveTail, runQuery]);

  const expandContext = () => {
    if (!result) return;
    const resultStart = new Date(result.start);
    const resultEnd = new Date(result.end);
    if (
      Number.isNaN(resultStart.getTime()) ||
      Number.isNaN(resultEnd.getTime())
    )
      return;
    setStart(
      toUTCDateTimeInput(
        new Date(resultStart.getTime() - 5 * 60_000).toISOString(),
      ),
    );
    setEnd(
      toUTCDateTimeInput(
        new Date(resultEnd.getTime() + 5 * 60_000).toISOString(),
      ),
    );
    setLiveTail(false);
  };

  return {
    query,
    setQuery,
    namespaces,
    setNamespaces,
    namespaceValues,
    limit,
    setLimit,
    start,
    setStart,
    end,
    setEnd,
    result,
    error,
    setError,
    loading,
    liveTail,
    setLiveTail,
    runQuery,
    expandContext,
  };
}

type LoggingQueryState = ReturnType<typeof useLoggingQueryState>;

function useSavedSearchController(
  output: LoggingOutput,
  state: LoggingQueryState,
) {
  const { setError } = state;
  const [searches, setSearches] = useState<LoggingSavedSearch[]>([]);
  const [selectedId, setSelectedId] = useState("");
  const [name, setName] = useState("");
  const [saving, setSaving] = useState(false);

  const refresh = useCallback(async () => {
    try {
      setSearches(await getLoggingSavedSearches(output.id));
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : "Failed to load saved searches",
      );
    }
  }, [output.id, setError]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const select = (id: string) => {
    setSelectedId(id);
    const saved = searches.find((candidate) => candidate.id === id);
    if (!saved) {
      setName("");
      return;
    }
    setName(saved.name);
    state.setQuery(saved.query);
    state.setNamespaces(saved.namespaces.join(", "));
    state.setLimit(saved.limit);
    state.setLiveTail(Boolean(saved.liveTail && output.capabilities?.tail));
  };

  const save = async () => {
    const trimmedName = name.trim();
    if (!trimmedName) {
      setError("Enter a name before saving this search");
      return;
    }
    setSaving(true);
    setError("");
    try {
      const input = {
        name: trimmedName,
        query: state.query,
        namespaces: state.namespaceValues,
        limit: state.limit,
        direction: "backward" as const,
        liveTail: Boolean(state.liveTail && output.capabilities?.tail),
      };
      const saved = selectedId
        ? await updateLoggingSavedSearch(selectedId, input)
        : await createLoggingSavedSearch({ ...input, outputId: output.id });
      setSelectedId(saved.id);
      await refresh();
      toastSuccess(selectedId ? "Saved search updated" : "Search saved");
    } catch (error) {
      setError(
        error instanceof Error ? error.message : "Failed to save search",
      );
    } finally {
      setSaving(false);
    }
  };

  const remove = async () => {
    if (!selectedId) return;
    setSaving(true);
    setError("");
    try {
      await deleteLoggingSavedSearch(selectedId);
      setSelectedId("");
      setName("");
      await refresh();
      toastSuccess("Saved search deleted");
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : "Failed to delete saved search",
      );
    } finally {
      setSaving(false);
    }
  };

  return {
    searches,
    selectedId,
    name,
    setName,
    saving,
    select,
    save,
    remove,
  };
}

type SavedSearchController = ReturnType<typeof useSavedSearchController>;

export function LoggingQueryDialog({
  output,
  onClose,
}: {
  output: LoggingOutput;
  onClose: () => void;
}) {
  const state = useLoggingQueryState(output, initialSharedFilters(output.id));
  const saved = useSavedSearchController(output, state);
  const [shareStatus, setShareStatus] = useState("");

  const shareFilters = async () => {
    if (typeof window === "undefined") return;
    const sharedURL = buildSharedLoggingURL(window.location.href, {
      outputId: output.id,
      query: state.query,
      namespaces: state.namespaceValues,
      limit: state.limit,
      start: fromUTCDateTimeInput(state.start),
      end: fromUTCDateTimeInput(state.end),
    });
    window.history.replaceState(null, "", sharedURL);
    try {
      await navigator.clipboard.writeText(sharedURL);
      setShareStatus("Share link copied");
      toastSuccess("Share link copied");
    } catch {
      setShareStatus("Share link is now in the address bar");
    }
  };

  return (
    <ModalShell
      title={`Query ${output.name}`}
      subtitle={`${outputTypeOf(output)} · results are constrained to the authorized cluster`}
      size="xl"
      onClose={onClose}
      footer={
        <QueryDialogFooter
          output={output}
          state={state}
          onClose={onClose}
          onShare={shareFilters}
        />
      }
      footerClassName="flex flex-wrap items-center justify-end gap-2"
    >
      <SavedSearchControls controller={saved} />
      <QueryFilterControls output={output} state={state} />
      {state.liveTail ? (
        <p role="status" className="text-xs text-status-success">
          Live tail refreshes the latest five-minute window every five seconds.
        </p>
      ) : null}
      {shareStatus ? (
        <p role="status" className="text-xs text-muted-foreground">
          {shareStatus}
        </p>
      ) : null}
      {state.error ? (
        <div
          role="alert"
          className="rounded-md border border-status-error/30 bg-status-error/10 p-3 text-sm text-status-error"
        >
          {state.error}
        </div>
      ) : null}
      <QueryResults state={state} />
    </ModalShell>
  );
}

function QueryDialogFooter({
  output,
  state,
  onClose,
  onShare,
}: {
  output: LoggingOutput;
  state: LoggingQueryState;
  onClose: () => void;
  onShare: () => Promise<void>;
}) {
  return (
    <>
      <ActionButton
        icon={<Share2 className="h-4 w-4" />}
        onClick={() => void onShare()}
      >
        Share filters
      </ActionButton>
      {output.capabilities?.tail ? (
        <ActionButton
          icon={
            state.liveTail ? (
              <Square className="h-4 w-4" />
            ) : (
              <Play className="h-4 w-4" />
            )
          }
          onClick={() => state.setLiveTail((active) => !active)}
        >
          {state.liveTail ? "Stop live tail" : "Start live tail"}
        </ActionButton>
      ) : null}
      <ActionButton onClick={onClose}>Close</ActionButton>
      <ActionButton
        intent="primary"
        icon={<Search className="h-4 w-4" />}
        loading={state.loading}
        loadingLabel="Querying"
        disabled={state.liveTail}
        onClick={() => void state.runQuery(false)}
      >
        Run query
      </ActionButton>
    </>
  );
}

function SavedSearchControls({
  controller,
}: {
  controller: SavedSearchController;
}) {
  return (
    <section
      aria-label="Saved searches"
      className="grid gap-3 rounded-md border border-border bg-muted/20 p-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto_auto]"
    >
      <div className="space-y-1.5">
        <label htmlFor="logging-saved-search" className="text-sm font-medium">
          Saved search
        </label>
        <select
          id="logging-saved-search"
          value={controller.selectedId}
          onChange={(event) => controller.select(event.target.value)}
          className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
        >
          <option value="">New saved search</option>
          {controller.searches.map((saved) => (
            <option key={saved.id} value={saved.id}>
              {saved.name}
            </option>
          ))}
        </select>
      </div>
      <div className="space-y-1.5">
        <label htmlFor="logging-saved-name" className="text-sm font-medium">
          Name
        </label>
        <Input
          id="logging-saved-name"
          value={controller.name}
          maxLength={120}
          onChange={(event) => controller.setName(event.target.value)}
          placeholder="Production errors"
        />
      </div>
      <ActionButton
        icon={<Save className="h-4 w-4" />}
        loading={controller.saving}
        disabled={!controller.name.trim()}
        className="self-end"
        onClick={() => void controller.save()}
      >
        Save
      </ActionButton>
      <ActionButton
        icon={<Trash2 className="h-4 w-4" />}
        disabled={!controller.selectedId || controller.saving}
        className="self-end"
        onClick={() => void controller.remove()}
      >
        Delete
      </ActionButton>
    </section>
  );
}

function QueryFilterControls({
  output,
  state,
}: {
  output: LoggingOutput;
  state: LoggingQueryState;
}) {
  const isLoki = outputTypeOf(output) === "loki";
  return (
    <>
      <div className="space-y-1.5">
        <label htmlFor="logging-query" className="text-sm font-medium">
          {isLoki ? "LogQL" : "Search query"}
        </label>
        <Textarea
          id="logging-query"
          value={state.query}
          onChange={(event) => state.setQuery(event.target.value)}
          placeholder={isLoki ? `{app="api"} |= "error"` : "level:error"}
          rows={4}
        />
        <p className="text-xs text-muted-foreground">
          Cluster and namespace guards are applied by the management API and
          cannot be overridden by the query.
        </p>
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <InputField label="Namespaces" htmlFor="logging-query-namespaces">
          <Input
            id="logging-query-namespaces"
            value={state.namespaces}
            onChange={(event) => state.setNamespaces(event.target.value)}
            placeholder="payments, platform"
          />
        </InputField>
        <InputField label="Result limit" htmlFor="logging-query-limit">
          <Input
            id="logging-query-limit"
            type="number"
            min={1}
            max={1000}
            value={state.limit}
            onChange={(event) => state.setLimit(Number(event.target.value))}
          />
        </InputField>
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <InputField label="Start (UTC)" htmlFor="logging-query-start">
          <Input
            id="logging-query-start"
            type="datetime-local"
            value={state.start}
            disabled={state.liveTail}
            onChange={(event) => state.setStart(event.target.value)}
          />
        </InputField>
        <InputField label="End (UTC)" htmlFor="logging-query-end">
          <Input
            id="logging-query-end"
            type="datetime-local"
            value={state.end}
            disabled={state.liveTail}
            onChange={(event) => state.setEnd(event.target.value)}
          />
        </InputField>
      </div>
    </>
  );
}

function InputField({
  label,
  htmlFor,
  children,
}: {
  label: string;
  htmlFor: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={htmlFor} className="text-sm font-medium">
        {label}
      </label>
      {children}
    </div>
  );
}

function QueryResults({ state }: { state: LoggingQueryState }) {
  if (!state.result) return null;
  return (
    <section aria-label="Log query results" className="space-y-2">
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <Badge variant="info">{state.result.backend}</Badge>
        <span>{state.result.limit} result limit</span>
        <span>
          {state.result.start} – {state.result.end}
        </span>
        <ActionButton
          icon={<Clock3 className="h-3.5 w-3.5" />}
          onClick={state.expandContext}
        >
          Expand ±5m
        </ActionButton>
      </div>
      <pre
        aria-live={state.liveTail ? "polite" : "off"}
        className="max-h-[45vh] overflow-auto rounded-md border border-border bg-muted/40 p-3 text-xs text-foreground"
      >
        {JSON.stringify(state.result.data, null, 2)}
      </pre>
    </section>
  );
}

function toUTCDateTimeInput(value?: string | null): string {
  if (!value) return "";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime())
    ? ""
    : parsed.toISOString().slice(0, 16);
}

function fromUTCDateTimeInput(value: string): string | undefined {
  if (!value) return undefined;
  const parsed = new Date(`${value}:00Z`);
  return Number.isNaN(parsed.getTime()) ? undefined : parsed.toISOString();
}
