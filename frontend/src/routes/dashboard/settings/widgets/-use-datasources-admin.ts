import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAppForm } from "@/lib/form";
import {
  listDatasources,
  createDatasource,
  deleteDatasource,
  testDatasource,
  type DashboardDatasource,
} from "@/lib/api/dashboards";
import {
  DATASOURCES_KEY,
  onMutationFail,
  onMutationOk,
} from "./-widget-defaults";

export function useDatasourcesAdmin() {
  const queryClient = useQueryClient();
  const [dsError, setDsError] = useState<string | null>(null);
  const [testStatus, setTestStatus] = useState<
    Record<string, { ok: boolean; msg: string }>
  >({});
  const [deleteTarget, setDeleteTarget] = useState<DashboardDatasource | null>(
    null,
  );
  const [showAdd, setShowAdd] = useState(false);

  const datasourcesQuery = useQuery({
    queryKey: DATASOURCES_KEY,
    queryFn: listDatasources,
  });
  const datasources = datasourcesQuery.data ?? [];

  const invalidateDatasources = () =>
    queryClient.invalidateQueries({ queryKey: DATASOURCES_KEY });

  const createMutation = useMutation({
    mutationFn: (body: {
      name: string;
      url: string;
      bearer_token: string;
      enabled: boolean;
    }) => createDatasource(body),
    onSuccess: onMutationOk(invalidateDatasources, "Data source created"),
    onError: onMutationFail("Create data source failed"),
  });
  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteDatasource(id),
    onSuccess: onMutationOk(invalidateDatasources, "Data source deleted"),
    onError: onMutationFail("Delete data source failed"),
  });

  const addForm = useAppForm({
    defaultValues: { name: "", url: "", bearer: "" },
  });

  const addDS = async () => {
    setDsError(null);
    const { name, url, bearer } = addForm.state.values;
    try {
      await createMutation.mutateAsync({
        name,
        url,
        bearer_token: bearer,
        enabled: true,
      });
      addForm.reset();
      setShowAdd(false);
    } catch (e) {
      setDsError(e instanceof Error ? e.message : String(e));
    }
  };

  const cancelAdd = () => {
    setShowAdd(false);
    addForm.reset();
  };

  const runTest = async (id: string) => {
    try {
      const out = await testDatasource(id);
      setTestStatus((s) => ({ ...s, [id]: { ok: out.ok, msg: out.message } }));
    } catch (e) {
      setTestStatus((s) => ({
        ...s,
        [id]: { ok: false, msg: e instanceof Error ? e.message : String(e) },
      }));
    }
  };

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    try {
      await deleteMutation.mutateAsync(deleteTarget.id);
    } catch (e) {
      setDsError(e instanceof Error ? e.message : String(e));
    }
    setDeleteTarget(null);
  };

  return {
    datasourcesQuery,
    datasources,
    dsError,
    testStatus,
    showAdd,
    setShowAdd,
    addForm,
    addDS,
    cancelAdd,
    runTest,
    deleteTarget,
    setDeleteTarget,
    confirmDelete,
    deleting: deleteMutation.isPending,
  };
}

export type DatasourcesAdmin = ReturnType<typeof useDatasourcesAdmin>;
