import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  acknowledgeAlert,
  createAlertRule,
  createAlertSilence,
  createNotificationChannel,
  deleteAlertRule,
  getAnomalyBaseline,
  getAnomalyBaselines,
  getAlertEvents,
  getAlertEventSummary,
  getAlertRules,
  getAlertSilences,
  getNotificationChannels,
  resolveAlert,
  testNotificationChannel,
  updateAlertRule,
  type AlertEventQuery,
  type AlertRuleWrite,
  type AlertSilenceWrite,
  type NotificationChannelWrite,
} from "@/lib/api/alerting";
import { liveFallback } from "@/lib/live/status-store";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";

export function useAlertRules(clusterId?: string) {
  return useQuery({
    queryKey: queryKeys.alerting.rules(clusterId),
    queryFn: ({ signal }) => getAlertRules({ clusterId, limit: 200 }, signal),
    refetchInterval: liveFallback(30000),
  });
}

export function useCreateAlertRule() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: AlertRuleWrite) => createAlertRule(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.all });
      toastSuccess("Alert rule created");
    },
    onError: (error: Error) => {
      toastApiError("Failed to create alert rule", error);
    },
  });
}

export function useUpdateAlertRule() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: AlertRuleWrite }) =>
      updateAlertRule(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.all });
      toastSuccess("Alert rule updated");
    },
    onError: (error: Error) => {
      toastApiError("Failed to update alert rule", error);
    },
  });
}

export function useDeleteAlertRule() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteAlertRule(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.all });
      toastSuccess("Alert rule deleted");
    },
    onError: (error: Error) => {
      toastApiError("Failed to delete alert rule", error);
    },
  });
}

export function useAlertEvents(params?: AlertEventQuery) {
  return useQuery({
    queryKey: queryKeys.alerting.events(params),
    queryFn: ({ signal }) => getAlertEvents(params, signal),
    refetchInterval: liveFallback(15000),
  });
}

export function useAlertEventSummary(clusterId?: string) {
  return useQuery({
    queryKey: queryKeys.alerting.eventSummary(clusterId),
    queryFn: ({ signal }) => getAlertEventSummary(clusterId, signal),
    refetchInterval: liveFallback(15000),
  });
}

export function useAcknowledgeAlert() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => acknowledgeAlert(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.all });
      toastSuccess("Alert acknowledged");
    },
    onError: (error: Error) => {
      toastApiError("Failed to acknowledge alert", error);
    },
  });
}

export function useResolveAlert() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => resolveAlert(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.all });
      toastSuccess("Alert resolved");
    },
    onError: (error: Error) => {
      toastApiError("Failed to resolve alert", error);
    },
  });
}

export function useNotificationChannels() {
  return useQuery({
    queryKey: queryKeys.alerting.channels,
    queryFn: ({ signal }) => getNotificationChannels(undefined, signal),
  });
}

export function useCreateNotificationChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: NotificationChannelWrite) =>
      createNotificationChannel(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.channels });
      toastSuccess("Notification channel created");
    },
    onError: (error: Error) => {
      toastApiError("Failed to create notification channel", error);
    },
  });
}

export function useTestNotificationChannel() {
  return useMutation({
    mutationFn: (id: string) => testNotificationChannel(id),
    onSuccess: (data) => {
      if (data.success) toastSuccess("Test notification sent successfully");
      else toastApiError("Test failed", data.message);
    },
    onError: (error: Error) => {
      toastApiError("Test failed", error);
    },
  });
}

export function useAlertSilences() {
  return useQuery({
    queryKey: queryKeys.alerting.silences,
    queryFn: ({ signal }) => getAlertSilences(undefined, signal),
    refetchInterval: liveFallback(30000),
  });
}

export function useCreateAlertSilence() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: AlertSilenceWrite) => createAlertSilence(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.silences });
      toastSuccess("Silence created");
    },
    onError: (error: Error) => {
      toastApiError("Failed to create silence", error);
    },
  });
}

export function useAnomalyBaselines(params?: {
  clusterId?: string;
  limit?: number;
  offset?: number;
}) {
  return useQuery({
    queryKey: queryKeys.anomalyBaselines.list(params),
    queryFn: ({ signal }) => getAnomalyBaselines(params, signal),
    refetchInterval: liveFallback(30000),
  });
}

export function useAnomalyBaseline(id: string) {
  return useQuery({
    queryKey: queryKeys.anomalyBaselines.detail(id),
    queryFn: ({ signal }) => getAnomalyBaseline(id, signal),
    enabled: Boolean(id),
  });
}
