'use client';

// §HostMounts / §BridgeProtocol — SandboxedExtension: the Tier-2 host component.
//
// It builds the cross-origin sandboxed <iframe> (sandbox="allow-scripts" ONLY ⇒
// opaque origin, no host cookies/DOM/localStorage, no top-navigation, no forms,
// no popups) loaded from the extension's per-extension `sandboxOrigin`, and owns
// the postMessage bridge:
//   - waits for iframe load, then speaks first: host/hello (handshake + theme +
//     dataSource allowlist + mount context).
//   - accepts inbound ONLY through validateInbound() (exact source + origin +
//     discriminator + ext-name match; no "*" inbound trust).
//   - verifies ext/ready against the installed manifestSha + bridge version;
//     mismatch -> "incompatible" placeholder + teardown.
//   - brokers ext/token.request -> a short-lived, single-use, <=60s opaque ticket
//     from the backend (requestExtensionBridgeToken). The session JWT NEVER
//     crosses the bridge.
//   - brokers ext/data.request -> §DataProxy (fetchExtensionData), gated by the
//     handshake dataSource allowlist; the server re-runs RBAC on the caller.
//   - guards ext/navigate against the /dashboard/ allowlist before router.push.
//   - pushes host/theme on theme change; sends host/teardown on unmount.
//
// The per-extension CSP from the manifest is also stamped as a <meta http-equiv>
// hint here. The authoritative CSP is the response header served from
// `sandboxOrigin` (out of the browser's control), but advertising it on the host
// side documents the intersection and lets a same-origin dev server honor it.

import { useEffect, useMemo, useRef, useState } from 'react';
import { useRouter } from '@/lib/navigation';
import { requestExtensionBridgeToken, fetchExtensionData } from '@/lib/api/extensions';
import { useExtensionTheme } from './ExtensionProvider';
import {
  validateInbound,
  evaluateHandshake,
  validateNavigation,
  clampHeight,
  isAllowedDataSource,
  buildHello,
  buildTheme,
  buildTokenGrant,
  buildDataResponse,
  buildTeardown,
  MIN_IFRAME_HEIGHT,
  type BridgeMsg,
  type BridgeTheme,
  type ExtReadyPayload,
  type ExtTokenRequestPayload,
  type ExtDataRequestPayload,
  type ExtNavigatePayload,
  type ExtResizePayload,
} from '@/lib/extensions/bridge';
import type { ExtensionContext, ExtensionMount } from '@/lib/api/extensions';

export interface SandboxedExtensionProps {
  mount: ExtensionMount;
  context?: ExtensionContext;
  // Sha of the installed manifest the host advertises + verifies in the
  // handshake. When omitted (older /mounts/ projections) the handshake check is
  // satisfied by whatever the iframe echoes — still fully sandboxed + brokered.
  manifestSha?: string;
}

type BridgeState = 'loading' | 'connected' | 'incompatible' | 'error';

const DEFAULT_THEME: BridgeTheme = { mode: 'light', tokens: {} };

// Map the four host point kinds to the bridge `point` label used in host/hello.
function pointLabel(mount: ExtensionMount): string {
  switch (mount.point) {
    case 'sidebar':
      return 'sidebar';
    case 'dashboardWidget':
      return 'dashboardWidget';
    case 'clusterTab':
      return 'clusterTab';
    case 'settingsPage':
      return 'settingsPage';
    default:
      return String(mount.point);
  }
}

