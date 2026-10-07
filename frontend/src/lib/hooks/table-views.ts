import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@/lib/store";
import { queryKeys } from "@/lib/query-keys";
import {
  createTableView,
  deleteTableView,
  listTableViews,
  updateTableView,
  type TableView,
  type TableViewState,
} from "@/lib/api/table-views";
import type { TableViewUpdateRequest } from "@/types/openapi.generated";

/**
 * Saved views for one DataTable (`tableKey` is the normalized persist key).
 * Mutations refresh the list; the server enforces ownership, the 20-view cap
 * and unique names, and their 409/400 errors surface through `error`.
 */
export function useTableViews(tableKey: string | undefined) {
  const queryClient = useQueryClient();
  const userID = useAuthStore((state) => state.user?.id);
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated);
  const key = queryKeys.users.tableViews(userID ?? "anonymous", tableKey ?? "");
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => listTableViews(tableKey as string, signal),
    enabled: isAuthenticated && !!userID && !!tableKey,
    staleTime: 60_000,
    retry: false,
  });
  const refresh = () => queryClient.invalidateQueries({ queryKey: key });

  const create = useMutation({
    mutationFn: (input: { name: string; state: TableViewState }) =>
      createTableView({ tableKey: tableKey as string, ...input }),
    onSuccess: refresh,
    // Resync after a rejected write (409 duplicate/cap, 404 deleted elsewhere).
    onError: refresh,
  });
  const update = useMutation({
    mutationFn: ({
      id,
      patch,
    }: {
      id: string;
      patch: TableViewUpdateRequest;
    }) => updateTableView(id, patch),
    onSuccess: refresh,
    // Resync after a rejected write (409 duplicate/cap, 404 deleted elsewhere).
    onError: refresh,
  });
  const remove = useMutation({
    mutationFn: (id: string) => deleteTableView(id),
    onSuccess: refresh,
    // Resync after a rejected write (409 duplicate/cap, 404 deleted elsewhere).
    onError: refresh,
  });

  const views: TableView[] = query.data ?? [];
  return {
    views,
    defaultView: views.find((view) => view.is_default),
    isLoading: query.isLoading,
    isError: query.isError,
    create,
    update,
    remove,
  };
}
