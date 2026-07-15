/**
 * P7.1 — Runtime API stub matcher for the route-smoke crawl.
 *
 * Answers every /api/v1 request from the generated OpenAPI stubs plus the
 * hand-written overrides, so all 100+ routes render over deterministic
 * skeleton data with zero backend. Matching rules:
 *   1. overrides (method + exact path or RegExp) win,
 *   2. EventSource requests get a well-typed empty text/event-stream,
 *   3. GETs match the OpenAPI path templates, longest-literal template first
 *      (`{id}` segments become `[^/]+`),
 *   4. anything left gets a generic empty envelope.
 */
import fs from 'node:fs';
import path from 'node:path';
import type { Page } from '@playwright/test';
import { overrides, type StubOverride } from './stub-overrides';

const generatedStubs = JSON.parse(
  fs.readFileSync(path.join(__dirname, 'openapi-stubs.generated.json'), 'utf8'),
) as Record<string, unknown>;

function escapeRegExp(literal: string): string {
  return literal.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

type TemplateMatcher = { re: RegExp; body: unknown; literalLength: number };

const templateMatchers: TemplateMatcher[] = Object.entries(generatedStubs)
  .map(([template, body]) => {
    const normalized = template.replace(/\/+$/, '');
    return {
      re: new RegExp(
        `^${normalized.split(/\{[^}]+\}/).map(escapeRegExp).join('[^/]+')}$`,
      ),
      body,
      literalLength: normalized.replace(/\{[^}]+\}/g, '').length,
    };
  })
  // Longest-literal wins: /clusters/{id}/nodes/ beats /clusters/{id}/{x}/.
  .sort((a, b) => b.literalLength - a.literalLength);

/**
 * Install the full stub surface on a page. `extra` overrides are consulted
 * before the shared ones (used by the negative detector-honesty tests).
 */
export async function installStubs(page: Page, extra: StubOverride[] = []): Promise<void> {
  // Terminal/exec/watch WebSockets: accept and park them client-side so no
  // page logs a "WebSocket connection ... failed" console error.
  await page.routeWebSocket(/.*/, () => {});

  const allOverrides = [...extra, ...overrides];
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const pathname = url.pathname.replace(/\/+$/, '');
    const method = request.method();

    for (const override of allOverrides) {
      if (override.method !== method) continue;
      const hit =
        typeof override.path === 'string' ? override.path === pathname : override.path.test(pathname);
      if (!hit) continue;
      const body = typeof override.body === 'function' ? override.body(url) : override.body;
      return route.fulfill({ status: override.status ?? 200, json: body as object });
    }

    // EventSource consumers (live events stream, pod/log watches): a JSON
    // body would make Chrome log a MIME-type console error on every page
    // that mounts a watch, so answer with an immediately-ended, correctly
    // typed stream — the client treats it as a drop and backs off silently.
    if ((request.headers()['accept'] ?? '').includes('text/event-stream')) {
      return route.fulfill({ status: 200, contentType: 'text/event-stream', body: ':route-smoke\n\n' });
    }

    if (method === 'GET') {
      const matched = templateMatchers.find((m) => m.re.test(pathname));
      if (matched) return route.fulfill({ json: matched.body as object });
    }

    // Undocumented endpoint or a non-GET fired during load: empty list
    // envelope. This matches the dominant `res.data.data ?? res.data` list
    // clients; undocumented DETAIL endpoints must get an explicit override
    // (a truthy `[]` sails past their `if (!x)` guards into deref crashes).
    return route.fulfill({ json: { data: [] } });
  });
}

/**
 * Console-error allowlist. Starts — and should stay — EMPTY: every addition
 * needs a comment linking the tracking issue (plan P7.1 step 3).
 */
export const CONSOLE_ALLOWLIST: RegExp[] = [];

/** Start collecting pageerror + console.error messages for a page. */
export function collectErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on('pageerror', (err) => errors.push(`pageerror: ${err.message}`));
  page.on('console', (msg) => {
    if (msg.type() === 'error') errors.push(`console: ${msg.text()}`);
  });
  return errors;
}

/** Drop allowlisted entries; whatever remains fails the crawl. */
export function filterAllowed(errors: string[]): string[] {
  return errors.filter((entry) => !CONSOLE_ALLOWLIST.some((re) => re.test(entry)));
}
