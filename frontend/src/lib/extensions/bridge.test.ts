// §BridgeProtocol — tests for the pure bridge logic: the strict inbound origin/
// source/discriminator gate, handshake acceptance, navigation guard, the
// dataSource allowlist, and the host->iframe message builders.

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
  BRIDGE_PROTOCOL_VERSION,
  MAX_INBOUND_PAYLOAD_BYTES,
  MIN_IFRAME_HEIGHT,
  MAX_IFRAME_HEIGHT,
  type BridgeMsg,
} from './bridge';

const ORIGIN = 'https://ext-cost.sandbox.astronomer.local';
const EXT = 'cost-insights';
const MOUNT = 'ClusterCostTab';

// A stand-in for the mounted iframe's contentWindow (identity is what matters).
const IFRAME_WIN = { name: 'iframe' } as unknown;

function guard(over: Partial<Parameters<typeof validateInbound>[1]> = {}) {
  return {
    expectedSource: IFRAME_WIN,
    expectedOrigin: ORIGIN,
    extensionName: EXT,
    ...over,
  };
}

function bridgeMsg(over: Partial<BridgeMsg> = {}): BridgeMsg {
  return {
    astronomerBridge: true,
    v: BRIDGE_PROTOCOL_VERSION,
    ext: EXT,
    mount: MOUNT,
    type: 'ext/ready',
    ...over,
  } as BridgeMsg;
}

describe('validateInbound — origin/source/discriminator gate', () => {
  it('accepts a well-formed message from the mounted iframe at the exact origin', () => {
    const r = validateInbound(
      { source: IFRAME_WIN, origin: ORIGIN, data: bridgeMsg({ type: 'ext/ready' }) },
      guard(),
    );
    expect(r.ok).toBe(true);
    expect(r.msg?.type).toBe('ext/ready');
  });

  it('rejects a message from a different window (source mismatch)', () => {
    const r = validateInbound(
      { source: { other: true }, origin: ORIGIN, data: bridgeMsg() },
      guard(),
    );
    expect(r.ok).toBe(false);
    expect(r.reason).toBe('source-mismatch');
  });

  it('rejects a message from a different origin (origin mismatch)', () => {
    const r = validateInbound(
      { source: IFRAME_WIN, origin: 'https://evil.example', data: bridgeMsg() },
      guard(),
    );
    expect(r.ok).toBe(false);
    expect(r.reason).toBe('origin-mismatch');
  });

  it('rejects the opaque "null" origin a sandboxed frame reports (no exact match)', () => {
    const r = validateInbound(
      { source: IFRAME_WIN, origin: 'null', data: bridgeMsg() },
      guard(),
    );
    expect(r.ok).toBe(false);
    expect(r.reason).toBe('origin-mismatch');
  });

  it('refuses to trust a wildcard expected origin', () => {
    const r = validateInbound(
      { source: IFRAME_WIN, origin: ORIGIN, data: bridgeMsg() },
      guard({ expectedOrigin: '*' }),
    );
    expect(r.ok).toBe(false);
    expect(r.reason).toBe('wildcard-origin');
  });

  it('drops a message lacking the astronomerBridge discriminator', () => {
    const r = validateInbound(
      { source: IFRAME_WIN, origin: ORIGIN, data: { v: 1, type: 'ext/ready', ext: EXT, mount: MOUNT } },
      guard(),
    );
    expect(r.ok).toBe(false);
    expect(r.reason).toBe('not-bridge');
  });

  it('drops a non-object payload', () => {
    const r = validateInbound({ source: IFRAME_WIN, origin: ORIGIN, data: 'hello' }, guard());
    expect(r.ok).toBe(false);
    expect(r.reason).toBe('not-bridge');
  });

  it('rejects a wrong protocol version', () => {
    const r = validateInbound(
      { source: IFRAME_WIN, origin: ORIGIN, data: bridgeMsg({ v: 2 as 1 }) },
      guard(),
    );
    expect(r.ok).toBe(false);
    expect(r.reason).toBe('bad-version');
  });

  it('rejects a message whose ext does not match the mounted extension', () => {
    const r = validateInbound(
      { source: IFRAME_WIN, origin: ORIGIN, data: bridgeMsg({ ext: 'other-ext' }) },
      guard(),
    );
    expect(r.ok).toBe(false);
    expect(r.reason).toBe('ext-mismatch');
  });

  it('rejects a non-string correlation id', () => {
    const r = validateInbound(
      { source: IFRAME_WIN, origin: ORIGIN, data: bridgeMsg({ id: 5 as unknown as string }) },
      guard(),
    );
    expect(r.ok).toBe(false);
    expect(r.reason).toBe('malformed');
  });

  it('drops an oversize payload (bridge DoS guard)', () => {
    const big = 'x'.repeat(MAX_INBOUND_PAYLOAD_BYTES + 10);
    const r = validateInbound(
      { source: IFRAME_WIN, origin: ORIGIN, data: bridgeMsg({ payload: { big } }) },
      guard(),
    );
    expect(r.ok).toBe(false);
    expect(r.reason).toBe('oversize');
  });
});

