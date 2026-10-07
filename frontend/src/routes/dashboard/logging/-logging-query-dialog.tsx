import { useState } from "react";
import { useNavigate, useLocation } from "@tanstack/react-router";
import {
  buildSharedLoggingURL,
  sharedLoggingSearchParams,
  type SharedLoggingFilters,
} from "@/lib/logging-share";
import { toastSuccess } from "@/lib/toast";
import { ModalShell } from "@/components/ui/modal-shell";
import type { LoggingOutput } from "@/types";
import {
  fromUTCDateTimeInput,
  initialSharedFilters,
  outputTypeOf,
  useLoggingQueryState,
  useSavedSearchController,
} from "./-logging-query-state";
import {
  QueryDialogFooter,
  QueryFilterControls,
  QueryResults,
  SavedSearchControls,
} from "./-logging-query-parts";

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
  const navigate = useNavigate();
  const pathname = useLocation({ select: (location) => location.pathname });

  const shareFilters = async () => {
    if (typeof window === "undefined") return;
    const filters: SharedLoggingFilters = {
      outputId: output.id,
      query: state.query,
      namespaces: state.namespaceValues,
      limit: state.limit,
      start: fromUTCDateTimeInput(state.start),
      end: fromUTCDateTimeInput(state.end),
    };
    const sharedURL = buildSharedLoggingURL(window.location.href, filters);
    void navigate({
      to: pathname,
      search: (prev: Record<string, unknown>) => ({
        ...prev,
        ...sharedLoggingSearchParams(filters),
      }),
      replace: true,
      resetScroll: false,
    });
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
