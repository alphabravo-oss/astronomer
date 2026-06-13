'use client';

/**
 * Cluster Service Mesh tab — Istio / Linkerd / Kuma / Cilium-mesh detection.
 *
 * Backend (sprint 071) populates cluster_service_mesh on a 5m worker cadence
 * plus on-demand via POST /service-mesh/detect/. This page just renders the
 * row and lets the operator click "Re-detect" for immediate feedback. Read-
 * only — install goes through the existing catalog deep-link with
 * ?tag=service-mesh.
 *
 * RBAC: clusters:read on the backend; the page assumes the caller is past
 * the auth gate (same as the snapshots page).
 */

import Link from 'next/link';
import { useParams } from 'next/navigation';
import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import {
  AlertTriangle,
  CheckCircle2,
  Loader2,
  Network,
  Plus,
  RefreshCw,
  Server,
  Shield,
} from 'lucide-react';

import { useCluster } from '@/lib/hooks';
import {
  getServiceMeshInventory,
  getServiceMeshDetection,
  reDetectServiceMesh,
  validateServiceMeshPolicy,
  type ServiceMeshDetection,
  type ServiceMeshInventory,
  type ServiceMeshPolicyValidation,
  type ServiceMeshKind,
} from '@/lib/api/cluster-detail';

const qk = {
  detection: (id: string) => ['clusters', id, 'service-mesh'] as const,
  inventory: (id: string) => ['clusters', id, 'service-mesh', 'inventory'] as const,
};

// meshLabel maps the backend enum to a human-readable string. Kept as a
// pure mapping (no JSX) so it can be reused in headers + tile labels.
function meshLabel(kind: ServiceMeshKind): string {
  switch (kind) {
    case 'istio':
      return 'Istio';
    case 'linkerd':
      return 'Linkerd';
    case 'kuma':
      return 'Kuma';
    case 'cilium':
      return 'Cilium Mesh';
    case 'none':
      return 'No mesh installed';
    default:
      return 'Detection pending';
  }
}

// meshAccent picks a tailwind-friendly accent class per mesh so the hero
// card visually distinguishes between meshes without an icon library.
function meshAccent(kind: ServiceMeshKind): string {
  switch (kind) {
    case 'istio':
      return 'text-blue-500';
    case 'linkerd':
      return 'text-emerald-500';
    case 'kuma':
      return 'text-purple-500';
    case 'cilium':
      return 'text-amber-500';
    case 'none':
      return 'text-muted-foreground';
    default:
      return 'text-muted-foreground';
  }
}

// ─── Detection hero card ────────────────────────────────────────────────────
function HeroCard({ detection, clusterId }: { detection: ServiceMeshDetection; clusterId: string }) {
  const isInstalled = detection.detectedMesh !== 'none' && detection.detectedMesh !== 'unknown';
  return (
    <div className="rounded-lg border border-border bg-card p-6">
      <div className="flex items-start justify-between gap-4">
        <div className="flex items-start gap-4">
          <div className="rounded-md bg-accent/30 p-2.5">
            <Network className={`h-6 w-6 ${meshAccent(detection.detectedMesh)}`} />
          </div>
          <div>
            <h2 className="text-lg font-semibold text-foreground">
              {meshLabel(detection.detectedMesh)}
            </h2>
            <p className="text-sm text-muted-foreground mt-1">
              {isInstalled ? (
                <>
                  {detection.detectedVersion ? (
                    <>
                      Version <span className="font-mono">{detection.detectedVersion}</span>
                      {detection.controlPlaneNamespace && (
                        <>
                          {' '} at <span className="font-mono">{detection.controlPlaneNamespace}</span>
                        </>
                      )}
                    </>
                  ) : detection.controlPlaneNamespace ? (
                    <>Control plane in <span className="font-mono">{detection.controlPlaneNamespace}</span></>
                  ) : (
                    'Detected — version unknown'
                  )}
                </>
              ) : detection.detectedMesh === 'none' ? (
                'No service mesh detected on this cluster.'
              ) : (
                'No detection has run yet; click Re-detect to populate.'
              )}
            </p>
            {detection.lastError && (
              <p className="text-xs text-amber-500 mt-2 flex items-start gap-1.5">
                <AlertTriangle className="h-3.5 w-3.5 flex-shrink-0 mt-0.5" />
                <span>{detection.lastError}</span>
              </p>
            )}
          </div>
        </div>
        {!isInstalled && (
          <Link
            href={`/dashboard/clusters/${clusterId}/apps?section=browse&install=istio-base`}
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded text-sm font-medium
              bg-primary text-primary-foreground hover:bg-primary/90 transition-colors flex-shrink-0"
          >
            <Plus className="h-3.5 w-3.5" />
            Install a mesh
          </Link>
        )}
      </div>
    </div>
  );
}

