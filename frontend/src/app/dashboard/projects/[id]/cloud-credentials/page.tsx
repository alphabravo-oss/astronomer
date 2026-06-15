'use client';

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * Project · Cloud Credentials tab — list view.
 *
 * One row per credential with inline Edit / Test / Delete actions. New
 * credentials are created via the multi-step wizard at `./new`; editing
 * pre-loads the same wizard from `./[credId]/edit`.
 *
 * The Test action calls `POST .../cloud-credentials/{id}/test/` and
 * surfaces success/failure inline next to the row rather than a toast,
 * since the operator usually wants the full error string when an AWS / GCP
 * key gets rejected.
 */
import { use, useState } from 'react';
import { Link } from '@/lib/link';
import { useRouter } from '@/lib/navigation';
import {
  Plus,
  Loader2,
  Trash2,
  PencilLine,
  Check,
  X,
  AlertCircle,
} from 'lucide-react';
import {
  useProjectCloudCredentials,
  useDeleteCloudCredential,
  useTestCloudCredential,
  canEditProject,
} from '@/components/projects/hooks';
import { useCurrentUser } from '@/lib/hooks';
import { ProviderBadge } from '@/components/projects/cloud-credentials/provider-badge';
import type { CloudCredential, CloudCredentialTestResult } from '@/lib/api/project-detail';
import { cn, formatRelativeTime } from '@/lib/utils';

interface ListPageProps {
  params: Promise<{ id: string }>;
}

export default function CloudCredentialsListPage({ params }: ListPageProps) {
  const { id: projectId } = use(params);
  const router = useRouter();
  const { data: user } = useCurrentUser();
  const canEdit = canEditProject(user);

  const { data: credentials = [], isLoading } = useProjectCloudCredentials(projectId);
  const deleteMutation = useDeleteCloudCredential(projectId);
  const testMutation = useTestCloudCredential(projectId);

  // Per-row test result. We keep this local to the page so the cache key
  // stays clean (the test mutation isn't a query — its output is ephemeral).
  const [testResults, setTestResults] = useState<Record<string, CloudCredentialTestResult>>({});
  const [testingId, setTestingId] = useState<string | null>(null);

  const handleTest = async (cred: CloudCredential) => {
    setTestingId(cred.id);
    try {
      const result = await testMutation.mutateAsync(cred.id);
      setTestResults((prev) => ({ ...prev, [cred.id]: result }));
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Test failed';
      setTestResults((prev) => ({ ...prev, [cred.id]: { ok: false, message } }));
    } finally {
      setTestingId(null);
    }
  };

  const handleDelete = (cred: CloudCredential) => {
    if (!confirm(`Delete cloud credential "${cred.name}"? This action cannot be undone.`)) return;
    deleteMutation.mutate(cred.id);
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          Cloud-provider credentials materialized into the listed clusters as Secrets.
        </p>
        {canEdit && (
          <Link
            href={`/dashboard/projects/${projectId}/cloud-credentials/new`}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
          >
            <Plus className="h-4 w-4" />
            New credential
          </Link>
        )}
      </div>

      {isLoading ? (
        <div className="flex items-center justify-center h-32">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      ) : credentials.length === 0 ? (
        <div className="rounded-xl border border-border bg-card p-8 text-center space-y-2">
          <p className="text-sm text-foreground">No cloud credentials yet.</p>
          <p className="text-xs text-muted-foreground">
            Add one to give workloads in this project access to cloud services.
          </p>
        </div>
      ) : (
        <div className="rounded-xl border border-border bg-card overflow-hidden">
          <Table className="w-full text-sm">
            <TableHeader>
              <TableRow className="text-xs text-muted-foreground border-b border-border bg-muted/30">
                <TableHead className="text-left font-medium py-2 px-3">Name</TableHead>
                <TableHead className="text-left font-medium py-2 px-3">Provider</TableHead>
                <TableHead className="text-left font-medium py-2 px-3">Targets</TableHead>
                <TableHead className="text-left font-medium py-2 px-3">Created by</TableHead>
                <TableHead className="text-left font-medium py-2 px-3">Test</TableHead>
                <TableHead className="text-right font-medium py-2 px-3 pr-4">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {credentials.map((cred) => {
                const result = testResults[cred.id];
                const testing = testingId === cred.id;
                return (
                  <TableRow key={cred.id} className="border-b border-border last:border-0 hover:bg-accent/20">
                    <TableCell className="py-2 px-3">
                      <div>
                        <p className="text-sm font-medium text-foreground">{cred.name}</p>
                        {cred.description && (
                          <p className="text-xs text-muted-foreground truncate max-w-[260px]">
                            {cred.description}
                          </p>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="py-2 px-3">
                      <ProviderBadge provider={cred.provider} />
                    </TableCell>
                    <TableCell className="py-2 px-3">
                      <span className="text-xs text-muted-foreground tabular-nums">
                        {cred.targetRefs.length} cluster
                        {cred.targetRefs.length === 1 ? '' : 's'}
                      </span>
                    </TableCell>
                    <TableCell className="py-2 px-3">
                      <span className="text-xs text-muted-foreground">
                        {cred.createdBy || '—'}
                        <br />
                        <span className="text-2xs">{formatRelativeTime(cred.createdAt)}</span>
                      </span>
                    </TableCell>
                    <TableCell className="py-2 px-3">
                      <button
                        type="button"
                        onClick={() => handleTest(cred)}
                        disabled={testing}
                        className="inline-flex items-center gap-1.5 h-7 px-2 rounded border border-border text-xs hover:bg-accent transition-colors disabled:opacity-50"
                      >
                        {testing ? (
                          <Loader2 className="h-3 w-3 animate-spin" />
                        ) : result?.ok ? (
                          <Check className="h-3 w-3 text-status-success" />
                        ) : result && !result.ok ? (
                          <X className="h-3 w-3 text-status-error" />
                        ) : (
                          <AlertCircle className="h-3 w-3 text-muted-foreground" />
                        )}
                        Test
                      </button>
                      {result && (
                        <p
                          className={cn(
                            'text-2xs mt-0.5 max-w-[200px] truncate',
                            result.ok ? 'text-status-success' : 'text-status-error',
                          )}
                          title={result.message || result.detail}
                        >
                          {result.ok
                            ? result.message || 'Credential valid'
                            : result.message || 'Test failed'}
                        </p>
                      )}
                    </TableCell>
                    <TableCell className="py-2 px-3 pr-4">
                      <div className="flex items-center justify-end gap-1">
                        {canEdit && (
                          <button
                            type="button"
                            onClick={() =>
                              router.push(
                                `/dashboard/projects/${projectId}/cloud-credentials/${cred.id}/edit`,
                              )
                            }
                            className="p-1.5 rounded text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                            title="Edit credential"
                          >
                            <PencilLine className="h-3.5 w-3.5" />
                          </button>
                        )}
                        {canEdit && (
                          <button
                            type="button"
                            onClick={() => handleDelete(cred)}
                            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
                            title="Delete credential"
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}