describe('evaluateHandshake', () => {
  it('accepts when the manifestSha matches and the protocol is supported', () => {
    const r = evaluateHandshake(
      { manifestSha: 'sha256:abc', acceptsProtocol: [1] },
      { manifestSha: 'sha256:abc' },
    );
    expect(r.ok).toBe(true);
  });

  it('rejects a mismatched manifestSha (bundle built against a different manifest)', () => {
    const r = evaluateHandshake(
      { manifestSha: 'sha256:zzz', acceptsProtocol: [1] },
      { manifestSha: 'sha256:abc' },
    );
    expect(r.ok).toBe(false);
    expect(r.reason).toBe('manifest-mismatch');
  });

  it('rejects when the iframe does not accept the host bridge version', () => {
    const r = evaluateHandshake(
      { manifestSha: 'sha256:abc', acceptsProtocol: [2, 3] },
      { manifestSha: 'sha256:abc' },
    );
    expect(r.ok).toBe(false);
    expect(r.reason).toBe('protocol-unsupported');
  });

  it('rejects a missing payload', () => {
    const r = evaluateHandshake(undefined, { manifestSha: 'sha256:abc' });
    expect(r.ok).toBe(false);
    expect(r.reason).toBe('manifest-mismatch');
  });
});

describe('validateNavigation — /dashboard/ allowlist', () => {
  it('accepts a /dashboard/ path', () => {
    expect(validateNavigation({ to: '/dashboard/clusters' })).toEqual({
      ok: true,
      to: '/dashboard/clusters',
    });
  });

  it('substitutes {param} placeholders and encodes them', () => {
    const r = validateNavigation({ to: '/dashboard/clusters/{id}', params: { id: 'a b' } });
    expect(r).toEqual({ ok: true, to: '/dashboard/clusters/a%20b' });
  });

  it('rejects a path outside /dashboard/', () => {
    expect(validateNavigation({ to: '/admin/secrets' }).ok).toBe(false);
  });

  it('rejects an absolute URL with a scheme', () => {
    expect(validateNavigation({ to: 'https://evil.example' }).reason).toBe('has-scheme');
    expect(validateNavigation({ to: 'javascript:alert(1)' }).reason).toBe('has-scheme');
  });

  it('rejects a protocol-relative URL', () => {
    expect(validateNavigation({ to: '//evil.example/dashboard/' }).reason).toBe('protocol-relative');
  });

  it('rejects path traversal', () => {
    expect(validateNavigation({ to: '/dashboard/../admin' }).reason).toBe('traversal');
  });

  it('rejects when a referenced placeholder has no supplied value', () => {
    expect(validateNavigation({ to: '/dashboard/clusters/{id}' }).reason).toBe('unknown-placeholder');
  });
});

describe('clampHeight', () => {
  it('clamps to the host min/max and rounds', () => {
    expect(clampHeight(10)).toBe(MIN_IFRAME_HEIGHT);
    expect(clampHeight(999999)).toBe(MAX_IFRAME_HEIGHT);
    expect(clampHeight(640.6)).toBe(641);
  });
  it('rejects non-finite/non-number', () => {
    expect(clampHeight(NaN)).toBeNull();
    expect(clampHeight('640' as unknown as number)).toBeNull();
  });
});

describe('isAllowedDataSource — handshake allowlist', () => {
  it('accepts an id in the allowlist', () => {
    expect(isAllowedDataSource('podCost', ['podCost', 'other'])).toBe(true);
  });
  it('rejects an id not in the allowlist', () => {
    expect(isAllowedDataSource('secrets', ['podCost'])).toBe(false);
    expect(isAllowedDataSource(undefined, ['podCost'])).toBe(false);
  });
});

describe('host -> iframe builders', () => {
  it('buildHello carries the handshake allowlist as ids only (no upstream paths)', () => {
    const msg = buildHello({
      ext: EXT,
      mount: MOUNT,
      version: '0.2.0',
      manifestSha: 'sha256:abc',
      hostOrigin: 'https://app.astronomer.local',
      point: 'clusterTab',
      component: 'ClusterCostTab',
      context: { clusterId: 'c1' },
      dataSources: [{ id: 'podCost', shape: 'list' }],
      theme: { mode: 'dark', tokens: { '--primary': '#fff' } },
    });
    expect(msg.type).toBe('host/hello');
    expect(msg.astronomerBridge).toBe(true);
    expect(msg.v).toBe(1);
    expect(msg.payload.dataSources).toEqual(['podCost']);
    // The id-only projection must NOT leak path/rbac/proxy fields.
    expect(JSON.stringify(msg.payload.dataSources)).not.toMatch(/path|rbac|proxy/);
    expect(msg.payload.hostOrigin).toBe('https://app.astronomer.local');
    expect(msg.payload.capabilities).toContain('data');
  });

  it('buildDataResponse correlates the response to the request id (ok + deny)', () => {
    const ok = buildDataResponse(EXT, MOUNT, 'r2', { ok: true, data: { rows: [] }, shape: 'list' });
    expect(ok.id).toBe('r2');
    expect(ok.type).toBe('host/data.response');
    expect(ok.payload.ok).toBe(true);

    const deny = buildDataResponse(EXT, MOUNT, 'r2', {
      ok: false,
      error: { code: 'extension_rbac_denied' },
    });
    expect(deny.payload.ok).toBe(false);
    expect(deny.payload.error?.code).toBe('extension_rbac_denied');
  });

  it('buildTokenGrant carries the opaque ticket + scope under the correlation id', () => {
    const g = buildTokenGrant(EXT, MOUNT, 'r1', {
      token: 'opaque',
      dataSource: 'podCost',
      expiresAt: '2026-06-25T00:00:60Z',
      scope: 'ext:cost-insights:data:podCost',
    });
    expect(g.id).toBe('r1');
    expect(g.type).toBe('host/token.grant');
    expect(g.payload.token).toBe('opaque');
  });

  it('buildTheme and buildTeardown shape the envelope without an id', () => {
    const t = buildTheme(EXT, MOUNT, { mode: 'light', tokens: {} });
    expect(t.type).toBe('host/theme');
    expect(t.id).toBeUndefined();
    const td = buildTeardown(EXT, MOUNT);
    expect(td.type).toBe('host/teardown');
  });
});
