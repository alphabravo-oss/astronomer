import {
  deleteAuthMeTableViewsById,
  getAuthMeTableViews,
  patchAuthMeTableViewsById,
  postAuthMeTableViews,
} from "@/lib/api/generated/client";
import type {
  TableView,
  TableViewState,
  TableViewUpdateRequest,
} from "@/types/openapi.generated";

export type { TableView, TableViewState };

/** Server-enforced cap, mirrored here so the UI can explain it before a 409. */
export const MAX_TABLE_VIEWS = 20;

export async function listTableViews(
  tableKey: string,
  signal?: AbortSignal,
): Promise<TableView[]> {
  const response = await getAuthMeTableViews({
    query: { table_key: tableKey },
    signal,
  });
  return response.data;
}

export async function createTableView(input: {
  tableKey: string;
  name: string;
  state: TableViewState;
}): Promise<TableView> {
  const response = await postAuthMeTableViews({
    body: { table_key: input.tableKey, name: input.name, state: input.state },
  });
  return response.data;
}

export async function updateTableView(
  id: string,
  patch: TableViewUpdateRequest,
): Promise<TableView> {
  const response = await patchAuthMeTableViewsById({
    path: { id },
    body: patch,
  });
  return response.data;
}

export async function deleteTableView(id: string): Promise<void> {
  await deleteAuthMeTableViewsById({ path: { id } });
}
