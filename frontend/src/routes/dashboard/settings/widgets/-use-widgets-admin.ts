import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAppForm } from "@/lib/form";
import {
  listWidgets,
  createWidget,
  updateWidget,
  deleteWidget,
  type Widget,
  type WidgetSpec,
  type WidgetWriteBody,
} from "@/lib/api/dashboards";
import {
  WIDGETS_KEY,
  onMutationFail,
  onMutationOk,
  widgetDefaults,
  widgetToFormValues,
} from "./-widget-defaults";

export function useWidgetsAdmin() {
  const queryClient = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  // Non-null while the editor is open; carries the row id in edit mode. The
  // field values themselves live on the TanStack form below.
  const [editing, setEditing] = useState<{ id?: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Widget | null>(null);

  const widgetsQuery = useQuery({
    queryKey: WIDGETS_KEY,
    queryFn: listWidgets,
  });
  const widgets = widgetsQuery.data ?? [];
  const loading = widgetsQuery.isLoading;

  const invalidateWidgets = () =>
    queryClient.invalidateQueries({ queryKey: WIDGETS_KEY });

  const createMutation = useMutation({
    mutationFn: (body: WidgetWriteBody) => createWidget(body),
    onSuccess: onMutationOk(invalidateWidgets, "Widget created"),
    onError: onMutationFail("Create widget failed"),
  });
  const updateMutation = useMutation({
    mutationFn: ({ id, body }: { id: string; body: WidgetWriteBody }) =>
      updateWidget(id, body),
    onSuccess: onMutationOk(invalidateWidgets, "Widget updated"),
    onError: onMutationFail("Update widget failed"),
  });
  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteWidget(id),
    onSuccess: onMutationOk(invalidateWidgets, "Widget deleted"),
    onError: onMutationFail("Delete widget failed"),
  });

  const saving = createMutation.isPending || updateMutation.isPending;

  const form = useAppForm({
    defaultValues: widgetDefaults(),
    onSubmit: async ({ value }) => {
      if (!editing) return;
      let spec: WidgetSpec = {};
      try {
        spec = JSON.parse(value.specText);
      } catch {
        setError("Spec is not valid JSON");
        return;
      }
      const body: WidgetWriteBody = {
        name: value.name,
        description: value.description,
        widget_type: value.widgetType,
        spec,
        scope: value.scope,
        scope_ids: value.scopeIds,
        grid: value.grid,
        refresh_seconds: value.refreshSeconds,
        enabled: value.enabled,
      };
      try {
        if (editing.id) {
          await updateMutation.mutateAsync({ id: editing.id, body });
        } else {
          await createMutation.mutateAsync(body);
        }
        setEditing(null);
        form.reset(widgetDefaults());
        setError(null);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
  });

  const startCreate = () => {
    setEditing({});
    form.reset(widgetDefaults());
  };

  const startEdit = (w: Widget) => {
    setEditing({ id: w.id });
    form.reset(widgetToFormValues(w));
  };

  const cancel = () => {
    setEditing(null);
    form.reset(widgetDefaults());
  };

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    try {
      await deleteMutation.mutateAsync(deleteTarget.id);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
    setDeleteTarget(null);
  };

  return {
    widgetsQuery,
    widgets,
    loading,
    error,
    editing,
    form,
    saving,
    startCreate,
    startEdit,
    cancel,
    deleteTarget,
    setDeleteTarget,
    confirmDelete,
    deleting: deleteMutation.isPending,
  };
}

export type WidgetsAdmin = ReturnType<typeof useWidgetsAdmin>;
