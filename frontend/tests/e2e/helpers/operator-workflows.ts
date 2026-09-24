import type { Page, BrowserContext } from "@playwright/test";
import { installStubs } from "../../e2e-smoke/stubs";
import { seedAuth } from "./auth";
import {
  adminStoreUser,
  SMOKE_CLUSTER_ID,
} from "../../e2e-smoke/stub-overrides";
export const clusterId = SMOKE_CLUSTER_ID;
export const now = "2026-09-24T12:00:00Z";
export const pageOf = (data: unknown[]) => ({
  data,
  pagination: {
    limit: 50,
    offset: 0,
    total: data.length,
    has_more: false,
    next_offset: null,
  },
});
export async function workflowAuth(page: Page, context: BrowserContext) {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
}
export async function jsonRoute(
  page: Page,
  path: string,
  data: unknown,
  status = 200,
) {
  await page.route(
    (url) => url.pathname.replace(/\/$/, "") === path,
    (route) => route.fulfill({ status, json: data }),
  );
}
export const errorBody = (message: string) => ({
  error: { code: "FIXTURE_ERROR", message },
});
