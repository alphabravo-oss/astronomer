import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  deleteSnapshot,
  deleteSnapshotSchedule,
  getVeleroStatus,
  listSnapshotSchedules,
  listSnapshots,
  updateSnapshotSchedule,
  type Snapshot,
  type SnapshotSchedule,
} from "@/lib/api/cluster-velero";
import { liveFallback } from "@/lib/live/status-store";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";

type CloseDialogs = {
  closeSnapshotDelete: () => void;
  closeScheduleDelete: () => void;
};

export function useVeleroSnapshotPage(
  clusterId: string,
  { closeSnapshotDelete, closeScheduleDelete }: CloseDialogs,
) {
  const queryClient = useQueryClient();
  const veleroQuery = useQuery({
    queryKey: queryKeys.clusterPages.veleroStatus(clusterId),
    queryFn: ({ signal }) => getVeleroStatus(clusterId, signal),
    enabled: !!clusterId,
    refetchInterval: liveFallback(30_000),
    refetchIntervalInBackground: false,
  });
  const veleroReady = Boolean(veleroQuery.data?.installed);
  const snapshotsQuery = useQuery({
    queryKey: queryKeys.clusterPages.snapshots(clusterId),
    queryFn: ({ signal }) => listSnapshots(clusterId, signal),
    enabled: !!clusterId && veleroReady,
    refetchInterval: liveFallback(30_000),
    refetchIntervalInBackground: false,
  });
  const schedulesQuery = useQuery({
    queryKey: queryKeys.clusterPages.snapshotSchedules(clusterId),
    queryFn: ({ signal }) => listSnapshotSchedules(clusterId, signal),
    enabled: !!clusterId && veleroReady,
    refetchInterval: liveFallback(60_000),
    refetchIntervalInBackground: false,
  });
  const deleteSnapshotMutation = useMutation({
    mutationFn: (snapshotId: string) => deleteSnapshot(clusterId, snapshotId),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.clusterPages.snapshots(clusterId),
      });
      toastSuccess("Snapshot delete initiated");
      closeSnapshotDelete();
    },
    onError: (error: Error) => toastApiError("Delete failed", error),
  });
  const deleteScheduleMutation = useMutation({
    mutationFn: (scheduleId: string) =>
      deleteSnapshotSchedule(clusterId, scheduleId),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.clusterPages.snapshotSchedules(clusterId),
      });
      toastSuccess("Schedule deleted");
      closeScheduleDelete();
    },
    onError: (error: Error) => toastApiError("Delete failed", error),
  });
  const toggleScheduleMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      updateSnapshotSchedule(clusterId, id, { enabled }),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.clusterPages.snapshotSchedules(clusterId),
      });
    },
    onError: (error: Error) => toastApiError("Toggle failed", error),
  });

  const defaultStorageLocation =
    veleroQuery.data?.storageLocations?.find(
      (location) => location.default && location.phase === "Available",
    )?.name ??
    veleroQuery.data?.storageLocations?.find(
      (location) => location.phase === "Available",
    )?.name ??
    veleroQuery.data?.storageLocations?.[0]?.name;

  return {
    veleroQuery,
    snapshotsQuery,
    schedulesQuery,
    veleroReady,
    defaultStorageLocation,
    deleteSnapshotMutation,
    deleteScheduleMutation,
    toggleScheduleMutation,
  };
}

export type { Snapshot, SnapshotSchedule };
