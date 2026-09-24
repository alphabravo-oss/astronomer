import { useNavigate } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Archive,
  CalendarClock,
  RefreshCw,
  RotateCcw,
  XCircle,
} from "lucide-react";

import { ActionButton } from "@/components/ui/action-button";
import { ModalShell } from "@/components/ui/modal-shell";
import { useAppForm, useStore } from "@/lib/form";
import { useClusterNamespaces } from "@/lib/hooks/clusters";
import { RemoteClusterPicker } from "./remote-cluster-picker";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import {
  createSnapshot,
  createSnapshotSchedule,
  restoreSnapshot,
  updateSnapshotSchedule,
  type Snapshot,
  type SnapshotSchedule,
  type SnapshotSpec,
} from "@/lib/api/cluster-velero";

export function parseCommaSeparated(value: string): string[] | undefined {
  const items = value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
  return items.length ? items : undefined;
}

function SnapshotModal({
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

function DialogFooter({
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

function NamespacePicker({
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
              <button
                type="button"
                onClick={() => toggle(namespace)}
                className="hover:text-foreground"
                aria-label={`Remove ${namespace}`}
              >
                <XCircle className="h-3 w-3" />
              </button>
            </span>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function TextField({
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

const inputClass =
  "w-full h-9 px-3 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-hidden focus:ring-2 focus:ring-ring";

export function NewSnapshotDialog({
  clusterId,
  onClose,
  defaultStorageLocation,
}: {
  clusterId: string;
  onClose: () => void;
  defaultStorageLocation?: string;
}) {
  const queryClient = useQueryClient();
  const { data: namespaces } = useClusterNamespaces(clusterId);
  const form = useAppForm({
    defaultValues: {
      selectedNamespaces: [] as string[],
      resources: "",
      ttl: "720h",
      snapshotVolumes: true,
    },
    onSubmit: () => mutation.mutate(),
  });
  const selectedNamespaces = useStore(
    form.store,
    (state) => state.values.selectedNamespaces,
  );
  const mutation = useMutation({
    mutationFn: () => {
      const values = form.state.values;
      const spec: SnapshotSpec = {
        includedNamespaces: values.selectedNamespaces.length
          ? values.selectedNamespaces
          : undefined,
        includedResources: parseCommaSeparated(values.resources),
        snapshotVolumes: values.snapshotVolumes,
        ttl: values.ttl || undefined,
        storageLocation: defaultStorageLocation,
      };
      return createSnapshot(clusterId, { spec });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.clusterPages.snapshots(clusterId),
      });
      toastSuccess("Snapshot queued");
      onClose();
    },
    onError: (error: Error) => toastApiError("Snapshot failed", error),
  });
  return (
    <SnapshotModal
      title="New snapshot"
      icon={<Archive className="h-4 w-4" />}
      onClose={onClose}
    >
      <NamespacePicker
        namespaces={namespaces?.map((namespace) => namespace.name) ?? []}
        selected={selectedNamespaces}
        onChange={(selected) =>
          form.setFieldValue("selectedNamespaces", selected)
        }
      />
      <TextField label="Resources (comma-separated)" id="snapshot-resources">
        <form.Field name="resources">
          {(field) => (
            <input
              id="snapshot-resources"
              type="text"
              value={field.state.value}
              onChange={(event) => field.handleChange(event.target.value)}
              onBlur={field.handleBlur}
              placeholder="e.g. deployments,configmaps,secrets — leave blank for all"
              className={inputClass}
            />
          )}
        </form.Field>
      </TextField>
      <TextField label="TTL" id="snapshot-ttl">
        <form.Field name="ttl">
          {(field) => (
            <input
              id="snapshot-ttl"
              type="text"
              value={field.state.value}
              onChange={(event) => field.handleChange(event.target.value)}
              onBlur={field.handleBlur}
              placeholder="e.g. 720h (30 days)"
              className={`${inputClass} font-mono`}
            />
          )}
        </form.Field>
      </TextField>
      <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer select-none">
        <form.Field name="snapshotVolumes">
          {(field) => (
            <input
              type="checkbox"
              checked={field.state.value}
              onChange={(event) => field.handleChange(event.target.checked)}
              onBlur={field.handleBlur}
              className="h-4 w-4"
            />
          )}
        </form.Field>
        Include PVC snapshots
      </label>
      <DialogFooter
        onCancel={onClose}
        onSubmit={() => void form.handleSubmit()}
        loading={mutation.isPending}
        submitLabel="Create snapshot"
      />
    </SnapshotModal>
  );
}

export function RestoreSnapshotDialog({
  clusterId,
  snapshot,
  onClose,
}: {
  clusterId: string;
  snapshot: Snapshot;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const form = useAppForm({
    defaultValues: {
      targetClusterId: clusterId,
      includedNamespaces: "",
      excludedNamespaces: "",
      restorePVs: true,
    },
    onSubmit: () => mutation.mutate(),
  });
  const mutation = useMutation({
    mutationFn: () => {
      const values = form.state.values;
      return restoreSnapshot(clusterId, snapshot.id, {
        target_cluster_id: values.targetClusterId,
        spec: {
          includedNamespaces: parseCommaSeparated(values.includedNamespaces),
          excludedNamespaces: parseCommaSeparated(values.excludedNamespaces),
          restorePVs: values.restorePVs,
        },
      });
    },
    onSuccess: (receipt) => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.clusterPages.snapshots(clusterId),
      });
      toastSuccess("Restore queued — follow its status in restore history");
      void navigate({
        to: `/dashboard/clusters/${receipt.targetClusterId}/snapshots?restore=${encodeURIComponent(receipt.id)}`,
      });
      onClose();
    },
    onError: (error: Error) => toastApiError("Restore failed", error),
  });
  return (
    <SnapshotModal
      title={`Restore from ${snapshot.name}`}
      icon={<RotateCcw className="h-4 w-4" />}
      onClose={onClose}
    >
      <TextField label="Target cluster" id="restore-target">
        <form.Field name="targetClusterId">
          {(field) => (
            <RemoteClusterPicker
              id="restore-target"
              ariaLabel="Target cluster"
              value={field.state.value}
              onChange={field.handleChange}
              onBlur={field.handleBlur}
            />
          )}
        </form.Field>
      </TextField>
      <details className="rounded-lg border border-border bg-muted/20 px-3 py-2">
        <summary className="text-sm font-medium text-foreground cursor-pointer">
          Advanced
        </summary>
        <div className="pt-3 space-y-3">
          <TextField
            label="Included namespaces (comma-separated)"
            id="restore-included"
          >
            <form.Field name="includedNamespaces">
              {(field) => (
                <input
                  id="restore-included"
                  type="text"
                  value={field.state.value}
                  onChange={(event) => field.handleChange(event.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="leave blank for all"
                  className={inputClass}
                />
              )}
            </form.Field>
          </TextField>
          <TextField
            label="Excluded namespaces (comma-separated)"
            id="restore-excluded"
          >
            <form.Field name="excludedNamespaces">
              {(field) => (
                <input
                  id="restore-excluded"
                  type="text"
                  value={field.state.value}
                  onChange={(event) => field.handleChange(event.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="e.g. kube-system"
                  className={inputClass}
                />
              )}
            </form.Field>
          </TextField>
          <label className="flex items-center gap-2 text-xs text-foreground cursor-pointer select-none">
            <form.Field name="restorePVs">
              {(field) => (
                <input
                  type="checkbox"
                  checked={field.state.value}
                  onChange={(event) => field.handleChange(event.target.checked)}
                  onBlur={field.handleBlur}
                  className="h-4 w-4"
                />
              )}
            </form.Field>
            Restore PersistentVolumes
          </label>
        </div>
      </details>
      <DialogFooter
        onCancel={onClose}
        onSubmit={() => void form.handleSubmit()}
        loading={mutation.isPending}
        submitLabel="Restore"
      />
    </SnapshotModal>
  );
}

export function ScheduleDialog({
  clusterId,
  mode,
  schedule,
  onClose,
}: {
  clusterId: string;
  mode: "create" | "edit";
  schedule?: SnapshotSchedule;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const { data: namespaces } = useClusterNamespaces(clusterId);
  const form = useAppForm({
    defaultValues: {
      name: schedule?.name ?? "",
      cron: schedule?.cron ?? "0 3 * * *",
      enabled: schedule?.enabled ?? true,
      selectedNamespaces: schedule?.spec.includedNamespaces ?? [],
      ttl: schedule?.spec.ttl ?? "720h",
      snapshotVolumes: schedule?.spec.snapshotVolumes ?? true,
    },
    onSubmit: () => mutation.mutate(),
  });
  const selectedNamespaces = useStore(
    form.store,
    (state) => state.values.selectedNamespaces,
  );
  const name = useStore(form.store, (state) => state.values.name);
  const cron = useStore(form.store, (state) => state.values.cron);
  const isEdit = mode === "edit" && schedule != null;
  const mutation = useMutation({
    mutationFn: () => {
      const values = form.state.values;
      const spec: SnapshotSpec = {
        includedNamespaces: values.selectedNamespaces.length
          ? values.selectedNamespaces
          : undefined,
        snapshotVolumes: values.snapshotVolumes,
        ttl: values.ttl || undefined,
      };
      return isEdit
        ? updateSnapshotSchedule(clusterId, schedule.id, {
            name: values.name,
            cron: values.cron,
            enabled: values.enabled,
            spec,
          })
        : createSnapshotSchedule(clusterId, {
            name: values.name,
            cron: values.cron,
            enabled: values.enabled,
            spec,
          });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.clusterPages.snapshotSchedules(clusterId),
      });
      toastSuccess(isEdit ? "Schedule updated" : "Schedule created");
      onClose();
    },
    onError: (error: Error) => toastApiError("Schedule failed", error),
  });
  return (
    <SnapshotModal
      title={
        isEdit ? `Edit schedule — ${schedule.name}` : "New snapshot schedule"
      }
      icon={<CalendarClock className="h-4 w-4" />}
      onClose={onClose}
    >
      <TextField label="Name" id="schedule-name">
        <form.Field name="name">
          {(field) => (
            <input
              id="schedule-name"
              type="text"
              value={field.state.value}
              onChange={(event) => field.handleChange(event.target.value)}
              onBlur={field.handleBlur}
              placeholder="e.g. nightly-prod"
              disabled={isEdit}
              className={`${inputClass} disabled:bg-muted/50 disabled:text-muted-foreground`}
            />
          )}
        </form.Field>
      </TextField>
      <TextField label="Cron" id="schedule-cron">
        <form.Field name="cron">
          {(field) => (
            <input
              id="schedule-cron"
              type="text"
              value={field.state.value}
              onChange={(event) => field.handleChange(event.target.value)}
              onBlur={field.handleBlur}
              placeholder="0 3 * * *"
              className={`${inputClass} font-mono`}
            />
          )}
        </form.Field>
      </TextField>
      <NamespacePicker
        namespaces={namespaces?.map((namespace) => namespace.name) ?? []}
        selected={selectedNamespaces}
        onChange={(selected) =>
          form.setFieldValue("selectedNamespaces", selected)
        }
      />
      <TextField label="TTL" id="schedule-ttl">
        <form.Field name="ttl">
          {(field) => (
            <input
              id="schedule-ttl"
              type="text"
              value={field.state.value}
              onChange={(event) => field.handleChange(event.target.value)}
              onBlur={field.handleBlur}
              className={`${inputClass} font-mono`}
            />
          )}
        </form.Field>
      </TextField>
      <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer select-none">
        <form.Field name="snapshotVolumes">
          {(field) => (
            <input
              type="checkbox"
              checked={field.state.value}
              onChange={(event) => field.handleChange(event.target.checked)}
              onBlur={field.handleBlur}
              className="h-4 w-4"
            />
          )}
        </form.Field>
        Include PVC snapshots
      </label>
      <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer select-none">
        <form.Field name="enabled">
          {(field) => (
            <input
              type="checkbox"
              checked={field.state.value}
              onChange={(event) => field.handleChange(event.target.checked)}
              onBlur={field.handleBlur}
              className="h-4 w-4"
            />
          )}
        </form.Field>
        Enabled
      </label>
      <DialogFooter
        onCancel={onClose}
        onSubmit={() => void form.handleSubmit()}
        loading={mutation.isPending}
        submitLabel={isEdit ? "Save" : "Create schedule"}
        disabled={!name || !cron}
      />
    </SnapshotModal>
  );
}
