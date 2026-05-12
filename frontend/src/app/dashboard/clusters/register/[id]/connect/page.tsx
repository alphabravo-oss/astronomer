'use client';

// Wizard page 2 — connect. Operator runs the install command on their
// cluster, then either clicks "I've run it →" or the agent's first
// CONNECT_ACK auto-advances the page (Detect automatically toggle).

import { useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import { useParams } from 'next/navigation';
import { toast } from 'sonner';
import { Loader2, Copy, Check, Download, Server } from 'lucide-react';
import {
  confirmRegistration,
  getClusterManifest,
  getRegistrationStatus,
  type RegistrationStatus,
} from '@/lib/api';
import { useLiveEvents } from '@/lib/live-events';

type Tab = 'quick' | 'yaml' | 'airgapped';

export default function ConnectStepPage() {
  const router = useRouter();
  const params = useParams();
  const clusterId = String(params?.id ?? '');

  const [manifest, setManifest] = useState('');
  const [tab, setTab] = useState<Tab>('quick');
  const [copied, setCopied] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [status, setStatus] = useState<RegistrationStatus | null>(null);
  const [autoDetect, setAutoDetect] = useState(true);
  const advancedRef = useRef(false);

  // Fetch the manifest once on mount; the backend mints a fresh
  // registration token each call.
  useEffect(() => {
    if (!clusterId) return;
    getClusterManifest(clusterId)
      .then(setManifest)
      .catch(() => toast.error('Failed to fetch install manifest'));
    getRegistrationStatus(clusterId).then(setStatus).catch(() => {/* tolerated */});
  }, [clusterId]);

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
      toast.error(`Failed to advance: ${msg}`);
      setConfirming(false);
    }
  };

  const oneLiner = `cat <<'EOF' | kubectl apply -f -\n${manifest}\nEOF`;

  const onCopy = async () => {
    try {
      await navigator.clipboard.writeText(oneLiner);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      toast.error('Failed to copy');
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
          <TabButton active={tab === 'quick'} onClick={() => setTab('quick')}>
            Quick install
          </TabButton>
          <TabButton active={tab === 'yaml'} onClick={() => setTab('yaml')}>
            YAML manifest
          </TabButton>
          <TabButton active={tab === 'airgapped'} onClick={() => setTab('airgapped')}>
            Air-gapped
          </TabButton>
        </nav>
      </div>

      {tab === 'quick' && (
        <div className="space-y-3">
          <p className="text-sm text-muted-foreground">
            Run this command on a host with <code className="text-xs bg-muted px-1 py-0.5 rounded font-mono">kubectl</code> pointed at your cluster:
          </p>
          <div className="relative">
            <pre className="text-xs bg-muted/30 border border-border rounded-lg p-4 overflow-x-auto font-mono whitespace-pre">
              {oneLiner || (manifest ? '' : '# loading...')}
            </pre>
            <button
              onClick={onCopy}
              className="absolute top-2 right-2 inline-flex items-center gap-1.5 h-7 px-2 rounded-md border border-border bg-background text-xs hover:bg-accent"
            >
              {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
              {copied ? 'Copied' : 'Copy'}
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
