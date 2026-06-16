// Hydrate empty management-plane pages with demo content for marketing
// screenshots, and tear it all back out. Everything created is name-prefixed
// `demo-` so teardown can find and delete exactly what we seeded — nothing else.
//
//   LOGIN_EMAIL=... LOGIN_PASSWORD=... node scripts/demo-seed.mjs           # seed
//   LOGIN_EMAIL=... LOGIN_PASSWORD=... node scripts/demo-seed.mjs --teardown # remove
//
// ponytail: marker is the `demo-` name prefix. Anything else named demo-* on
// this install would also be removed by --teardown; acceptable for a demo env.
const BASE = process.env.BASE_URL || 'https://astronomer.dev.alphabravo.io';
const EMAIL = process.env.LOGIN_EMAIL;
const PASSWORD = process.env.LOGIN_PASSWORD;
const PREFIX = 'demo-';
const TEARDOWN = process.argv.includes('--teardown');
if (!EMAIL || !PASSWORD) throw new Error('set LOGIN_EMAIL and LOGIN_PASSWORD');

// Node 18+ has global fetch; allow the self-signed/ingress cert.
process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';

let TOKEN = '';
async function api(method, path, body) {
  const res = await fetch(`${BASE}/api/v1${path}`, {
    method,
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${TOKEN}` },
    body: body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let json;
  try { json = text ? JSON.parse(text) : {}; } catch { json = { raw: text }; }
  if (!res.ok) throw new Error(`${method} ${path} -> ${res.status} ${text.slice(0, 300)}`);
  return json.data ?? json;
}

// Pull a list and normalize to [{id, name}], tolerating envelope shapes.
function asList(d) {
  if (Array.isArray(d)) return d;
  for (const k of ['items', 'results', 'data', 'rules', 'channels', 'projects']) {
    if (Array.isArray(d?.[k])) return d[k];
  }
  return [];
}

async function login() {
  for (let attempt = 0; attempt < 6; attempt++) {
    const r = await fetch(`${BASE}/api/v1/auth/login/`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email: EMAIL, password: PASSWORD }),
    });
    if (r.ok) { TOKEN = (await r.json()).data.token; return; }
    if (r.status === 429) { // rate-limited: back off and retry
      await new Promise((res) => setTimeout(res, 20000));
      continue;
    }
    throw new Error(`login failed: ${r.status}`);
  }
  throw new Error('login failed: still rate-limited after retries');
}

// --- entity definitions: list + delete for teardown, create payloads for seed.
// Each create payload's `name` starts with PREFIX so teardown matches it.
// `seeds` may be an array, or a fn(ctx) -> array where ctx maps a previously
// seeded item's name to its created id (used so a schedule can reference its
// storage config). Definitions seed in order, so dependencies come first.
function definitions(localClusterId) {
  return [
    {
      label: 'notification channels',
      list: '/alerting/channels/',
      del: (id) => `/alerting/channels/${id}/`,
      seeds: [
        { name: `${PREFIX}platform-slack`, channel_type: 'slack', type: 'slack', enabled: true,
          configuration: { webhook_url: 'https://hooks.slack.com/services/T000/B000/demo' } },
        { name: `${PREFIX}oncall-email`, channel_type: 'email', type: 'email', enabled: true,
          configuration: { to: 'sre@example.com' } },
        { name: `${PREFIX}pagerduty`, channel_type: 'pagerduty', type: 'pagerduty', enabled: false,
          configuration: { routing_key: 'demo-routing-key' } },
      ],
    },
    {
      label: 'alert rules',
      list: '/alerting/rules/',
      del: (id) => `/alerting/rules/${id}/`,
      seeds: [
        { name: `${PREFIX}High CPU`, description: 'Node CPU sustained above 85%', rule_type: 'metric',
          type: 'threshold', severity: 'warning', enabled: true, query: 'node_cpu_utilization', threshold: 85,
          duration: '5m', cooldown_minutes: 15 },
        { name: `${PREFIX}Memory Pressure`, description: 'Pod memory above 90% of limit', rule_type: 'metric',
          type: 'threshold', severity: 'critical', enabled: true, query: 'container_memory_utilization',
          threshold: 90, duration: '10m', cooldown_minutes: 30 },
        { name: `${PREFIX}Pod Restarts`, description: 'CrashLoopBackOff restarts climbing', rule_type: 'metric',
          type: 'threshold', severity: 'warning', enabled: true, query: 'kube_pod_container_restarts',
          threshold: 5, duration: '15m', cooldown_minutes: 20 },
        { name: `${PREFIX}Disk Almost Full`, description: 'PVC usage above 90%', rule_type: 'metric',
          type: 'threshold', severity: 'critical', enabled: true, query: 'kubelet_volume_used_percent',
          threshold: 90, duration: '5m', cooldown_minutes: 60 },
      ],
    },
    {
      label: 'projects',
      list: '/projects/',
      del: (id) => `/projects/${id}/`,
      seeds: [
        { name: `${PREFIX}payments`, display_name: 'Payments', description: 'Payment processing services',
          cluster_id: localClusterId, network_policy_mode: 'baseline' },
        { name: `${PREFIX}data-platform`, display_name: 'Data Platform', description: 'Analytics & ETL pipelines',
          cluster_id: localClusterId, network_policy_mode: 'baseline' },
        { name: `${PREFIX}web`, display_name: 'Web Frontend', description: 'Customer-facing web apps',
          cluster_id: localClusterId, network_policy_mode: 'baseline' },
      ],
    },
    {
      label: 'webhooks',
      list: '/admin/webhooks/',
      del: (id) => `/admin/webhooks/${id}/`,
      seeds: [
        { name: `${PREFIX}ci-pipeline`, url: 'https://ci.example.com/hooks/astronomer', enabled: true,
          secret: 'demo-webhook-secret-ci', event_filters: ['cluster.adopted', 'tool.installed'] },
        { name: `${PREFIX}audit-sink`, url: 'https://siem.example.com/ingest', enabled: true,
          secret: 'demo-webhook-secret-audit', event_filters: ['audit.event'] },
      ],
    },
    {
      label: 'backup storage',
      list: '/backups/storage/',
      del: (id) => `/backups/storage/${id}/`,
      seeds: [
        { name: `${PREFIX}s3-primary`, storage_type: 's3', bucket: 'astronomer-backups-prod',
          region: 'us-east-1', prefix: 'velero', is_default: true, cluster_id: localClusterId,
          access_key: 'AKIADEMO0000000', secret_key: 'demoSecretKeyDoNotUse' },
        { name: `${PREFIX}s3-dr`, storage_type: 's3', bucket: 'astronomer-backups-dr',
          region: 'us-west-2', prefix: 'velero', is_default: false, cluster_id: localClusterId,
          access_key: 'AKIADEMO1111111', secret_key: 'demoSecretKeyDoNotUse' },
      ],
    },
    {
      label: 'backup schedules',
      list: '/backups/schedules/',
      del: (id) => `/backups/schedules/${id}/`,
      // Needs a storage id; reference the primary storage seeded just above.
      // May fail if Velero isn't installed on the cluster — tolerated per-item.
      seeds: (ctx) => {
        const storageId = ctx[`${PREFIX}s3-primary`];
        if (!storageId) return [];
        return [
          { name: `${PREFIX}nightly-full`, storage_id: storageId, backup_type: 'full',
            cron_expression: '0 2 * * *', retention_count: 14, enabled: true, cluster_id: localClusterId },
          { name: `${PREFIX}hourly-config`, storage_id: storageId, backup_type: 'config',
            cron_expression: '0 * * * *', retention_count: 48, enabled: false, cluster_id: localClusterId },
        ];
      },
    },
  ];
}

async function localClusterId() {
  const clusters = asList(await api('GET', '/clusters/'));
  const local = clusters.find((c) => c.is_local) || clusters.find((c) => c.name === 'local') || clusters[0];
  return local?.id;
}

async function seed(defs) {
  const ctx = {}; // name -> created id, so later defs can reference earlier ones
  for (const d of defs) {
    const payloads = typeof d.seeds === 'function' ? d.seeds(ctx) : d.seeds;
    let ok = 0;
    for (const payload of payloads) {
      try {
        const created = await api('POST', d.post || d.list, payload);
        if (created?.id) ctx[payload.name] = created.id;
        ok++;
      } catch (e) { console.log(`  ! ${d.label}: ${payload.name} -> ${e.message}`); }
    }
    console.log(`seeded ${ok}/${payloads.length} ${d.label}`);
  }
}

async function teardown(defs) {
  // Reverse order so dependents (schedules) are removed before their
  // dependencies (storage), avoiding FK-constraint delete failures.
  for (const d of [...defs].reverse()) {
    const items = asList(await api('GET', d.list));
    const mine = items.filter((it) => String(it.name || '').startsWith(PREFIX));
    let ok = 0;
    for (const it of mine) {
      try { await api('DELETE', d.del(it.id)); ok++; }
      catch (e) { console.log(`  ! delete ${d.label} ${it.name}: ${e.message}`); }
    }
    console.log(`removed ${ok}/${mine.length} ${d.label}`);
  }
}

async function main() {
  await login();
  const cid = await localClusterId();
  const defs = definitions(cid);
  if (TEARDOWN) {
    console.log('=== teardown (removing demo-* entities) ===');
    await teardown(defs);
  } else {
    console.log(`=== seed (local cluster ${cid}) ===`);
    await seed(defs);
    console.log('\ndone. remove with: node scripts/demo-seed.mjs --teardown');
  }
}

main().catch((e) => { console.error(e); process.exit(1); });
