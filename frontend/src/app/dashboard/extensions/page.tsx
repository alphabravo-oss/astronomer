'use client';

import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import {
  AlertTriangle,
  CheckCircle2,
  Loader2,
  PackagePlus,
  Puzzle,
  Shield,
  XCircle,
} from 'lucide-react';

import {
  disableExtension,
  enableExtension,
  installExtension,
  listExtensions,
  validateExtensionManifest,
  type ExtensionFinding,
  type ExtensionListResponse,
  type ExtensionManifest,
  type ExtensionValidationResponse,
} from '@/lib/api/extensions';

const qk = {
  extensions: ['extensions'] as const,
};

function statusClass(status: string, enabled?: boolean) {
  if (enabled) return 'bg-emerald-500/10 text-emerald-500';
  if (status === 'compatible') return 'bg-muted text-muted-foreground';
  if (status === 'incompatible') return 'bg-red-500/10 text-red-500';
  return 'bg-amber-500/10 text-amber-500';
}

function FindingList({ findings }: { findings: ExtensionFinding[] }) {
  if (findings.length === 0) {
    return <p className="text-xs text-muted-foreground">No findings.</p>;
  }
  return (
    <div className="space-y-2">
      {findings.map((finding, index) => (
        <div key={`${finding.field || 'finding'}:${index}`} className="rounded border border-border p-3">
          <div className="flex items-center gap-2 text-xs font-medium text-foreground">
            {finding.severity === 'error' ? (
              <XCircle className="h-3.5 w-3.5 text-red-500" />
            ) : (
              <AlertTriangle className="h-3.5 w-3.5 text-amber-500" />
            )}
            {finding.severity === 'error' ? 'Error' : 'Warning'}
            {finding.field && <span className="text-muted-foreground">{finding.field}</span>}
          </div>
          <p className="text-xs text-muted-foreground mt-1 leading-relaxed">{finding.message}</p>
        </div>
      ))}
    </div>
  );
}

