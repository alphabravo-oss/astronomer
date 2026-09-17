/**
 * Build-time / runtime environment shim (replaces the dead Next.js public env vars).
 *
 * Same-origin relative defaults are the design: there is nothing to configure
 * at runtime, so there are no .env files and no VITE_* vars.
 */

declare const __APP_VERSION__: string | undefined; // vite define; absent under vitest config without define — typeof-guarded
declare const __BUILD_COMMIT__: string | undefined;
declare const __BUILD_DATE__: string | undefined;
declare const __BUILD_NODE_VERSION__: string | undefined;
export const APP_VERSION =
  typeof __APP_VERSION__ === "undefined" ? "development" : __APP_VERSION__;
export const BUILD_INFO = {
  version: APP_VERSION,
  commit:
    typeof __BUILD_COMMIT__ === "undefined" ? "unknown" : __BUILD_COMMIT__,
  date: typeof __BUILD_DATE__ === "undefined" ? "unknown" : __BUILD_DATE__,
  node:
    typeof __BUILD_NODE_VERSION__ === "undefined"
      ? "unknown"
      : __BUILD_NODE_VERSION__,
} as const;
export const API_BASE = "/api/v1";
export function wsBase(): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/api/v1/ws`;
}
export const IS_DEV = import.meta.env.DEV;