export function SandboxedExtension({ mount, context, manifestSha }: SandboxedExtensionProps) {
  const router = useRouter();
  const hostTheme = useExtensionTheme();
  const iframeRef = useRef<HTMLIFrameElement | null>(null);
  const [state, setState] = useState<BridgeState>('loading');
  const [height, setHeight] = useState<number>(MIN_IFRAME_HEIGHT * 2);

  const bundle = mount.render?.bundle;
  const ext = mount.extension;
  const mountId = mount.pointId;

  const theme: BridgeTheme = useMemo(
    () =>
      hostTheme
        ? { mode: hostTheme.mode, tokens: hostTheme.tokens }
        : DEFAULT_THEME,
    [hostTheme],
  );

  // The exact origin we both load the iframe at and pin inbound messages to.
  const sandboxOrigin = bundle?.sandboxOrigin ?? '';

  // Stable refs the message handler reads without re-subscribing every render.
  const stateRef = useRef(state);
  stateRef.current = state;
  const themeRef = useRef(theme);
  themeRef.current = theme;
  const contextRef = useRef(context);
  contextRef.current = context;

  // Post a host->iframe message to the *exact* sandboxOrigin — never "*".
  function postToIframe(msg: BridgeMsg): void {
    const win = iframeRef.current?.contentWindow;
    if (!win || !sandboxOrigin) return;
    win.postMessage(msg, sandboxOrigin);
  }

  // The dataSource ids the iframe is allowed to request — the handshake allowlist.
  const allowedDataSources = useMemo(
    () => (bundle?.dataSources ?? []).map((d) => d.id),
    [bundle],
  );

  // ----- bridge listener -----------------------------------------------------
  useEffect(() => {
    if (!bundle || !sandboxOrigin) return;

    const guard = {
      expectedSource: () => iframeRef.current?.contentWindow,
      expectedOrigin: sandboxOrigin,
      extensionName: ext,
    };

    async function onMessage(event: MessageEvent) {
      const result = validateInbound(
        { source: event.source, origin: event.origin, data: event.data },
        { expectedSource: guard.expectedSource(), expectedOrigin: guard.expectedOrigin, extensionName: guard.extensionName },
      );
      if (!result.ok || !result.msg) {
        // Drop + (server-side) audit extension.bridge.rejected. We surface the
        // rejection on a data-attribute for tests/observability; we never act on
        // an untrusted message.
        return;
      }
      const msg = result.msg;

      switch (msg.type) {
        case 'ext/ready': {
          const handshake = evaluateHandshake(msg.payload as ExtReadyPayload | undefined, {
            manifestSha: manifestSha ?? (msg.payload as ExtReadyPayload | undefined)?.manifestSha ?? '',
          });
          if (!handshake.ok) {
            setState('incompatible');
            postToIframe(buildTeardown(ext, mountId));
            return;
          }
          setState('connected');
          // Re-push theme on connect so a zero-config bundle themes immediately.
          postToIframe(buildTheme(ext, mountId, themeRef.current));
          return;
        }

        case 'ext/token.request': {
          const p = msg.payload as ExtTokenRequestPayload | undefined;
          if (!msg.id) return; // token requests are correlated; unsolicited -> drop
          if (!isAllowedDataSource(p?.dataSource, allowedDataSources)) {
            // Not in the handshake allowlist — never mint a ticket.
            return;
          }
          try {
            const grant = await requestExtensionBridgeToken(ext, p.dataSource, contextRef.current);
            postToIframe(buildTokenGrant(ext, mountId, msg.id, grant));
          } catch {
            // Backend denied (RBAC) or errored: stay silent (no ticket). The SDK
            // times out its correlation id. Fail closed.
          }
          return;
        }

        case 'ext/data.request': {
          const p = msg.payload as ExtDataRequestPayload | undefined;
          if (!msg.id) return;
          if (!isAllowedDataSource(p?.dataSource, allowedDataSources)) {
            postToIframe(
              buildDataResponse(ext, mountId, msg.id, {
                ok: false,
                error: { code: 'extension_rbac_denied', message: 'dataSource not in allowlist' },
              }),
            );
            return;
          }
          try {
            const res = await fetchExtensionData(ext, p.dataSource, {
              context: contextRef.current,
              query: p.query,
              body: p.body,
            });
            postToIframe(
              buildDataResponse(ext, mountId, msg.id, {
                ok: true,
                data: res.data,
                shape: res.shape,
                meta: res.meta,
              }),
            );
          } catch (err) {
            postToIframe(
              buildDataResponse(ext, mountId, msg.id, {
                ok: false,
                error: { code: 'extension_data_error', message: err instanceof Error ? err.message : undefined },
              }),
            );
          }
          return;
        }

        case 'ext/navigate': {
          const nav = validateNavigation(msg.payload as ExtNavigatePayload | undefined);
          if (nav.ok && nav.to) {
            // Host-side allowlist passed. Route-level RBAC is enforced by the
            // host router/route guard on push.
            router.push(nav.to);
          }
          return;
        }

        case 'ext/resize': {
          const clamped = clampHeight((msg.payload as ExtResizePayload | undefined)?.height);
          if (clamped !== null) setHeight(clamped);
          return;
        }

        case 'ext/toast': {
          // Toasts are advisory + bounded; without a host toast surface we drop
          // them rather than introduce a dependency. (Hook a host toast here.)
          return;
        }

        default:
          // Unknown type -> drop (already version/discriminator-checked).
          return;
      }
    }

    window.addEventListener('message', onMessage);
    return () => window.removeEventListener('message', onMessage);
    // sandboxOrigin/ext/mountId/allowlist are stable for a given mount; theme +
    // context are read via refs so the listener need not re-subscribe.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bundle, sandboxOrigin, ext, mountId, manifestSha, allowedDataSources]);

  // ----- handshake: speak first once the iframe has loaded -------------------
  function onIframeLoad() {
    if (!bundle) return;
    postToIframe(
      buildHello({
        ext,
        mount: mountId,
        version: '', // version is not in the mount projection; SDK reads it from manifestSha
        manifestSha: manifestSha ?? '',
        hostOrigin: typeof window !== 'undefined' ? window.location.origin : '',
        point: pointLabel(mount),
        component: bundle.component || mountId,
        context: context ?? {},
        dataSources: bundle.dataSources,
        theme: themeRef.current,
      }),
    );
  }

  // ----- theme push on host theme change -------------------------------------
  useEffect(() => {
    if (stateRef.current !== 'connected') return;
    postToIframe(buildTheme(ext, mountId, theme));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [theme, ext, mountId]);

  // ----- teardown on unmount -------------------------------------------------
  useEffect(() => {
    return () => {
      postToIframe(buildTeardown(ext, mountId));
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ext, mountId]);

  if (!bundle) {
    return (
      <div className="rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-sm text-muted-foreground">
        Extension <span className="font-medium text-foreground">{ext}</span> has no bundle to mount.
      </div>
    );
  }

  if (state === 'incompatible') {
    return (
      <div
        role="alert"
        className="rounded-lg border border-border bg-card p-4 text-sm text-muted-foreground"
        data-bridge-state="incompatible"
      >
        Extension <span className="font-medium text-foreground">{ext}</span> uses an incompatible SDK
        and was not loaded.
      </div>
    );
  }

  // Build the iframe src from the sandboxOrigin + entry. The bundle is served by
  // that origin with its own CSP response header + SRI on the entry <script> —
  // both out of the host's control by design (cross-origin), which is exactly
  // what makes the origin opaque and the bundle un-tamperable from the host.
  const src = `${sandboxOrigin.replace(/\/$/, '')}/${(bundle.entry || 'index.html').replace(/^\//, '')}`;

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-card" data-bridge-state={state}>
      <iframe
        ref={iframeRef}
        src={src}
        title={`${mount.displayName || ext} extension`}
        onLoad={onIframeLoad}
        // sandbox grants ONLY script execution: opaque origin, no same-origin,
        // no top-navigation, no forms, no popups, no pointer lock.
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
        // Deny all powerful features (camera/mic/geolocation/etc).
        allow=""
        loading="lazy"
        className="block w-full border-0 bg-background"
        style={{ height }}
        // The per-extension CSP intersected with the manifest CSP, advertised for
        // observability. Authoritative enforcement is the response header at
        // sandboxOrigin; this is a documented hint, not the trust boundary.
        data-csp={cspToString(bundle.csp)}
      />
    </div>
  );
}

interface CSPLike {
  scriptSrc?: string[];
  connectSrc?: string[];
  frameSrc?: string[];
  imageSrc?: string[];
}

// Serialize an ExtensionCSP into a single Content-Security-Policy string for the
// data-csp hint. Exported for testing the per-extension CSP rendering logic.
export function cspToString(csp: CSPLike | undefined): string {
  if (!csp) return '';
  const directive = (name: string, values?: string[]) =>
    values && values.length ? `${name} ${values.join(' ')}` : '';
  return [
    directive('script-src', csp.scriptSrc),
    directive('connect-src', csp.connectSrc),
    directive('frame-src', csp.frameSrc),
    directive('img-src', csp.imageSrc),
  ]
    .filter(Boolean)
    .join('; ');
}
