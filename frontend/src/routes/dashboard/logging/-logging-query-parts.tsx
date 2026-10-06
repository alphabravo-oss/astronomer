import type { ReactNode } from "react";
import {
  Clock3,
  Play,
  Save,
  Search,
  Share2,
  Square,
  Trash2,
} from "lucide-react";
import { Select } from "@/components/ui/select";
import { ActionButton } from "@/components/ui/action-button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import type { LoggingOutput } from "@/types";
import {
  outputTypeOf,
  type LoggingQueryState,
  type SavedSearchController,
} from "./-logging-query-state";

export function QueryDialogFooter({
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

export function SavedSearchControls({
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
        <Select
          id="logging-saved-search"
          value={controller.selectedId}
          onChange={(event) => controller.select(event.target.value)}
          className="h-(--control-h) w-full rounded-md border border-input bg-background px-3 text-sm"
        >
          <option value="">New saved search</option>
          {controller.searches.map((saved) => (
            <option key={saved.id} value={saved.id}>
              {saved.name}
            </option>
          ))}
        </Select>
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

export function QueryFilterControls({
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

export function QueryResults({ state }: { state: LoggingQueryState }) {
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
