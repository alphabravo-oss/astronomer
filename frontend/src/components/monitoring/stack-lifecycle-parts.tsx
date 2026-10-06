import { RemoteClusterPicker } from "@/components/clusters/remote-cluster-picker";
import { RemoteStoragePicker } from "@/components/backups/remote-storage-picker";
import { StatusBadge } from "@/components/ui/status-badge";
import { Tooltip } from "@/components/ui/tooltip";
import { cn, formatRelativeTime } from "@/lib/utils";
import type { useMonitoringStackController } from "@/components/monitoring/hooks";
import type { StackField } from "@/components/monitoring/stack-spec";

const inputClass =
  "h-8 w-full rounded-md border border-border bg-background px-2 text-sm focus:outline-hidden focus:ring-1 focus:ring-ring";

export function StackSummary({
  status,
  installed,
}: {
  status: ReturnType<typeof useMonitoringStackController>["status"]["data"];
  installed: boolean;
}) {
  if (!installed) {
    return (
      <p className="text-xs text-muted-foreground">
        No Helm release is recorded for this stack. Preview the values below,
        then install.
      </p>
    );
  }

  const observed = status?.observedRelease;
  const rows: Array<[string, React.ReactNode]> = [
    ["Namespace", status?.namespace || "—"],
    ["Release", status?.releaseName || "—"],
    ["Chart version", status?.chartVersion || "—"],
    [
      "Helm status",
      observed ? (
        <span className="inline-flex items-center gap-1.5">
          <StatusBadge status={observed.status} size="sm" />
          {/*
            no-release-history-or-revision-rollback-ui (separate, still-open
            audit item): the deployed revision below is the anchor a release
            HISTORY table and a revision picker would hang off — the backend
            already records lastObservedRevision and the reconciler rolls back
            to a prior revision itself. Not built here.
          */}
          {typeof observed.revision === "number" && (
            <span className="text-xs text-muted-foreground">
              rev {observed.revision}
            </span>
          )}
        </span>
      ) : (
        "—"
      ),
    ],
    ["Pods", typeof status?.pods === "number" ? String(status.pods) : "—"],
    [
      "Observed",
      observed?.observedAt ? formatRelativeTime(observed.observedAt) : "—",
    ],
  ];

  return (
    <dl className="grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-3 lg:grid-cols-6">
      {rows.map(([label, value]) => (
        <div key={label} className="min-w-0">
          <dt className="text-10 font-medium uppercase tracking-wider text-muted-foreground">
            {label}
          </dt>
          <dd className="truncate text-xs text-foreground">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

export function StackFieldControl({
  field,
  value,
  onChange,
}: {
  field: StackField;
  value: string;
  onChange: (next: string) => void;
}) {
  const label = (
    <span className="flex items-center gap-1.5 text-xs font-medium text-foreground">
      {field.label}
      {field.required && <span className="text-status-error">*</span>}
      {field.replaceTrigger && (
        <Tooltip content="Changing this needs a reinstall (Replace), not an in-place upgrade.">
          <span className="rounded-sm bg-muted px-1 py-0.5 text-9 uppercase tracking-wide text-muted-foreground">
            replace
          </span>
        </Tooltip>
      )}
    </span>
  );

  if (field.kind === "boolean") {
    return (
      <label className="flex items-start gap-2">
        <input
          type="checkbox"
          checked={value === "true"}
          onChange={(event) =>
            onChange(event.target.checked ? "true" : "false")
          }
          className="mt-0.5 h-4 w-4 rounded-sm border-border"
          aria-label={field.label}
        />
        <span className="min-w-0">
          {label}
          {field.help && (
            <span className="mt-0.5 block text-11 text-muted-foreground">
              {field.help}
            </span>
          )}
        </span>
      </label>
    );
  }

  // A checkbox cannot express "I am not asking for either" — and for these
  // fields the form has no idea what the current setting is, because no status
  // endpoint returns them (SERVER_BLIND_FIELDS). The empty option is what keeps
  // the key OUT of the request body so the backend's own policy applies.
  if (field.kind === "tristate") {
    return (
      <label className="block min-w-0">
        {label}
        <select
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className={cn(inputClass, "mt-1")}
          aria-label={field.label}
        >
          <option value="">{field.unsetLabel ?? "Use backend default"}</option>
          <option value="true">Enabled</option>
          <option value="false">Disabled</option>
        </select>
        {field.help && (
          <span className="mt-0.5 block text-11 text-muted-foreground">
            {field.help}
          </span>
        )}
      </label>
    );
  }

  return (
    <div className="block min-w-0">
      {label}
      {field.kind === "cluster" ? (
        <RemoteClusterPicker
          value={value}
          onChange={onChange}
          ariaLabel={field.label}
          className="mt-1"
        />
      ) : field.kind === "storageConfig" ? (
        <RemoteStoragePicker
          value={value}
          onChange={onChange}
          label={field.label}
        />
      ) : (
        <input
          type={field.kind === "number" ? "number" : "text"}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          placeholder={field.placeholder}
          className={cn(inputClass, "mt-1")}
          aria-label={field.label}
        />
      )}
      {field.help && (
        <span className="mt-0.5 block text-11 text-muted-foreground">
          {field.help}
        </span>
      )}
    </div>
  );
}
