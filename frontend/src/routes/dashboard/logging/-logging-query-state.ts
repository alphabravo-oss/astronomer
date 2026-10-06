import { useCallback, useEffect, useMemo, useRef, useState } from "react";
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
  parseSharedLoggingFilters,
  type SharedLoggingFilters,
} from "@/lib/logging-share";
import { toastSuccess } from "@/lib/toast";
import type { LoggingOutput } from "@/types";

export function outputTypeOf(row: LoggingOutput): string {
  return row.outputType || row.type || "";
}

export function namespaceList(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

export function initialSharedFilters(
  outputId: string,
): SharedLoggingFilters | null {
  if (typeof window === "undefined") return null;
  const filters = parseSharedLoggingFilters(window.location.href);
  return filters?.outputId === outputId ? filters : null;
}

export function useLoggingQueryState(
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
  const queryParams = useMemo(
    () => ({ query, limit, namespaceValues, start, end }),
    [query, limit, namespaceValues, start, end],
  );
  const queryParamsRef = useRef(queryParams);
  useEffect(() => {
    queryParamsRef.current = queryParams;
  }, [queryParams]);

  const runQuery = useCallback(
    async (tail = liveTail) => {
      if (inFlight.current) return;
      inFlight.current = true;
      setLoading(true);
      setError("");
      try {
        const params = queryParamsRef.current;
        const now = new Date();
        setResult(
          await queryLoggingOutput(output.id, {
            query: params.query,
            limit: params.limit,
            namespaces: params.namespaceValues,
            start: tail
              ? new Date(now.getTime() - 5 * 60_000).toISOString()
              : fromUTCDateTimeInput(params.start),
            end: tail ? now.toISOString() : fromUTCDateTimeInput(params.end),
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
    [liveTail, output.id],
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

export type LoggingQueryState = ReturnType<typeof useLoggingQueryState>;

export function useSavedSearchController(
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
    let cancelled = false;
    void getLoggingSavedSearches(output.id)
      .then((nextSearches) => {
        if (!cancelled) setSearches(nextSearches);
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        setError(
          error instanceof Error
            ? error.message
            : "Failed to load saved searches",
        );
      });
    return () => {
      cancelled = true;
    };
  }, [output.id, setError]);

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

export type SavedSearchController = ReturnType<typeof useSavedSearchController>;

export function toUTCDateTimeInput(value?: string | null): string {
  if (!value) return "";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime())
    ? ""
    : parsed.toISOString().slice(0, 16);
}

export function fromUTCDateTimeInput(value: string): string | undefined {
  if (!value) return undefined;
  const parsed = new Date(`${value}:00Z`);
  return Number.isNaN(parsed.getTime()) ? undefined : parsed.toISOString();
}
