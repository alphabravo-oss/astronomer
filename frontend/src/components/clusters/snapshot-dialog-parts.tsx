import { useMemo, useState } from "react";
import { RefreshCw, XCircle } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { ModalShell } from "@/components/ui/modal-shell";
import { BareButton } from "@/components/form/bare-button";

export function parseCommaSeparated(value: string): string[] | undefined {
  const items = value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
  return items.length ? items : undefined;
}

export function SnapshotModal({
  title,
  icon,
  onClose,
  children,
}: {
  title: string;
  icon: React.ReactNode;
  onClose: () => void;
  children: React.ReactNode;
}) {
  return (
    <ModalShell
      title={title}
      onClose={onClose}
      size="md"
      titleIcon={
        <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center text-muted-foreground shrink-0">
          {icon}
        </div>
      }
    >
      {children}
    </ModalShell>
  );
}

export function DialogFooter({
  onCancel,
  onSubmit,
  loading,
  submitLabel,
  disabled,
}: {
  onCancel: () => void;
  onSubmit: () => void;
  loading?: boolean;
  submitLabel: string;
  disabled?: boolean;
}) {
  return (
    <div className="flex items-center justify-end gap-2 pt-2 border-t border-border -mx-6 px-6 pb-0 mt-2">
      <div className="pt-3 flex items-center gap-2">
        <ActionButton onClick={onCancel} disabled={loading} intent="ghost">
          Cancel
        </ActionButton>
        <ActionButton
          onClick={onSubmit}
          disabled={loading || disabled}
          intent="primary"
          loading={loading}
          icon={<RefreshCw className="h-3.5 w-3.5" />}
        >
          {submitLabel}
        </ActionButton>
      </div>
    </div>
  );
}

export function NamespacePicker({
  namespaces,
  selected,
  onChange,
}: {
  namespaces: string[];
  selected: string[];
  onChange: (namespaces: string[]) => void;
}) {
  const sorted = useMemo(() => [...namespaces].sort(), [namespaces]);
  const [filter, setFilter] = useState("");
  const filtered = sorted.filter((namespace) =>
    namespace.toLowerCase().includes(filter.toLowerCase()),
  );
  const toggle = (namespace: string) =>
    onChange(
      selected.includes(namespace)
        ? selected.filter((item) => item !== namespace)
        : [...selected, namespace],
    );
  return (
    <div className="space-y-1.5">
      <label
        className="text-sm font-medium text-foreground"
        htmlFor="snapshot-namespaces"
      >
        Namespaces{" "}
        <span className="text-xs text-muted-foreground font-normal">
          (leave empty for all)
        </span>
      </label>
      <input
        id="snapshot-namespaces"
        type="text"
        value={filter}
        onChange={(event) => setFilter(event.target.value)}
        placeholder="Filter namespaces…"
        className="w-full h-8 px-2.5 rounded-md border border-border bg-background text-xs placeholder:text-muted-foreground focus:outline-hidden focus:ring-1 focus:ring-ring"
      />
      <div className="rounded-md border border-border bg-background max-h-40 overflow-y-auto">
        {filtered.length === 0 ? (
          <div className="text-xs text-muted-foreground px-3 py-2">
            No namespaces match.
          </div>
        ) : (
          filtered.map((namespace) => (
            <label
              key={namespace}
              className="flex items-center gap-2 px-3 py-1 text-xs hover:bg-accent/40 cursor-pointer"
            >
              <input
                type="checkbox"
                checked={selected.includes(namespace)}
                onChange={() => toggle(namespace)}
                className="h-3.5 w-3.5"
              />
              <span className="font-mono">{namespace}</span>
            </label>
          ))
        )}
      </div>
      {selected.length > 0 ? (
        <div className="flex flex-wrap gap-1 pt-1">
          {selected.map((namespace) => (
            <span
              key={namespace}
              className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm text-xs bg-muted border border-border text-muted-foreground"
            >
              {namespace}
              <BareButton
                onClick={() => toggle(namespace)}
                className="hover:text-foreground inline-block font-normal"
                aria-label={`Remove ${namespace}`}
              >
                <XCircle className="h-3 w-3" />
              </BareButton>
            </span>
          ))}
        </div>
      ) : null}
    </div>
  );
}

export function TextField({
  label,
  id,
  children,
}: {
  label: string;
  id: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground" htmlFor={id}>
        {label}
      </label>
      {children}
    </div>
  );
}

export const inputClass =
  "w-full h-(--control-h) px-3 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-hidden focus:ring-2 focus:ring-ring";
