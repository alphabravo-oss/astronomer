'use client';

// Add-repo dialog. Supports HTTPS git/helm with username+password and SSH with
// a private key. Test button hits /argocd/instances/{id}/repos/test/ before
// submitting to upstream.

import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { ModalShell } from '@/components/ui/modal-shell';
import { Loader2, GitFork, CheckCircle2, AlertCircle } from 'lucide-react';
import { createArgoRepo, testArgoRepo } from '@/lib/api';
import { queryKeys } from '@/lib/hooks';
import type { ArgoRepositoryCreate } from '@/types';

interface AddRepoDialogProps {
  instanceId: string;
  onClose: () => void;
}

type AuthMode = 'none' | 'userpass' | 'ssh';

export function AddRepoDialog({ instanceId, onClose }: AddRepoDialogProps) {
  const queryClient = useQueryClient();
  const [form, setForm] = useState({
    repo: '',
    type: 'git' as 'git' | 'helm',
    name: '',
    username: '',
    password: '',
    sshPrivateKey: '',
    insecure: false,
  });
  const [authMode, setAuthMode] = useState<AuthMode>('userpass');
  const [testResult, setTestResult] = useState<null | { ok: boolean; message: string }>(null);

  const buildBody = (): ArgoRepositoryCreate => ({
    repo: form.repo.trim(),
    type: form.type,
    name: form.name.trim() || undefined,
    insecure: form.insecure || undefined,
    username: authMode === 'userpass' ? form.username || undefined : undefined,
    password: authMode === 'userpass' ? form.password || undefined : undefined,
    ssh_private_key: authMode === 'ssh' ? form.sshPrivateKey || undefined : undefined,
  });

  const test = useMutation({
    mutationFn: () => testArgoRepo(instanceId, buildBody()),
    onSuccess: (repo) => {
      const status = repo.connectionState?.status ?? 'Unknown';
      setTestResult({
        ok: status === 'Successful',
        message: repo.connectionState?.message ?? status,
      });
    },
    onError: (error: Error) => {
      setTestResult({ ok: false, message: error.message });
    },
  });

  const create = useMutation({
    mutationFn: () => createArgoRepo(instanceId, buildBody()),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.repos(instanceId) });
      toastSuccess('Repository added');
      onClose();
    },
    onError: (error: Error) => {
      toastApiError('Add failed', error);
    },
  });

  return (
    <ModalShell
      title="Add Repository"
      onClose={onClose}
      panelClassName="max-w-lg bg-popover overflow-hidden"
      bodyClassName="max-h-[70vh] overflow-y-auto"
      footerClassName="bg-muted/30"
      titleIcon={(
        <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
          <GitFork className="h-4 w-4 text-muted-foreground" />
        </div>
      )}
      footer={(
        <div className="flex items-center justify-between gap-2">
          <button
            onClick={() => test.mutate()}
            disabled={!form.repo.trim() || test.isPending}
            className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-sm
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors
              disabled:opacity-50"
          >
            {test.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Test Connection
          </button>
          <div className="flex gap-2">
            <button
              onClick={onClose}
              disabled={create.isPending}
              className="inline-flex items-center h-8 px-3 rounded text-sm
                text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            >
              Cancel
            </button>
            <button
              onClick={() => create.mutate()}
              disabled={!form.repo.trim() || create.isPending}
              className="inline-flex items-center gap-1.5 h-8 px-4 rounded text-sm font-medium
                bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
                disabled:opacity-50"
            >
              {create.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Add
            </button>
          </div>
        </div>
      )}
    >
          <div className="grid grid-cols-3 gap-3">
            <div className="col-span-2 space-y-1.5">
              <label className="text-sm font-medium text-foreground">Repository URL</label>
              <input
                type="text"
                value={form.repo}
                onChange={(e) => {
                  setForm({ ...form, repo: e.target.value });
                  setTestResult(null);
                }}
                placeholder="https://github.com/org/manifests"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                  focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Type</label>
              <select
                value={form.type}
                onChange={(e) => setForm({ ...form, type: e.target.value as 'git' | 'helm' })}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  focus:outline-none focus:ring-1 focus:ring-ring"
              >
                <option value="git">Git</option>
                <option value="helm">Helm</option>
              </select>
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Display Name (optional)</label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="manifests"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-2">
            <label className="text-sm font-medium text-foreground">Authentication</label>
            <div className="flex gap-2">
              {(['none', 'userpass', 'ssh'] as AuthMode[]).map((m) => (
                <button
                  key={m}
                  type="button"
                  onClick={() => setAuthMode(m)}
                  className={`px-3 py-1.5 rounded-md text-xs font-medium transition-colors ${
                    authMode === m
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-muted text-muted-foreground hover:text-foreground'
                  }`}
                >
                  {m === 'none' ? 'Public' : m === 'userpass' ? 'Username + Password' : 'SSH Key'}
                </button>
              ))}
            </div>
          </div>

          {authMode === 'userpass' && (
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">Username</label>
                <input
                  type="text"
                  value={form.username}
                  onChange={(e) => setForm({ ...form, username: e.target.value })}
                  autoComplete="off"
                  className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                    focus:outline-none focus:ring-1 focus:ring-ring"
                />
              </div>
              <div className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">Password / Token</label>
                <input
                  type="password"
                  value={form.password}
                  onChange={(e) => setForm({ ...form, password: e.target.value })}
                  autoComplete="new-password"
                  placeholder="••••••••"
                  className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                    focus:outline-none focus:ring-1 focus:ring-ring"
                />
              </div>
            </div>
          )}

          {authMode === 'ssh' && (
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">SSH Private Key</label>
              <textarea
                value={form.sshPrivateKey}
                onChange={(e) => setForm({ ...form, sshPrivateKey: e.target.value })}
                rows={5}
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                className="w-full px-3 py-2 rounded-md border border-border bg-background text-xs font-mono
                  focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          )}

          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={form.insecure}
              onChange={(e) => setForm({ ...form, insecure: e.target.checked })}
              className="h-4 w-4 rounded border-border"
            />
            <span className="text-foreground">Skip TLS verification (insecure)</span>
          </label>

          {testResult && (
            <div
              className={`flex items-start gap-2 p-3 rounded-md text-xs ${
                testResult.ok
                  ? 'bg-status-success/10 text-status-success'
                  : 'bg-status-error/10 text-status-error'
              }`}
            >
              {testResult.ok ? (
                <CheckCircle2 className="h-4 w-4 mt-0.5 shrink-0" />
              ) : (
                <AlertCircle className="h-4 w-4 mt-0.5 shrink-0" />
              )}
              <span className="font-mono">{testResult.message || (testResult.ok ? 'Connection OK' : 'Failed')}</span>
            </div>
          )}
    </ModalShell>
  );
}
