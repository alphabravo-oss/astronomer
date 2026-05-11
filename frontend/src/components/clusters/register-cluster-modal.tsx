'use client';

import { useState, useEffect } from 'react';
import { useCreateCluster } from '@/lib/hooks';
import { registerCluster } from '@/lib/api';
import { CodeBlock } from '@/components/ui/code-block';
import type { ClusterEnvironment } from '@/types';
import {
  X,
  Loader2,
  Server,
  AlertTriangle,
  Download,
  ChevronDown,
  ChevronRight,
  Info,
  Copy,
  Check,
} from 'lucide-react';
import { cn, copyToClipboard } from '@/lib/utils';
import { toast } from 'sonner';

interface RegisterClusterModalProps {
  onClose: () => void;
  /** When provided, skips the form step and immediately fetches registration commands for this cluster */
  clusterId?: string;
  clusterName?: string;
}

type InstallTab = 'kubectl' | 'curl';

export function RegisterClusterModal({ onClose, clusterId: existingClusterId, clusterName: existingClusterName }: RegisterClusterModalProps) {
  const createCluster = useCreateCluster();
  const isExisting = !!existingClusterId;
  const [step, setStep] = useState<'form' | 'install'>(isExisting ? 'install' : 'form');
  const [form, setForm] = useState({
    name: '',
    displayName: '',
    environment: 'development' as ClusterEnvironment,
    description: '',
  });
  const [installManifest, setInstallManifest] = useState('');
  const [registrationToken, setRegistrationToken] = useState('');
  const [clusterId, setClusterId] = useState(existingClusterId || '');
  const [activeTab, setActiveTab] = useState<InstallTab>('kubectl');
  const [insecure, setInsecure] = useState(false);
  const [showYaml, setShowYaml] = useState(false);
  const [copiedField, setCopiedField] = useState<string | null>(null);
  const [loadingRegistration, setLoadingRegistration] = useState(false);

  // When opened for an existing cluster, immediately fetch registration data
  useEffect(() => {
    if (isExisting && existingClusterId) {
      setLoadingRegistration(true);
      registerCluster(existingClusterId)
        .then((registration) => {
          setInstallManifest(registration.install_manifest);
          setRegistrationToken(registration.token?.token || '');
          setClusterId(existingClusterId);
        })
        .catch(() => {
          toast.error('Failed to fetch registration command');
          onClose();
        })
        .finally(() => setLoadingRegistration(false));
    }
  }, [isExisting, existingClusterId, onClose]);

  const handleSubmit = async () => {
    if (!form.name) return;

    try {
      const cluster = await createCluster.mutateAsync({
        name: form.name,
        displayName: form.displayName || form.name,
        environment: form.environment,
        description: form.description || undefined,
      });
      const registration = await registerCluster(cluster.id);
      setInstallManifest(registration.install_manifest);
      setRegistrationToken(registration.token?.token || '');
      setClusterId(cluster.id);
      setStep('install');
    } catch {
      // Error handled by mutation
    }
  };

  const handleCopy = async (text: string, field: string) => {
    const success = await copyToClipboard(text);
    if (success) {
      setCopiedField(field);
      toast.success('Copied to clipboard');
      setTimeout(() => setCopiedField(null), 2000);
    } else {
      toast.error('Failed to copy');
    }
  };

  const handleDownloadYaml = () => {
    const blob = new Blob([installManifest], { type: 'text/yaml' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `astronomer-agent-${existingClusterName || form.name || 'cluster'}.yaml`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
    toast.success('Manifest downloaded');
  };

  // Build the server base URL from the current page location
  const serverBaseUrl = typeof window !== 'undefined'
    ? `${window.location.protocol}//${window.location.host}`
    : '';

  const manifestUrl = `${serverBaseUrl}/api/v1/clusters/${clusterId}/manifest/`;

  const kubectlCommand = `cat <<'EOF' | kubectl apply -f -\n${installManifest}\nEOF`;

  const curlCommand = insecure
    ? `curl --insecure -sfL '${manifestUrl}' | kubectl apply -f -`
    : `curl -sfL '${manifestUrl}' | kubectl apply -f -`;

  const modalTitle = isExisting
    ? 'Registration Command'
    : step === 'form'
      ? 'Register Cluster'
      : 'Install Agent';

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className={cn(
        'relative w-full rounded-xl border border-border bg-popover shadow-2xl overflow-hidden',
        step === 'form' ? 'max-w-lg' : 'max-w-2xl'
      )}>
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-border">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
              <Server className="h-4 w-4 text-muted-foreground" />
            </div>
            <h3 className="text-lg font-semibold text-foreground">
              {modalTitle}
            </h3>
          </div>
          <button
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground transition-colors"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        {/* Content */}
        <div className="p-6 max-h-[70vh] overflow-y-auto">
          {loadingRegistration ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            </div>
          ) : step === 'form' ? (
            <div className="space-y-4">
              <div className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">Cluster Name</label>
                <input
                  type="text"
                  value={form.name}
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      name: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'),
                    }))
                  }
                  placeholder="my-cluster"
                  className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                    placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                  autoFocus
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">Display Name</label>
                <input
                  type="text"
                  value={form.displayName}
                  onChange={(e) => setForm((f) => ({ ...f, displayName: e.target.value }))}
                  placeholder="My Production Cluster"
                  className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                    placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">Environment</label>
                <select
                  value={form.environment}
                  onChange={(e) => setForm((f) => ({ ...f, environment: e.target.value as ClusterEnvironment }))}
                  className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                    focus:outline-none focus:ring-2 focus:ring-ring"
                >
                  <option value="production">Production</option>
                  <option value="staging">Staging</option>
                  <option value="development">Development</option>
                  <option value="testing">Testing</option>
                </select>
              </div>

              <div className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">
                  Description <span className="text-muted-foreground font-normal">(optional)</span>
                </label>
                <textarea
                  value={form.description}
                  onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
                  placeholder="Brief description..."
                  rows={2}
                  className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm
                    placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring resize-none"
                />
              </div>
            </div>
          ) : (
            <div className="space-y-5">
              {/* Prerequisite banner */}
              <div className="flex items-start gap-2.5 p-3 rounded-lg bg-status-warning/5 border border-status-warning/20">
                <AlertTriangle className="h-4 w-4 text-status-warning mt-0.5 flex-shrink-0" />
                <p className="text-sm text-status-warning">
                  Ensure <code className="text-xs bg-status-warning/10 px-1 py-0.5 rounded font-mono">kubectl</code> is
                  configured for the target cluster with <strong>cluster-admin</strong> privileges.
                </p>
              </div>

              {/* Installation method tabs */}
              <div className="flex gap-1 border-b border-border">
                <button
                  onClick={() => setActiveTab('kubectl')}
                  className={cn(
                    'px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors',
                    activeTab === 'kubectl'
                      ? 'border-primary text-foreground'
                      : 'border-transparent text-muted-foreground hover:text-foreground'
                  )}
                >
                  kubectl
                </button>
                <button
                  onClick={() => setActiveTab('curl')}
                  className={cn(
                    'px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors',
                    activeTab === 'curl'
                      ? 'border-primary text-foreground'
                      : 'border-transparent text-muted-foreground hover:text-foreground'
                  )}
                >
                  curl
                </button>
              </div>

              {/* Registration token */}
              {registrationToken && (
                <div className="space-y-1.5">
                  <label className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
                    Registration Token
                  </label>
                  <div className="flex items-center gap-2">
                    <div className="flex-1 flex items-center h-9 px-3 rounded-lg border border-border bg-muted/30 font-mono text-xs text-muted-foreground overflow-hidden">
                      <span className="truncate">{registrationToken}</span>
                    </div>
                    <button
                      onClick={() => handleCopy(registrationToken, 'token')}
                      className={cn(
                        'inline-flex items-center gap-1.5 h-9 px-3 rounded-lg border text-xs font-medium transition-all flex-shrink-0',
                        copiedField === 'token'
                          ? 'border-status-success/30 bg-status-success/5 text-status-success'
                          : 'border-border text-muted-foreground hover:text-foreground hover:bg-accent'
                      )}
                    >
                      {copiedField === 'token' ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
                      {copiedField === 'token' ? 'Copied' : 'Copy'}
                    </button>
                  </div>
                </div>
              )}

              {/* kubectl tab */}
              {activeTab === 'kubectl' && (
                <div className="space-y-4">
                  <div className="space-y-1.5">
                    <label className="text-sm font-medium text-foreground">
                      Run this command on your cluster:
                    </label>
                    <CodeBlock
                      code={kubectlCommand}
                      language="bash"
                      title="kubectl apply"
                    />
                  </div>

                  {/* Expand YAML / Download */}
                  <div className="space-y-3">
                    <button
                      onClick={() => setShowYaml(!showYaml)}
                      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground transition-colors"
                    >
                      {showYaml ? (
                        <ChevronDown className="h-4 w-4" />
                      ) : (
                        <ChevronRight className="h-4 w-4" />
                      )}
                      View full YAML manifest
                    </button>

                    {showYaml && (
                      <CodeBlock
                        code={installManifest}
                        language="yaml"
                        title="astronomer-agent.yaml"
                        showLineNumbers
                      />
                    )}

                    <button
                      onClick={handleDownloadYaml}
                      className="inline-flex items-center gap-1.5 h-8 px-3 rounded-md border border-border text-xs font-medium
                        text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                    >
                      <Download className="h-3 w-3" />
                      Download YAML
                    </button>
                  </div>
                </div>
              )}

              {/* curl tab */}
              {activeTab === 'curl' && (
                <div className="space-y-4">
                  <div className="space-y-1.5">
                    <label className="text-sm font-medium text-foreground">
                      Run this command on your cluster:
                    </label>
                    <CodeBlock
                      code={curlCommand}
                      language="bash"
                      title="curl | kubectl apply"
                    />
                  </div>

                  <p className="text-xs text-muted-foreground">
                    This fetches the manifest from the server and pipes it into kubectl.
                    Use this method when you cannot paste the full manifest directly.
                  </p>
                </div>
              )}

              {/* Insecure checkbox */}
              <label className="flex items-start gap-2.5 cursor-pointer group">
                <input
                  type="checkbox"
                  checked={insecure}
                  onChange={(e) => setInsecure(e.target.checked)}
                  className="mt-0.5 h-4 w-4 rounded border-border text-primary focus:ring-ring"
                />
                <div>
                  <span className="text-sm text-foreground group-hover:text-foreground/80">
                    Insecure connection (skip TLS verification)
                  </span>
                  <p className="text-xs text-muted-foreground mt-0.5">
                    Use this if the Astronomer server uses self-signed certificates or you are in an air-gapped environment with a custom CA.
                  </p>
                </div>
              </label>

              {/* What gets installed */}
              <div className="rounded-lg bg-muted/30 border border-border p-4">
                <div className="flex items-center gap-2 mb-2.5">
                  <Info className="h-3.5 w-3.5 text-muted-foreground" />
                  <span className="text-sm font-medium text-foreground">What gets installed</span>
                </div>
                <ul className="text-sm text-muted-foreground space-y-1.5 ml-5.5 list-disc list-inside">
                  <li><code className="text-xs bg-muted px-1 py-0.5 rounded font-mono">astronomer-system</code> namespace</li>
                  <li>Astronomer agent deployment</li>
                  <li>ClusterRole + ServiceAccount (RBAC)</li>
                  <li>Secure WebSocket tunnel to server</li>
                </ul>
              </div>
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border bg-muted/30">
          {step === 'form' ? (
            <>
              <button
                onClick={onClose}
                className="h-9 px-4 rounded-lg border border-border text-sm font-medium
                  text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={handleSubmit}
                disabled={!form.name || createCluster.isPending}
                className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                  text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
              >
                {createCluster.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                Register
              </button>
            </>
          ) : (
            <button
              onClick={onClose}
              className="h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium
                hover:opacity-90 transition-opacity"
            >
              Done
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
