// §BridgeProtocol — Tier-2 iframe postMessage bridge (pure logic).
//
// This module holds the protocol envelope, the strict inbound origin/source/
// discriminator checks, and the host->iframe message builders. It is deliberately
// React- and DOM-free so the security-critical predicates (origin rejection,
// handshake acceptance, data req/resp correlation) are unit-testable in
// isolation. SandboxedExtension.tsx wires these into a real window `message`
// listener and an <iframe>.
//
// Invariants enforced here (design doc §BridgeProtocol "Origin checks"):
//   - inbound is trusted ONLY when event.source === the mounted iframe's window
//     && event.origin === the exact sandboxOrigin && msg.astronomerBridge === true
//     && msg.v === 1 && msg.ext === the mounted extension. No "*" inbound trust.
//   - the iframe NEVER receives the session JWT; data flows via §DataProxy using
//     an opaque, single-use, <=60s ticket the host brokers on request.
//   - ext/navigate only routes within the host "/dashboard/" allowlist; the
//     iframe (sandbox="allow-scripts", no allow-top-navigation) cannot self-nav.

import type {
  ExtensionContext,
  ExtensionDataResponse,
  ExtensionDataSourceMeta,
} from '@/lib/api/extensions';

export const BRIDGE_PROTOCOL_VERSION = 1 as const;

// Hard payload cap (bytes, post-serialization) — oversize inbound is dropped.
// A flood/oversize is the bridge-DoS vector the design doc calls out.
export const MAX_INBOUND_PAYLOAD_BYTES = 256 * 1024;

// Host -> iframe message types.
export type HostMessageType =
  | 'host/hello'
  | 'host/theme'
  | 'host/token.grant'
  | 'host/data.response'
  | 'host/teardown';

// iframe -> host message types.
export type ExtMessageType =
  | 'ext/ready'
  | 'ext/token.request'
  | 'ext/data.request'
  | 'ext/navigate'
  | 'ext/resize'
  | 'ext/toast';

export type BridgeMessageType = HostMessageType | ExtMessageType;

// The wire envelope. `astronomerBridge` is the discriminator — any message
// lacking it (e.g. unrelated postMessage traffic, third-party SDKs) is dropped.
export interface BridgeMsg<P = unknown> {
  astronomerBridge: true;
  v: typeof BRIDGE_PROTOCOL_VERSION;
  ext: string;
  mount: string;
  type: string;
  id?: string;
  payload?: P;
}

// ---- Payload shapes (host -> iframe) --------------------------------------

export interface HostHelloPayload {
  hostOrigin: string;
  extension: { name: string; version: string; manifestSha: string };
  mount: {
    point: string;
    component: string;
    context: ExtensionContext;
  };
  capabilities: string[];
  dataSources: string[]; // ids the iframe may request — the handshake allowlist
  theme: BridgeTheme;
}

export interface BridgeTheme {
  mode: 'light' | 'dark';
  tokens: Record<string, string>;
}

export interface HostTokenGrantPayload {
  token: string;
  dataSource: string;
  expiresAt: string;
  scope: string;
}

export interface HostDataResponsePayload<T = unknown> {
  ok: boolean;
  data?: T;
  shape?: ExtensionDataResponse['shape'];
  meta?: ExtensionDataResponse['meta'];
  error?: { code: string; message?: string };
}

// ---- Payload shapes (iframe -> host) --------------------------------------

export interface ExtReadyPayload {
  sdkVersion?: string;
  acceptsProtocol?: number[];
  manifestSha?: string;
}

export interface ExtTokenRequestPayload {
  dataSource: string;
}

export interface ExtDataRequestPayload {
  dataSource: string;
  query?: Record<string, string>;
  body?: unknown;
}

export interface ExtNavigatePayload {
  to: string;
  params?: Record<string, string>;
}

export interface ExtResizePayload {
  height: number;
}

export interface ExtToastPayload {
  level: 'info' | 'success' | 'warning' | 'error' | string;
  message: string;
}

// ---------------------------------------------------------------------------
// Inbound validation — the security choke point.
// ---------------------------------------------------------------------------

// What the host must already know to trust an inbound message: which iframe
// window it pinned and which exact origin + extension name it mounted.
export interface InboundGuard {
  // The mounted iframe's contentWindow. event.source must be referentially this.
  expectedSource: unknown;
  // The exact sandboxOrigin the iframe was loaded at. event.origin must equal it.
  expectedOrigin: string;
  // The mounted extension name. msg.ext must equal it.
  extensionName: string;
}

export type RejectReason =
  | 'source-mismatch'
  | 'origin-mismatch'
  | 'wildcard-origin'
  | 'not-bridge'
  | 'bad-version'
  | 'ext-mismatch'
  | 'oversize'
  | 'malformed';

export interface InboundResult {
  ok: boolean;
  reason?: RejectReason;
  msg?: BridgeMsg;
}

