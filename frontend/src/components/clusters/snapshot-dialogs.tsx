import { useOperationIntent } from "@/lib/use-operation-intent";
import { useNavigate } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Archive, CalendarClock, RotateCcw } from "lucide-react";

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
import {
  parseCommaSeparated,
  SnapshotModal,
  DialogFooter,
  NamespacePicker,
  TextField,
  inputClass,
} from "./snapshot-dialog-parts";
export { parseCommaSeparated } from "./snapshot-dialog-parts";

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
  const intent = useOperationIntent();
  const mutation = useMutation({
    mutationFn: () => {
      const values = form.state.values;
      const body = {
        target_cluster_id: values.targetClusterId,
        spec: {
          includedNamespaces: parseCommaSeparated(values.includedNamespaces),
          excludedNamespaces: parseCommaSeparated(values.excludedNamespaces),
          restorePVs: values.restorePVs,
        },
      };
      return restoreSnapshot(
        clusterId,
        snapshot.id,
        body,
        undefined,
        intent.keyFor({ clusterId, snapshotId: snapshot.id, body }),
      );
    },
    onSuccess: (receipt) => {
      intent.complete();
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
