/**
 * Build-time / runtime environment shim (replaces the dead Next.js public env vars).
 *
 * Same-origin relative defaults are the design: there is nothing to configure
 * at runtime, so there are no .env files and no VITE_* vars.
 */

declare const __APP_VERSION__: string | undefined; // vite define; absent under vitest config without define — typeof-guarded
export const APP_VERSION = typeof __APP_VERSION__ === 'undefined' ? '0.3.0-dev' : __APP_VERSION__;
export const API_BASE = '/api/v1';
export function wsBase(): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${proto}//${window.location.host}/api/v1/ws`;
}
export const IS_DEV = import.meta.env.DEV;