function ExtensionTable({
  data,
  onToggle,
  toggling,
}: {
  data?: ExtensionListResponse;
  onToggle: (name: string, enabled: boolean) => void;
  toggling: boolean;
}) {
  const items = data?.items ?? [];
  return (
    <div className="rounded-lg border border-border bg-card overflow-hidden">
      <div className="px-5 py-4 border-b border-border flex items-center justify-between">
        <div>
          <h2 className="text-sm font-semibold text-foreground">Installed extensions</h2>
          <p className="text-xs text-muted-foreground mt-1">{items.length} registered</p>
        </div>
      </div>
      {items.length === 0 ? (
        <div className="p-8 text-center text-sm text-muted-foreground">No extensions installed.</div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-muted/40 text-xs text-muted-foreground">
              <tr>
                <th className="px-5 py-2.5 text-left font-medium">Name</th>
                <th className="px-5 py-2.5 text-left font-medium">Version</th>
                <th className="px-5 py-2.5 text-left font-medium">Permissions</th>
                <th className="px-5 py-2.5 text-left font-medium">Status</th>
                <th className="px-5 py-2.5 text-right font-medium">Action</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.id} className="border-t border-border">
                  <td className="px-5 py-3">
                    <div className="font-medium text-foreground">{item.displayName || item.name}</div>
                    <div className="text-xs text-muted-foreground font-mono">{item.name}</div>
                  </td>
                  <td className="px-5 py-3 text-muted-foreground whitespace-nowrap">{item.version}</td>
                  <td className="px-5 py-3">
                    <div className="flex flex-wrap gap-1.5">
                      {(item.manifest.permissions ?? []).slice(0, 4).map((permission) => (
                        <span
                          key={permission}
                          className="rounded border border-border px-2 py-1 text-xs text-muted-foreground"
                        >
                          {permission}
                        </span>
                      ))}
                      {(item.manifest.permissions ?? []).length > 4 && (
                        <span className="text-xs text-muted-foreground py-1">
                          +{(item.manifest.permissions ?? []).length - 4}
                        </span>
                      )}
                    </div>
                  </td>
                  <td className="px-5 py-3">
                    <span className={`inline-flex rounded px-2 py-1 text-xs ${statusClass(item.compatibilityStatus, item.enabled)}`}>
                      {item.enabled ? 'enabled' : item.compatibilityStatus}
                    </span>
                  </td>
                  <td className="px-5 py-3 text-right">
                    <button
                      type="button"
                      disabled={toggling || (item.compatibilityStatus !== 'compatible' && !item.enabled)}
                      onClick={() => onToggle(item.name, !item.enabled)}
                      className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
                        border border-border text-foreground hover:bg-accent transition-colors
                        disabled:opacity-50 disabled:cursor-not-allowed"
                    >
                      {item.enabled ? 'Disable' : 'Enable'}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

export default function ExtensionsPage() {
  const queryClient = useQueryClient();
  const [manifestText, setManifestText] = useState('');
  const [validation, setValidation] = useState<ExtensionValidationResponse | undefined>();

  const { data, isLoading } = useQuery({
    queryKey: qk.extensions,
    queryFn: listExtensions,
  });

  useEffect(() => {
    if (!manifestText && data?.sampleManifest) {
      setManifestText(JSON.stringify(data.sampleManifest, null, 2));
    }
  }, [data?.sampleManifest, manifestText]);

  const parsedManifest = useMemo(() => {
    try {
      return JSON.parse(manifestText) as ExtensionManifest;
    } catch {
      return undefined;
    }
  }, [manifestText]);

  const validate = useMutation({
    mutationFn: async () => {
      if (!parsedManifest) throw new Error('Manifest must be valid JSON');
      return validateExtensionManifest(parsedManifest);
    },
    onSuccess: (result) => {
      setValidation(result);
      if (result.valid) toast.success('Extension manifest is valid');
      else toast.warning('Extension manifest has findings');
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const install = useMutation({
    mutationFn: async () => {
      if (!parsedManifest) throw new Error('Manifest must be valid JSON');
      return installExtension(parsedManifest, { source: 'manual', enable: validation?.valid ?? false });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: qk.extensions });
      toast.success('Extension installed');
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const toggle = useMutation({
    mutationFn: ({ name, enabled }: { name: string; enabled: boolean }) =>
      enabled ? enableExtension(name) : disableExtension(name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: qk.extensions });
      toast.success('Extension updated');
    },
    onError: (error: Error) => toast.error(error.message),
  });

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Extensions</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Registry, manifest validation, permissions, and compatibility state.
          </p>
        </div>
        <div className="rounded-md bg-accent/30 p-2.5">
          <Puzzle className="h-5 w-5 text-foreground" />
        </div>
      </div>

      {isLoading ? (
        <div className="flex items-center justify-center h-40">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      ) : (
        <ExtensionTable
          data={data}
          toggling={toggle.isPending}
          onToggle={(name, enabled) => toggle.mutate({ name, enabled })}
        />
      )}

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_420px]">
        <div className="rounded-lg border border-border bg-card overflow-hidden">
          <div className="px-5 py-4 border-b border-border flex items-center justify-between gap-4">
            <div>
              <h2 className="text-sm font-semibold text-foreground">Manifest</h2>
              <p className="text-xs text-muted-foreground mt-1">
                {parsedManifest?.name || 'No manifest loaded'}
              </p>
            </div>
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => validate.mutate()}
                disabled={validate.isPending || !parsedManifest}
                className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
                  border border-border text-foreground hover:bg-accent transition-colors
                  disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {validate.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <CheckCircle2 className="h-3.5 w-3.5" />}
                Validate
              </button>
              <button
                type="button"
                onClick={() => install.mutate()}
                disabled={install.isPending || !validation?.valid || validation.compatibilityStatus !== 'compatible'}
                className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
                  bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
                  disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {install.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <PackagePlus className="h-3.5 w-3.5" />}
                Install
              </button>
            </div>
          </div>
          <textarea
            value={manifestText}
            onChange={(event) => {
              setManifestText(event.target.value);
              setValidation(undefined);
            }}
            spellCheck={false}
            className="min-h-[520px] w-full resize-y bg-background p-4 font-mono text-xs text-foreground outline-none"
          />
        </div>

        <div className="space-y-4">
          <div className="rounded-lg border border-border bg-card p-4">
            <div className="flex items-start gap-3">
              <Shield className="h-5 w-5 text-muted-foreground mt-0.5" />
              <div>
                <h2 className="text-sm font-semibold text-foreground">Validation state</h2>
                <p className="text-xs text-muted-foreground mt-1">
                  {validation
                    ? `${validation.compatibilityStatus}; ${validation.checksum}`
                    : parsedManifest
                      ? 'Ready to validate'
                      : 'Invalid JSON'}
                </p>
              </div>
            </div>
          </div>
          {validation && (
            <>
              <div className={`rounded-lg border p-4 ${
                validation.valid && validation.compatibilityStatus === 'compatible'
                  ? 'border-emerald-500/30 bg-emerald-500/10'
                  : 'border-amber-500/30 bg-amber-500/10'
              }`}>
                <div className="text-sm font-medium text-foreground">
                  {validation.valid ? 'Manifest accepted' : 'Manifest blocked'}
                </div>
                <p className="text-xs text-muted-foreground mt-1">
                  Compatibility: {validation.compatibilityStatus}
                </p>
              </div>
              <FindingList findings={[...validation.errors, ...validation.warnings]} />
            </>
          )}
        </div>
      </div>
    </div>
  );
}
