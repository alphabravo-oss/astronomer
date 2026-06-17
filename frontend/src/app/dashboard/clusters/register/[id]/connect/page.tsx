'use client';

// Wizard page 2 — connect. Operator runs the install command on their
// cluster, then either clicks "I've run it →" or the agent's first
// CONNECT_ACK auto-advances the page (Detect automatically toggle).

import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useRouter } from '@/lib/navigation';
import { useParams } from '@/lib/navigation';
import { useTabParam } from '@/lib/use-tab-param';
import { queryKeys } from '@/lib/query-keys';
import { toastError } from '@/lib/toast';
import { Loader2, Copy, Check, Download, Server } from 'lucide-react';
import {
  confirmRegistration,
  getClusterManifestWithToken,
  getRegistrationStatus,
  getRegistrationTLS,
  type RegistrationTLSMode,
} from '@/lib/api';
import { useLiveEvents } from '@/lib/live-events';

type Tab = 'curl' | 'quick' | 'yaml' | 'airgapped';
type CurlVariant = 'public_ca' | 'private_ca' | 'insecure';

const TAB_KEYS = ['curl', 'quick', 'yaml', 'airgapped'] as const;

export default function ConnectStepPage() {
  const router = useRouter();
  const params = useParams();
  const clusterId = String(params?.id ?? '');

  const [tab, setTab] = useTabParam(TAB_KEYS, 'curl');
  const [copied, setCopied] = useState<CurlVariant | 'quick' | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [autoDetect, setAutoDetect] = useState(true);
  const [tlsMode, setTlsMode] = useState<RegistrationTLSMode>('public_ca');
  const [curlVariant, setCurlVariant] = useState<CurlVariant>('public_ca');
  const advancedRef = useRef(false);

  // Fetch the manifest once on mount; the backend mints a fresh registration
  // token each call and exposes it via a response header so the Curl tab can
  // render the Rancher-style one-liner. staleTime: Infinity keeps a window
  // refocus from silently re-minting the token mid-flow.
  const { data: manifestData, isError: manifestError } = useQuery({
    queryKey: queryKeys.clusterPages.registrationManifest(clusterId),
    queryFn: () => getClusterManifestWithToken(clusterId),
    enabled: !!clusterId,
    staleTime: Infinity,
    retry: false,
  });
  const manifest = manifestData?.manifest ?? '';
  const registrationToken = manifestData?.token ?? '';
  useEffect(() => {
    if (manifestError) toastError('Failed to fetch install manifest');
  }, [manifestError]);

  const { data: status = null } = useQuery({
    queryKey: queryKeys.clusterPages.registrationStatus(clusterId),
    queryFn: () => getRegistrationStatus(clusterId),
    enabled: !!clusterId,
    retry: false,
  });

  // Operator-configured TLS posture from platform_settings. Seeds the editable
  // tlsMode/curlVariant selectors; defaults to public_ca on any failure.
  const { data: tlsData } = useQuery({
    queryKey: queryKeys.settings.registrationTls,
    queryFn: () => getRegistrationTLS(),
    staleTime: Infinity,
    retry: false,
  });
  useEffect(() => {
    if (tlsData) {
      setTlsMode(tlsData.mode);
      setCurlVariant(tlsData.mode);
    }
  }, [tlsData]);

  // SSE subscription via the global live-events bus. When the agent
  // connects, hop to page 3 if auto-detect is on.
  const live = useLiveEvents();
  useEffect(() => {
    if (!autoDetect || advancedRef.current) return;
    const off1 = live.subscribe('cluster.connected', (payload) => {
      const data = payload as { cluster_id?: string };
      if (data?.cluster_id === clusterId && !advancedRef.current) {
        advancedRef.current = true;
        router.push(`/dashboard/clusters/register/${clusterId}/progress`);
      }
    });
    return () => off1();
  }, [live, autoDetect, clusterId, router]);

  const advance = async () => {
    setConfirming(true);
    try {
      await confirmRegistration(clusterId);
      router.push(`/dashboard/clusters/register/${clusterId}/progress`);
    } catch (e) {
      const msg = e instanceof Error ? e.message : 'Unknown error';
      toastError(`Failed to advance: ${msg}`);
      setConfirming(false);
    }
  };

  const oneLiner = `cat <<'EOF' | kubectl apply -f -\n${manifest}\nEOF`;

  // Rancher-style one-liner that pulls the manifest from the public
  // /api/v1/register/<token> endpoint and pipes it into kubectl apply.
  // We build the URL from the browser's origin so the agent host hits
  // the same server the operator is staring at. Falls back to a
  // placeholder while the token is still in flight.
  const curlOrigin = typeof window !== 'undefined' ? window.location.origin : '';
  // The `.yaml` suffix matters: the server's trailing-slash middleware
  // leaves dotted last segments alone, so chi dispatches the request
  // directly to the public manifest handler without a trailing-slash
  // rewrite stealing the request first.
  const manifestURL = registrationToken
    ? `${curlOrigin}/api/v1/register/${registrationToken}.yaml`
    : '';
  const caURL = `${curlOrigin}/api/v1/register/ca.crt`;

  // Rancher offers three TLS postures. We render all three so the
  // operator can pick whichever one matches their reality (the radio
  // defaults to the platform-configured mode, but copy-paste is the
  // ultimate decider).
  const curlVariants: Record<CurlVariant, { label: string; hint: string; cmd: string }> = {
    public_ca: {
      label: 'Trusted CA',
      hint: 'Use when this platform serves over HTTPS with a publicly-trusted certificate.',
      cmd: manifestURL ? `curl -sfL ${manifestURL} | kubectl apply -f -` : '',
    },
    private_ca: {
      label: 'Private CA',
      hint: 'Use when this platform serves over HTTPS with a CA that isn\'t in the system trust store. The first curl fetches the operator-provided bundle, the second pins to it.',
      cmd: manifestURL
        ? `curl -sfL ${caURL} -o /tmp/astronomer-ca.crt\ncurl --cacert /tmp/astronomer-ca.crt -sfL ${manifestURL} | kubectl apply -f -`
        : '',
    },
    insecure: {
      label: 'Skip TLS verify',
      hint: 'Escape hatch for ops who haven\'t pinned a CA yet. Functionally equivalent to passing --insecure-skip-tls-verify; not recommended for long-lived agents.',
      cmd: manifestURL ? `curl --insecure -sfL ${manifestURL} | kubectl apply -f -` : '',
    },
  };

  const onCopy = async (which: CurlVariant | 'quick', text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(which);
      setTimeout(() => setCopied(null), 1500);
    } catch {
      toastError('Failed to copy');
    }
  };

  const onDownload = () => {
    const blob = new Blob([manifest], { type: 'text/yaml' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `astronomer-agent-${clusterId}.yaml`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  };

  const agentConnected =
    status?.phase === 'connected' || status?.phase === 'provisioning' || status?.phase === 'ready';

  return (
    <div className="max-w-4xl mx-auto p-6">
      <div className="mb-6 flex items-center gap-3">
        <div className="w-10 h-10 rounded-lg bg-muted flex items-center justify-center">
          <Server className="h-5 w-5 text-muted-foreground" />
        </div>
        <div>
          <h1 className="text-2xl font-semibold text-foreground">Install the agent</h1>
          <p className="text-sm text-muted-foreground">Step 2 of 3 — Run the install command on your cluster</p>
        </div>
      </div>

      <div className="border-b border-border mb-4">
        <nav className="flex gap-1">
          <TabButton active={tab === 'curl'} onClick={() => setTab('curl')}>
            Curl
          </TabButton>
          <TabButton active={tab === 'quick'} onClick={() => setTab('quick')}>
            Inline
          </TabButton>
          <TabButton active={tab === 'yaml'} onClick={() => setTab('yaml')}>
            YAML manifest
          </TabButton>
          <TabButton active={tab === 'airgapped'} onClick={() => setTab('airgapped')}>
            Air-gapped
          </TabButton>
        </nav>
      </div>

      {tab === 'curl' && (
        <div className="space-y-3">
          <p className="text-sm text-muted-foreground">
            On a host with <code className="text-xs bg-muted px-1 py-0.5 rounded font-mono">kubectl</code> pointed at your cluster, run the variant that matches this platform&apos;s TLS posture:
          </p>
          <div className="flex flex-wrap gap-2 text-xs">
            {(['public_ca', 'private_ca', 'insecure'] as const).map((v) => {
              const active = curlVariant === v;
              const isPlatformDefault = v === tlsMode;
              return (
                <button
                  key={v}
                  type="button"
                  onClick={() => setCurlVariant(v)}
                  className={
                    'inline-flex items-center gap-1.5 h-8 px-3 rounded-md border text-xs transition-colors ' +
                    (active
                      ? 'border-primary bg-primary/10 text-foreground'
                      : 'border-border bg-background text-muted-foreground hover:bg-accent')
                  }
                >
                  {curlVariants[v].label}
                  {isPlatformDefault && (
                    <span className="text-[10px] text-muted-foreground">(platform default)</span>
                  )}
                </button>
              );
            })}
          </div>
          <p className="text-xs text-muted-foreground">{curlVariants[curlVariant].hint}</p>
          <div className="relative">
            <pre className="text-xs bg-muted/30 border border-border rounded-lg p-4 overflow-x-auto font-mono whitespace-pre">
              {curlVariants[curlVariant].cmd || '# loading...'}
            </pre>
            <button
              onClick={() => onCopy(curlVariant, curlVariants[curlVariant].cmd)}
              disabled={!curlVariants[curlVariant].cmd}
              className="absolute top-2 right-2 inline-flex items-center gap-1.5 h-7 px-2 rounded-md border border-border bg-background text-xs hover:bg-accent disabled:opacity-50"
            >
              {copied === curlVariant ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
              {copied === curlVariant ? 'Copied' : 'Copy'}
            </button>
          </div>
          <p className="text-xs text-muted-foreground">
            The URL pulls a freshly-rendered manifest signed with a single-use registration token (24h TTL). The agent host must be able to reach{' '}
            <code className="font-mono">{curlOrigin || 'this server'}</code>.
          </p>
        </div>
      )}

      {tab === 'quick' && (
        <div className="space-y-3">
          <p className="text-sm text-muted-foreground">
            Paste the rendered manifest directly into <code className="text-xs bg-muted px-1 py-0.5 rounded font-mono">kubectl apply</code>. Useful when the agent host can't reach this server's URL (the Curl tab needs egress to {curlOrigin || 'this server'}).
          </p>
          <div className="relative">
            <pre className="text-xs bg-muted/30 border border-border rounded-lg p-4 overflow-x-auto font-mono whitespace-pre">
              {oneLiner || (manifest ? '' : '# loading...')}
            </pre>
            <button
              onClick={() => onCopy('quick', oneLiner)}
              className="absolute top-2 right-2 inline-flex items-center gap-1.5 h-7 px-2 rounded-md border border-border bg-background text-xs hover:bg-accent"
            >
              {copied === 'quick' ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
              {copied === 'quick' ? 'Copied' : 'Copy'}
            </button>
          </div>
        </div>
      )}

      {tab === 'yaml' && (
        <div className="space-y-3">
          <p className="text-sm text-muted-foreground">
            Download the rendered manifest and apply it manually. Useful when piping curl into kubectl isn't allowed.
          </p>
          <div className="relative">
            <pre className="text-xs bg-muted/30 border border-border rounded-lg p-4 overflow-x-auto font-mono max-h-96">
              {manifest || '# loading...'}
            </pre>
          </div>
          <button
            onClick={onDownload}
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-lg border border-border text-sm font-medium hover:bg-accent"
          >
            <Download className="h-3.5 w-3.5" />
            Download YAML
          </button>
        </div>
      )}

      {tab === 'airgapped' && (
        <div className="space-y-3 text-sm text-muted-foreground">
          <p>
            For air-gapped environments, download the manifest above, mirror the
            agent image to your private registry, and apply the modified manifest.
          </p>
          <p>
            See <a className="underline" href="/docs/cluster-registration-api.md" target="_blank" rel="noreferrer">cluster-registration-api.md</a> for the full offline-install procedure.
          </p>
        </div>
      )}

      <div className="mt-8 flex flex-col gap-3">
        <label className="flex items-center gap-2 text-sm text-muted-foreground">
          <input
            type="checkbox"
            checked={autoDetect}
            onChange={(e) => setAutoDetect(e.target.checked)}
            className="h-4 w-4 rounded border-border"
          />
          Detect automatically (advance when the agent connects)
        </label>

        {agentConnected && (
          <div className="text-sm text-status-success">✓ Agent connected — advancing...</div>
        )}
        {!agentConnected && autoDetect && (
          <div className="text-sm text-muted-foreground">⏳ Waiting for agent...</div>
        )}

        <div className="flex items-center justify-end gap-2">
          <button
            type="button"
            onClick={() => router.push('/dashboard/clusters/register')}
            className="h-10 px-4 rounded-lg border border-border text-sm font-medium hover:bg-accent"
          >
            ← Back
          </button>
          <button
            onClick={advance}
            disabled={confirming}
            className="inline-flex items-center gap-2 h-10 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50"
          >
            {confirming && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            I've run it →
          </button>
        </div>
      </div>
    </div>
  );
}

function TabButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
        active ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'
      }`}
    >
      {children}
    </button>
  );
}