// ─── Health tile (one of the 4-grid items) ──────────────────────────────────
function HealthTile({
  label,
  value,
  suffix,
  hint,
}: {
  label: string;
  value: number | string;
  suffix?: string;
  hint?: string;
}) {
  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">{label}</p>
      <p className="text-2xl font-semibold text-foreground mt-1">
        {value}
        {suffix && <span className="text-sm font-normal text-muted-foreground ml-1">{suffix}</span>}
      </p>
      {hint && <p className="text-xs text-muted-foreground mt-1">{hint}</p>}
    </div>
  );
}

function InventoryPanel({
  inventory,
  loading,
}: {
  inventory?: ServiceMeshInventory;
  loading: boolean;
}) {
  if (loading) {
    return (
      <div className="rounded-lg border border-border bg-card p-5 flex items-center justify-center h-36">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (!inventory) return null;
  return (
    <div className="rounded-lg border border-border bg-card overflow-hidden">
      <div className="px-5 py-4 border-b border-border flex items-center justify-between gap-4">
        <div>
          <h2 className="text-sm font-semibold text-foreground">Mesh resources</h2>
          <p className="text-xs text-muted-foreground mt-1">{inventory.totalCount} resources tracked</p>
        </div>
        {inventory.notice && (
          <span className="text-xs text-amber-500 text-right max-w-md">{inventory.notice}</span>
        )}
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead className="bg-muted/40 text-xs text-muted-foreground">
            <tr>
              <th className="text-left font-medium px-5 py-2.5">Kind</th>
              <th className="text-left font-medium px-5 py-2.5">API</th>
              <th className="text-right font-medium px-5 py-2.5">Count</th>
              <th className="text-left font-medium px-5 py-2.5">Objects</th>
              <th className="text-left font-medium px-5 py-2.5">Ownership</th>
            </tr>
          </thead>
          <tbody>
            {inventory.resources.map((resource) => {
              const argoOwned = resource.items.filter((item) => item.readOnly).length;
              const preview = resource.items.slice(0, 3);
              return (
                <tr key={resource.kind} className="border-t border-border">
                  <td className="px-5 py-3 font-medium text-foreground whitespace-nowrap">
                    {resource.kind}
                  </td>
                  <td className="px-5 py-3 text-xs text-muted-foreground font-mono whitespace-nowrap">
                    {resource.apiVersion}
                  </td>
                  <td className="px-5 py-3 text-right tabular-nums text-foreground">
                    {resource.count}
                  </td>
                  <td className="px-5 py-3 min-w-64">
                    {resource.count === 0 ? (
                      <span className="text-xs text-muted-foreground">{resource.notice || 'None'}</span>
                    ) : (
                      <div className="flex flex-wrap gap-1.5">
                        {preview.map((item) => (
                          <span
                            key={`${resource.kind}:${item.namespace || '_'}:${item.name}`}
                            className="inline-flex items-center rounded border border-border px-2 py-1 text-xs text-foreground"
                            title={item.reason || undefined}
                          >
                            {item.namespace && (
                              <span className="text-muted-foreground mr-1">{item.namespace}/</span>
                            )}
                            {item.name}
                          </span>
                        ))}
                        {resource.count > preview.length && (
                          <span className="text-xs text-muted-foreground px-1 py-1">
                            +{resource.count - preview.length}
                          </span>
                        )}
                      </div>
                    )}
                  </td>
                  <td className="px-5 py-3 whitespace-nowrap">
                    {argoOwned > 0 ? (
                      <span className="inline-flex items-center gap-1 rounded bg-amber-500/10 px-2 py-1 text-xs text-amber-500">
                        <Shield className="h-3 w-3" />
                        {argoOwned} GitOps owned
                      </span>
                    ) : (
                      <span className="text-xs text-muted-foreground">Direct edit allowed</span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function PolicyValidationPanel({
  value,
  onChange,
  result,
  validating,
  onValidate,
}: {
  value: string;
  onChange: (value: string) => void;
  result?: ServiceMeshPolicyValidation;
  validating: boolean;
  onValidate: () => void;
}) {
  const findings = result ? [...result.errors, ...result.warnings] : [];
  return (
    <div className="rounded-lg border border-border bg-card overflow-hidden">
      <div className="px-5 py-4 border-b border-border flex items-center justify-between gap-4">
        <div>
          <h2 className="text-sm font-semibold text-foreground">Policy validation</h2>
          {result && (
            <p className="text-xs text-muted-foreground mt-1">
              {result.kind || 'Object'} {result.namespace ? `${result.namespace}/` : ''}
              {result.name || ''}
            </p>
          )}
        </div>
        <button
          type="button"
          onClick={onValidate}
          disabled={validating || value.trim().length === 0}
          className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
            border border-border text-foreground hover:bg-accent transition-colors
            disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {validating ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <CheckCircle2 className="h-3.5 w-3.5" />}
          Validate
        </button>
      </div>
      <div className="grid gap-0 lg:grid-cols-[minmax(0,1fr)_360px]">
        <textarea
          value={value}
          onChange={(event) => onChange(event.target.value)}
          spellCheck={false}
          className="min-h-72 w-full resize-y bg-background p-4 font-mono text-xs text-foreground outline-none border-b lg:border-b-0 lg:border-r border-border"
          placeholder={`apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: payments
  namespace: payments
spec:
  hosts:
    - payments.example.com`}
        />
        <div className="p-4 space-y-3">
          {result ? (
            <>
              <div
                className={`rounded border px-3 py-2 text-xs ${
                  result.applyAllowed
                    ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-500'
                    : 'border-amber-500/30 bg-amber-500/10 text-amber-500'
                }`}
              >
                {result.applyAllowed
                  ? 'Validation passed'
                  : result.readOnly
                    ? 'GitOps-owned resource'
                    : 'Validation requires changes'}
              </div>
              {findings.length > 0 ? (
                <div className="space-y-2">
                  {findings.map((finding, index) => (
                    <div
                      key={`${finding.field || 'finding'}:${index}`}
                      className="rounded border border-border p-3 text-xs"
                    >
                      <div className="font-medium text-foreground">
                        {finding.severity === 'error' ? 'Error' : 'Warning'}
                        {finding.field ? `: ${finding.field}` : ''}
                      </div>
                      <p className="text-muted-foreground mt-1 leading-relaxed">{finding.message}</p>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="text-xs text-muted-foreground">No validation findings.</p>
              )}
            </>
          ) : (
            <p className="text-xs text-muted-foreground">No validation result.</p>
          )}
        </div>
      </div>
    </div>
  );
}

// ─── Page ───────────────────────────────────────────────────────────────────
export default function ClusterServiceMeshPage() {
  const params = useParams();
  const clusterId = params.id as string;
  const queryClient = useQueryClient();
  const [policyYaml, setPolicyYaml] = useState('');
  const [validationResult, setValidationResult] = useState<ServiceMeshPolicyValidation | undefined>();

  const { data: cluster, isLoading: clusterLoading } = useCluster(clusterId);
  const { data: detection, isLoading: detLoading } = useQuery({
    queryKey: qk.detection(clusterId),
    queryFn: () => getServiceMeshDetection(clusterId),
    enabled: !!clusterId,
    refetchInterval: 60000,
    refetchIntervalInBackground: false,
  });
  const { data: inventory, isLoading: inventoryLoading } = useQuery({
    queryKey: qk.inventory(clusterId),
    queryFn: () => getServiceMeshInventory(clusterId),
    enabled: !!clusterId,
    refetchInterval: 60000,
    refetchIntervalInBackground: false,
  });

  const reDetect = useMutation({
    mutationFn: () => reDetectServiceMesh(clusterId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: qk.detection(clusterId) });
      queryClient.invalidateQueries({ queryKey: qk.inventory(clusterId) });
      toast.success('Service-mesh detection refreshed');
    },
    onError: (e: Error) => toast.error(`Re-detect failed: ${e.message}`),
  });
  const validatePolicy = useMutation({
    mutationFn: () => validateServiceMeshPolicy(clusterId, { yaml: policyYaml }),
    onSuccess: (result) => {
      setValidationResult(result);
      if (result.valid) {
        toast.success('Policy validation passed');
      } else {
        toast.warning('Policy validation returned findings');
      }
    },
    onError: (e: Error) => toast.error(`Validation failed: ${e.message}`),
  });

  if (clusterLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (!cluster) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
        <Server className="h-8 w-8 mb-3" />
        <p>Cluster not found</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Service mesh</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Detect and monitor the service mesh installed on {cluster.displayName}.
          </p>
        </div>
        <button
          onClick={() => reDetect.mutate()}
          disabled={reDetect.isPending}
          className="inline-flex items-center gap-1.5 h-9 px-3 rounded text-sm font-medium
            border border-border text-foreground hover:bg-accent transition-colors
            disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {reDetect.isPending ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <RefreshCw className="h-3.5 w-3.5" />
          )}
          Re-detect
        </button>
      </div>

      {/* Hero card */}
      {detLoading ? (
        <div className="rounded-lg border border-border bg-card p-6 flex items-center justify-center h-32">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      ) : detection ? (
        <HeroCard detection={detection} clusterId={clusterId} />
      ) : null}

      {/* Health 4-grid (Istio counts when Istio, Linkerd counts when Linkerd) */}
      {detection && detection.detectedMesh === 'istio' && (
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
          <HealthTile label="Gateways" value={detection.gatewayCount} />
          <HealthTile label="VirtualServices" value={detection.virtualServiceCount} />
          <HealthTile label="DestinationRules" value={detection.destinationRuleCount} />
          <HealthTile
            label="mTLS coverage"
            value={detection.mtlsCoveragePct}
            suffix="%"
            hint={detection.peerAuthenticationCount + ' PeerAuthentication rules'}
          />
        </div>
      )}
      {detection && detection.detectedMesh === 'linkerd' && (
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
          <HealthTile label="ServiceProfiles" value={detection.serviceProfileCount} />
          <HealthTile label="Servers" value={detection.serverAuthCount} />
          <HealthTile label="VirtualServices" value="—" />
          <HealthTile
            label="mTLS coverage"
            value={detection.mtlsCoveragePct}
            suffix="%"
            hint="Linkerd Server-level proxy auth"
          />
        </div>
      )}

      {/* mTLS breakdown link */}
      {detection && detection.detectedMesh !== 'none' && detection.detectedMesh !== 'unknown' && (
        <div className="rounded-lg border border-border bg-card p-4 flex items-center justify-between">
          <div className="flex items-start gap-3">
            <Shield className="h-5 w-5 text-emerald-500 flex-shrink-0 mt-0.5" />
            <div>
              <p className="text-sm font-medium text-foreground">mTLS posture</p>
              <p className="text-xs text-muted-foreground mt-0.5">
                {detection.mtlsCoveragePct}% of user namespaces covered.
              </p>
            </div>
          </div>
          <Link
            href={`/dashboard/clusters/${clusterId}/service-mesh/mtls/`}
            className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
              border border-border text-foreground hover:bg-accent transition-colors"
          >
            View breakdown
          </Link>
        </div>
      )}

      <InventoryPanel inventory={inventory} loading={inventoryLoading} />

      {detection && detection.detectedMesh !== 'none' && detection.detectedMesh !== 'unknown' && (
        <PolicyValidationPanel
          value={policyYaml}
          onChange={(value) => {
            setPolicyYaml(value);
            setValidationResult(undefined);
          }}
          result={validationResult}
          validating={validatePolicy.isPending}
          onValidate={() => validatePolicy.mutate()}
        />
      )}

      {/* "no mesh" install CTA already lives in HeroCard; surface a hint here too */}
      {detection && detection.detectedMesh === 'none' && (
        <div className="rounded-lg border border-border bg-card p-6 flex items-start gap-3">
          <CheckCircle2 className="h-5 w-5 text-muted-foreground flex-shrink-0 mt-0.5" />
          <div className="flex-1">
            <p className="text-sm font-medium text-foreground">No service mesh installed</p>
            <p className="text-xs text-muted-foreground mt-1">
              Use the catalog to install Istio, Linkerd, Kuma, or Cilium-mesh. The "Install a mesh"
              button above deep-links to the catalog filtered to service-mesh charts.
            </p>
          </div>
        </div>
      )}
    </div>
  );
}