// A minimal structural view of a MessageEvent so this stays DOM-free/testable.
export interface InboundEvent {
  source: unknown;
  origin: string;
  data: unknown;
}

// The non-negotiable inbound gate (design doc §Origin checks). Order matters:
// cheap identity/origin checks first, then the discriminator, then structural
// validation. A failure is a hard drop — the caller audits
// `extension.bridge.rejected` and ignores the message entirely.
export function validateInbound(event: InboundEvent, guard: InboundGuard): InboundResult {
  // event.source must be the *exact* window we postMessage'd to. This pins the
  // sender to the one iframe we mounted — a different frame/tab cannot spoof in.
  if (event.source !== guard.expectedSource) {
    return { ok: false, reason: 'source-mismatch' };
  }
  // Never accept a wildcard/empty origin, and require an exact string match to
  // the sandboxOrigin. sandbox="allow-scripts" (no allow-same-origin) yields an
  // opaque origin that browsers report as "null"; an exact-match guard against a
  // real https sandboxOrigin therefore also rejects the opaque-origin case.
  if (!guard.expectedOrigin || guard.expectedOrigin === '*') {
    return { ok: false, reason: 'wildcard-origin' };
  }
  if (event.origin !== guard.expectedOrigin) {
    return { ok: false, reason: 'origin-mismatch' };
  }

  const data = event.data;
  if (!data || typeof data !== 'object') {
    return { ok: false, reason: 'not-bridge' };
  }
  const m = data as Record<string, unknown>;
  if (m.astronomerBridge !== true) {
    return { ok: false, reason: 'not-bridge' };
  }
  if (m.v !== BRIDGE_PROTOCOL_VERSION) {
    return { ok: false, reason: 'bad-version' };
  }
  if (typeof m.type !== 'string' || typeof m.mount !== 'string') {
    return { ok: false, reason: 'malformed' };
  }
  if (m.ext !== guard.extensionName) {
    return { ok: false, reason: 'ext-mismatch' };
  }
  if (m.id !== undefined && typeof m.id !== 'string') {
    return { ok: false, reason: 'malformed' };
  }
  // Oversize guard (bridge-DoS). Cheap to compute on the payload only.
  if (m.payload !== undefined && approxByteSize(m.payload) > MAX_INBOUND_PAYLOAD_BYTES) {
    return { ok: false, reason: 'oversize' };
  }
  return { ok: true, msg: m as unknown as BridgeMsg };
}

function approxByteSize(value: unknown): number {
  try {
    // 1 char ≈ 1 byte is a fine upper-bound approximation for a cap check.
    return JSON.stringify(value)?.length ?? 0;
  } catch {
    // Unserializable (cycles/functions) — treat as oversize to fail closed.
    return MAX_INBOUND_PAYLOAD_BYTES + 1;
  }
}

// ---------------------------------------------------------------------------
// Handshake acceptance — does the iframe's ext/ready match what we installed?
// ---------------------------------------------------------------------------

export interface HandshakeExpectation {
  manifestSha: string; // the installed manifest sha the host advertised
}

export type HandshakeReject = 'manifest-mismatch' | 'protocol-unsupported';

export interface HandshakeResult {
  ok: boolean;
  reason?: HandshakeReject;
}

// The iframe must (a) echo the installed manifestSha — a different sha means the
// bundle was built against a manifest the host did not install — and (b) accept
// the host's bridge protocol version. Either miss => render the "incompatible
// SDK" placeholder and tear down (design doc handshake step 2 note).
export function evaluateHandshake(
  payload: ExtReadyPayload | undefined,
  expect: HandshakeExpectation,
): HandshakeResult {
  if (!payload || payload.manifestSha !== expect.manifestSha) {
    return { ok: false, reason: 'manifest-mismatch' };
  }
  const accepts = payload.acceptsProtocol;
  if (!Array.isArray(accepts) || !accepts.includes(BRIDGE_PROTOCOL_VERSION)) {
    return { ok: false, reason: 'protocol-unsupported' };
  }
  return { ok: true };
}

// ---------------------------------------------------------------------------
// Navigation guard — ext/navigate may only route within the host allowlist.
// ---------------------------------------------------------------------------

export interface NavResult {
  ok: boolean;
  to?: string;
  reason?: 'not-dashboard' | 'has-scheme' | 'protocol-relative' | 'traversal' | 'unknown-placeholder';
}

