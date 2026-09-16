import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus, Search, X } from "lucide-react";
import { queryKeys } from "@/lib/query-keys";
import {
  searchCharlieContext,
  type CharlieContextOption,
} from "@/lib/api/charlie";

function ContextPicker({
  open,
  onOpenChange,
  add,
}: {
  open: boolean;
  add: (value: CharlieContextOption) => void;
  onOpenChange: (open: boolean) => void;
}) {
  const [q, setQ] = useState("");
  const result = useQuery({
    queryKey: queryKeys.charlie.contextSearch(q),
    queryFn: () => searchCharlieContext(q),
    enabled: open && (q.trim().length === 0 || q.trim().length >= 2),
    retry: false,
  });
  if (!open)
    return (
      <button
        type="button"
        onClick={() => onOpenChange(true)}
        aria-expanded="false"
        className="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-xs"
      >
        <Plus className="h-3 w-3" />
        Narrow scope
      </button>
    );
  return (
    <div
      className="min-w-72 rounded-md border bg-background p-2 shadow-xs"
      role="search"
    >
      <div className="mb-2 flex items-center justify-between gap-2">
        <p className="text-xs font-medium">Choose a diagnostic scope</p>
        <button
          type="button"
          onClick={() => onOpenChange(false)}
          className="rounded-sm px-1.5 py-0.5 text-xs text-muted-foreground hover:bg-accent"
        >
          Done
        </button>
      </div>
      <label className="flex items-center gap-2">
        <Search className="h-4 w-4" />
        <span className="sr-only">Search components or agent connections</span>
        <input
          aria-label="Search components or agent connections"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search components or agent connections"
          className="w-full bg-transparent text-sm outline-hidden"
        />
      </label>
      {result.isError && (
        <p role="status" className="mt-2 text-xs text-muted-foreground">
          Context search is unavailable for this installation.
        </p>
      )}
      {result.isLoading && (
        <p role="status" className="mt-2 text-xs text-muted-foreground">
          Loading available scopes…
        </p>
      )}
      {result.data?.map((v) => (
        <button
          type="button"
          key={`${v.type}:${v.id}`}
          onClick={() => {
            add(v);
            onOpenChange(false);
          }}
          className="mt-2 block w-full rounded-sm p-2 text-left text-sm hover:bg-accent"
        >
          <b>{v.label}</b>
          <span className="block text-xs text-muted-foreground">
            {v.summary}
          </span>
        </button>
      ))}
      {!result.isLoading && result.data?.length === 0 && (
        <p className="mt-2 text-xs text-muted-foreground">
          No matching scope is available to your account.
        </p>
      )}
    </div>
  );
}

export function CharlieScope({
  resources,
  remove,
  add,
  scopePickerOpen,
  setScopePickerOpen,
}: {
  resources: CharlieContextOption[];
  remove: (id: string) => void;
  add: (resource: CharlieContextOption) => void;
  scopePickerOpen: boolean;
  setScopePickerOpen: (open: boolean) => void;
}) {
  return (
    <div
      id="charlie-assistant-drawer"
      className="shrink-0 space-y-2 border-b border-border px-5 py-3"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <p className="mb-1 text-xs font-medium text-muted-foreground">
            Scope
          </p>
          <div
            className="flex flex-wrap gap-2"
            role="list"
            aria-label="Conversation scope"
          >
            {resources.length === 0 ? (
              <span
                role="listitem"
                className="inline-flex items-center rounded-full bg-muted px-2 py-1 text-xs"
              >
                This Astronomer deployment
              </span>
            ) : null}
            {resources.map((r) => (
              <span
                key={`${r.type}:${r.id}`}
                role="listitem"
                aria-label={`${r.label}: ${r.summary}`}
                className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-1 text-xs"
              >
                {r.label}
                <button
                  type="button"
                  aria-label={`Remove ${r.label}`}
                  onClick={() => remove(`${r.type}:${r.id}`)}
                >
                  <X className="h-3 w-3" />
                </button>
              </span>
            ))}
          </div>
        </div>
        <div className="shrink-0">
          <ContextPicker
            add={add}
            open={scopePickerOpen}
            onOpenChange={setScopePickerOpen}
          />
        </div>
      </div>
      <p className="text-xs text-muted-foreground">
        Charlie retrieves authorized diagnostics through audited read tools when
        needed. Choose a component or agent connection to narrow this
        conversation.
      </p>
    </div>
  );
}
