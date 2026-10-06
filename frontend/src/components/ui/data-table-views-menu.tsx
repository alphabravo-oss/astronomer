import { useEffect, useRef, useState } from "react";
import { Bookmark, Check, Pencil, Star, Trash2, X } from "lucide-react";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Tooltip } from "@/components/ui/tooltip";
import {
  isEmptyViewState,
  sanitizeViewState,
  type TableViewState,
} from "@/components/ui/data-table-view-state";
import { MAX_TABLE_VIEWS, type TableView } from "@/lib/api/table-views";
import { useTableViews } from "@/lib/hooks/table-views";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { cn } from "@/lib/utils";

interface DataTableViewsMenuProps {
  /** Normalized server table key. */
  tableKey: string;
  columnKeys: ReadonlySet<string>;
  /** The table's current state, saved by "Save current view". */
  current: TableViewState;
  onApply: (state: TableViewState) => void;
  /** URL state present at mount wins over the default view. */
  hasInitialUrlState: () => boolean;
}

type ViewsApi = ReturnType<typeof useTableViews>;

const iconButton =
  "inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring";

/**
 * Per-user saved views for one table: apply, save the current state, rename,
 * delete and choose a default (applied on load unless the URL already carries
 * state). Views persist server-side; the URL carries the live state.
 */
export function DataTableViewsMenu({
  tableKey,
  columnKeys,
  current,
  onApply,
  hasInitialUrlState,
}: DataTableViewsMenuProps) {
  const api = useTableViews(tableKey);
  const { views, defaultView, isLoading } = api;
  const [open, setOpen] = useState(false);

  // Apply the default view once, after the list first resolves.
  const defaultHandled = useRef(false);
  useEffect(() => {
    if (defaultHandled.current || isLoading) return;
    defaultHandled.current = true;
    if (defaultView && !hasInitialUrlState()) {
      onApply(sanitizeViewState(defaultView.state, columnKeys));
    }
  }, [isLoading, defaultView, hasInitialUrlState, onApply, columnKeys]);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="inline-flex h-9 items-center gap-1.5 rounded-md border border-border px-3 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          <Bookmark className="h-4 w-4" />
          Views
          {views.length > 0 && (
            <span className="text-xs text-muted-foreground">
              ({views.length})
            </span>
          )}
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 p-2">
        <p className="px-1 pb-1 text-xs font-medium text-muted-foreground">
          Saved views
        </p>
        {views.length === 0 ? (
          <p className="px-1 py-2 text-sm text-muted-foreground">
            No saved views yet. Set filters, sort or columns, then save them
            below.
          </p>
        ) : (
          <ul className="max-h-64 space-y-0.5 overflow-y-auto">
            {views.map((view) => (
              <ViewRow
                key={view.id}
                view={view}
                api={api}
                onApply={() => {
                  onApply(sanitizeViewState(view.state, columnKeys));
                  setOpen(false);
                }}
              />
            ))}
          </ul>
        )}
        <SaveViewForm api={api} current={current} />
      </PopoverContent>
    </Popover>
  );
}