// Validate an ext/navigate target before the host router.push()es it. The iframe
// can request navigation but cannot perform it (sandbox denies top-nav); this is
// the host's allowlist filter. Route-level RBAC is then applied by the host
// router itself — this guard only constrains the *shape* of the target.
export function validateNavigation(payload: ExtNavigatePayload | undefined): NavResult {
  if (!payload || typeof payload.to !== 'string') return { ok: false, reason: 'not-dashboard' };
  const raw = payload.to;
  // No scheme (http:, javascript:, data:) and no protocol-relative (//evil.com).
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(raw)) return { ok: false, reason: 'has-scheme' };
  if (raw.startsWith('//')) return { ok: false, reason: 'protocol-relative' };
  if (!raw.startsWith('/dashboard/')) return { ok: false, reason: 'not-dashboard' };
  if (raw.includes('..')) return { ok: false, reason: 'traversal' };

  // Substitute {param} placeholders from params; reject any placeholder with no
  // supplied value and re-check the result is still within /dashboard/.
  const params = payload.params ?? {};
  let unknown = false;
  const to = raw.replace(/\{([^}]+)\}/g, (_, key: string) => {
    const v = params[key];
    if (v === undefined) {
      unknown = true;
      return '';
    }
    return encodeURIComponent(v);
  });
  if (unknown) return { ok: false, reason: 'unknown-placeholder' };
  if (!to.startsWith('/dashboard/') || to.includes('..')) {
    return { ok: false, reason: 'not-dashboard' };
  }
  return { ok: true, to };
}

// Clamp an ext/resize request to a sane host range (design doc: "host clamps
// min/max"). Defends against a 0px (hidden) or absurdly tall iframe.
export const MIN_IFRAME_HEIGHT = 80;
export const MAX_IFRAME_HEIGHT = 4096;
export function clampHeight(height: unknown): number | null {
  if (typeof height !== 'number' || !Number.isFinite(height)) return null;
  return Math.min(MAX_IFRAME_HEIGHT, Math.max(MIN_IFRAME_HEIGHT, Math.round(height)));
}

// ---------------------------------------------------------------------------
// Host -> iframe message builders. The host always targets the exact
// sandboxOrigin when posting (never "*") — that is enforced at the call site in
// SandboxedExtension; these builders just shape the envelope.
// ---------------------------------------------------------------------------

function envelope<P>(
  ext: string,
  mount: string,
  type: HostMessageType,
  payload?: P,
  id?: string,
): BridgeMsg<P> {
  const msg: BridgeMsg<P> = { astronomerBridge: true, v: BRIDGE_PROTOCOL_VERSION, ext, mount, type };
  if (id !== undefined) msg.id = id;
  if (payload !== undefined) msg.payload = payload;
  return msg;
}

// A host->iframe message whose payload is guaranteed present (the builders that
// always carry one return this, so test/consumer code need not null-check).
export type BridgeMsgWith<P> = BridgeMsg<P> & { payload: P };

export interface HelloArgs {
  ext: string;
  mount: string;
  version: string;
  manifestSha: string;
  hostOrigin: string;
  point: string;
  component: string;
  context: ExtensionContext;
  dataSources: ExtensionDataSourceMeta[] | undefined;
  theme: BridgeTheme;
  capabilities?: string[];
}

export function buildHello(a: HelloArgs): BridgeMsgWith<HostHelloPayload> {
  return envelope<HostHelloPayload>(a.ext, a.mount, 'host/hello', {
    hostOrigin: a.hostOrigin,
    extension: { name: a.ext, version: a.version, manifestSha: a.manifestSha },
    mount: { point: a.point, component: a.component, context: a.context },
    capabilities: a.capabilities ?? ['data', 'navigate', 'theme'],
    // Only the *ids* cross the bridge — never upstream paths/rbac (those stay
    // server-side in the stored manifest). This is the handshake allowlist.
    dataSources: (a.dataSources ?? []).map((d) => d.id),
    theme: a.theme,
  }) as BridgeMsgWith<HostHelloPayload>;
}

export function buildTheme(ext: string, mount: string, theme: BridgeTheme): BridgeMsgWith<BridgeTheme> {
  return envelope<BridgeTheme>(ext, mount, 'host/theme', theme) as BridgeMsgWith<BridgeTheme>;
}

export function buildTokenGrant(
  ext: string,
  mount: string,
  id: string,
  grant: HostTokenGrantPayload,
): BridgeMsgWith<HostTokenGrantPayload> {
  return envelope<HostTokenGrantPayload>(ext, mount, 'host/token.grant', grant, id) as BridgeMsgWith<HostTokenGrantPayload>;
}

export function buildDataResponse<T>(
  ext: string,
  mount: string,
  id: string,
  payload: HostDataResponsePayload<T>,
): BridgeMsgWith<HostDataResponsePayload<T>> {
  return envelope<HostDataResponsePayload<T>>(ext, mount, 'host/data.response', payload, id) as BridgeMsgWith<HostDataResponsePayload<T>>;
}

export function buildTeardown(ext: string, mount: string): BridgeMsg<undefined> {
  return envelope<undefined>(ext, mount, 'host/teardown');
}

// True iff `id` is in the handshake dataSources allowlist. Both ext/token.request
// and ext/data.request are rejected for any dataSource not advertised in
// host/hello (design doc: "the handshake allowlist").
export function isAllowedDataSource(id: unknown, allowlist: string[]): id is string {
  return typeof id === 'string' && allowlist.includes(id);
}
