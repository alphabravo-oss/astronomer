import { getCatalogRepositoriesById } from "./generated/client";

export async function getCatalogSupportRepository(
  id: string,
  signal?: AbortSignal,
) {
  const response = await getCatalogRepositoriesById({ path: { id }, signal });
  return response.data;
}