function ViewRow({
  view,
  api,
  onApply,
}: {
  view: TableView;
  api: ViewsApi;
  onApply: () => void;
}) {
  const { update, remove } = api;
  const [editing, setEditing] = useState(false);
  const [editName, setEditName] = useState(view.name);
  const [confirming, setConfirming] = useState(false);

  const commitRename = () => {
    const next = editName.trim();
    if (!next) return;
    update.mutate(
      { id: view.id, patch: { name: next } },
      {
        onSuccess: () => setEditing(false),
        onError: (error) => toastApiError("Could not rename view", error),
      },
    );
  };

  if (editing) {
    return (
      <li className="flex items-center gap-1 rounded-sm">
        <input
          aria-label={`New name for view ${view.name}`}
          ref={(el) => el?.focus()}
          value={editName}
          maxLength={64}
          onChange={(event) => setEditName(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") commitRename();
            if (event.key === "Escape") setEditing(false);
          }}
          className="h-7 min-w-0 flex-1 rounded-sm border border-border bg-background px-2 text-sm"
        />
        <button
          type="button"
          aria-label="Save new name"
          className={iconButton}
          onClick={commitRename}
        >
          <Check className="h-3.5 w-3.5" />
        </button>
        <button
          type="button"
          aria-label="Cancel rename"
          className={iconButton}
          onClick={() => setEditing(false)}
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </li>
    );
  }

  return (
    <li className="flex items-center gap-1 rounded-sm hover:bg-accent/60">
      <button
        type="button"
        onClick={onApply}
        className="flex min-w-0 flex-1 items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
      >
        <span className="truncate">{view.name}</span>
        {view.is_default && (
          <span className="shrink-0 rounded-full bg-primary/15 px-1.5 text-2xs">
            default
          </span>
        )}
      </button>
      <Tooltip content={view.is_default ? "Remove default" : "Set as default"}>
        <button
          type="button"
          aria-label={
            view.is_default
              ? `Unset ${view.name} as default`
              : `Set ${view.name} as default`
          }
          aria-pressed={view.is_default}
          className={cn(iconButton, view.is_default && "text-foreground")}
          onClick={() =>
            update.mutate(
              { id: view.id, patch: { is_default: !view.is_default } },
              {
                onError: (error) =>
                  toastApiError("Could not update view", error),
              },
            )
          }
        >
          <Star
            className={cn("h-3.5 w-3.5", view.is_default && "fill-current")}
          />
        </button>
      </Tooltip>
      <button
        type="button"
        aria-label={`Rename ${view.name}`}
        className={iconButton}
        onClick={() => {
          setEditName(view.name);
          setConfirming(false);
          setEditing(true);
        }}
      >
        <Pencil className="h-3.5 w-3.5" />
      </button>
      {confirming ? (
        <button
          type="button"
          aria-label={`Confirm delete ${view.name}`}
          className="h-7 rounded-sm bg-status-error px-2 text-xs text-background"
          onClick={() =>
            remove.mutate(view.id, {
              onSuccess: () => setConfirming(false),
              onError: (error) => toastApiError("Could not delete view", error),
            })
          }
        >
          Delete?
        </button>
      ) : (
        <button
          type="button"
          aria-label={`Delete ${view.name}`}
          className={iconButton}
          onClick={() => setConfirming(true)}
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      )}
    </li>
  );
}

function SaveViewForm({
  api,
  current,
}: {
  api: ViewsApi;
  current: TableViewState;
}) {
  const { views, create } = api;
  const [name, setName] = useState("");
  const atCap = views.length >= MAX_TABLE_VIEWS;
  const empty = isEmptyViewState(current);
  const canSave = name.trim() !== "" && !atCap && !empty;

  const save = () => {
    if (!canSave) return;
    create.mutate(
      { name: name.trim(), state: current },
      {
        onSuccess: () => {
          setName("");
          toastSuccess("View saved");
        },
        onError: (error) => toastApiError("Could not save view", error),
      },
    );
  };

  return (
    <div className="mt-2 border-t border-border pt-2">
      <form
        className="flex items-center gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          save();
        }}
      >
        <input
          aria-label="View name"
          placeholder="View name"
          value={name}
          maxLength={64}
          onChange={(event) => setName(event.target.value)}
          className="h-8 min-w-0 flex-1 rounded-md border border-border bg-background px-2 text-sm"
        />
        <button
          type="submit"
          disabled={!canSave || create.isPending}
          className="h-8 shrink-0 rounded-md bg-primary px-3 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50"
        >
          Save current view
        </button>
      </form>
      {atCap && (
        <p className="pt-1 text-xs text-muted-foreground">
          Limit of {MAX_TABLE_VIEWS} views reached. Delete one to save another.
        </p>
      )}
      {!atCap && empty && (
        <p className="pt-1 text-xs text-muted-foreground">
          Nothing to save yet: apply a filter, sort or column change first.
        </p>
      )}
    </div>
  );
}
