import { createFileRoute } from '@tanstack/react-router';
/**
 * Storage location wizard.
 *
 * Walks the admin through five steps to register a Velero
 * BackupStorageLocation:
 *   1. Name + cluster select
 *   2. Backend type radio
 *   3. Connection details (bucket / region / endpoint / credentials)
 *   4. Live test connection — proceed allowed even on failure with a warning
 *   5. Review & create
 *
 * The wizard creates the row first (so we have an id), then immediately
 * calls `POST /backups/storage/{id}/test` for the probe step. If the user
 * abandons the wizard mid-flow the row is left in place — they can retest
 * or edit it from the overview page; the alternative (probe-before-create)
 * would force credentials to bounce through a separate endpoint, doubling
 * the number of routes and the secret handling surface.
 */

import { useMemo, useState } from 'react';
import { useRouter } from '@/lib/navigation';
import {
  AlertTriangle,
  ArrowLeft,
  Check,
  CheckCircle2,
  Cloud,
  HardDrive,
  Loader2,
  ServerCog,
  XCircle,
} from 'lucide-react';
import { useClusters } from '@/lib/hooks';
import {
  useB2CreateStorageLocation,
  useB2TestStorageLocation,
} from '@/components/backups/hooks';
import { cn } from '@/lib/utils';
import type {
  BackupBackendKind,
  BackupStorageType,
  TestStorageResult,
} from '@/types';

interface FormState {
  name: string;
  clusterId: string;
  backend: BackupBackendKind;
  bucket: string;
  prefix: string;
  region: string;
  endpointUrl: string;
  accessKey: string;
  secretKey: string;
  isDefault: boolean;
}

const BACKENDS: {
  key: BackupBackendKind;
  label: string;
  description: string;
  icon: React.ElementType;
}[] = [
  { key: 's3', label: 'Amazon S3', description: 'AWS S3 with native SigV4', icon: Cloud },
  { key: 'gcs', label: 'Google Cloud Storage', description: 'GCS with HMAC keys', icon: Cloud },
  { key: 'azure', label: 'Azure Blob', description: 'Azure storage account container', icon: Cloud },
  {
    key: 's3-compatible',
    label: 'S3-compatible',
    description: 'MinIO, Cloudflare R2, Wasabi, etc.',
    icon: ServerCog,
  },
];

/** Map the wizard's backend selector to the wire `storage_type`. The
 *  s3-compatible branch is just `s3` with a populated `endpoint_url`; the
 *  Velero AWS plugin handles both. */
function wireStorageType(b: BackupBackendKind): BackupStorageType {
  if (b === 'gcs') return 'gcs';
  if (b === 'azure') return 'azure';
  // 's3' and 's3-compatible' both serialize as 's3' on the wire.
  return 's3';
}

const STEPS = ['Identity', 'Backend', 'Connection', 'Test', 'Review'] as const;
type Step = number;

function StorageWizardPage() {
  const router = useRouter();
  const clustersQ = useClusters({ pageSize: 100 });
  const create = useB2CreateStorageLocation();
  const test = useB2TestStorageLocation();

  const [step, setStep] = useState<Step>(0);
  const [form, setForm] = useState<FormState>({
    name: '',
    clusterId: '',
    backend: 's3',
    bucket: '',
    prefix: '',
    region: '',
    endpointUrl: '',
    accessKey: '',
    secretKey: '',
    isDefault: false,
  });
  const [createdId, setCreatedId] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<TestStorageResult | null>(null);
  const [testRan, setTestRan] = useState(false);

  const update = <K extends keyof FormState>(k: K, v: FormState[K]) =>
    setForm((f) => ({ ...f, [k]: v }));

  const clusters = clustersQ.data?.data ?? [];

  const stepValid = useMemo(() => {
    switch (step) {
      case 0:
        return form.name.trim().length > 0 && form.clusterId.length > 0;
      case 1:
        return true;
      case 2: {
        if (!form.bucket.trim()) return false;
        if (form.backend === 's3' || form.backend === 's3-compatible') {
          if (!form.region.trim()) return false;
          if (form.backend === 's3-compatible' && !form.endpointUrl.trim()) return false;
        }
        return true;
      }
      default:
        return true;
    }
  }, [step, form]);

  const handleCreateAndTest = async () => {
    try {
      const created = await create.mutateAsync({
        name: form.name.trim(),
        cluster_id: form.clusterId,
        storage_type: wireStorageType(form.backend),
        bucket: form.bucket.trim(),
        prefix: form.prefix.trim() || undefined,
        region: form.region.trim() || undefined,
        endpoint_url: form.endpointUrl.trim() || undefined,
        access_key: form.accessKey || undefined,
        secret_key: form.secretKey || undefined,
        is_default: form.isDefault,
      });
      setCreatedId(created.id);
      try {
        const result = await test.mutateAsync(created.id);
        setTestResult(result);
      } catch (err) {
        setTestResult({ success: false, message: (err as Error).message || 'Probe failed' });
      } finally {
        setTestRan(true);
      }
    } catch {
      /* hook surfaces the error toast */
    }
  };

  const handleNext = async () => {
    if (step === 2) {
      // Step 2 → Step 3: create the row server-side, then probe.
      if (!createdId) await handleCreateAndTest();
      setStep(3);
      return;
    }
    if (step === STEPS.length - 1) {
      // Final review → land on the overview.
      router.push('/dashboard/backups?tab=storage');
      return;
    }
    setStep((s) => (s + 1) as Step);
  };

  const handleBack = () => {
    if (step === 0) {
      router.push('/dashboard/backups');
      return;
    }
    setStep((s) => (s - 1) as Step);
  };

  return (
    <div className="max-w-3xl mx-auto space-y-6">
      <div>
        <button
          onClick={() => router.push('/dashboard/backups')}
          className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors mb-2"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to backups
        </button>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight">Add Storage Location</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Register a Velero BackupStorageLocation on the cluster Velero is running on.
        </p>
      </div>

      {/* Step indicator */}
      <ol className="flex items-center gap-2">
        {STEPS.map((label, i) => {
          const done = i < step;
          const current = i === step;
          return (
            <li key={label} className="flex-1 flex items-center gap-2">
              <span
                className={cn(
                  'flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-full text-xs font-medium border transition-colors',
                  done && 'bg-status-success/10 border-status-success text-status-success',
                  current && 'bg-primary text-primary-foreground border-primary',
                  !done && !current && 'bg-muted text-muted-foreground border-border',
                )}
              >
                {done ? <Check className="h-3.5 w-3.5" /> : i + 1}
              </span>
              <span
                className={cn(
                  'text-xs font-medium',
                  current ? 'text-foreground' : 'text-muted-foreground',
                )}
              >
                {label}
              </span>
              {i < STEPS.length - 1 && <span className="flex-1 h-px bg-border" />}
            </li>
          );
        })}
      </ol>

      <div className="rounded-xl border border-border bg-card p-6 animate-fade-in">
        {step === 0 && (
          <div className="space-y-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Name</label>
              <input
                type="text"
                value={form.name}
                onChange={(e) => update('name', e.target.value)}
                placeholder="prod-s3-backups"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
              <p className="text-xs text-muted-foreground">
                A friendly identifier shown across the dashboard.
              </p>
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Cluster</label>
              <select
                value={form.clusterId}
                onChange={(e) => update('clusterId', e.target.value)}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  focus:outline-none focus:ring-1 focus:ring-ring"
              >
                <option value="">Select a cluster…</option>
                {clusters.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.displayName || c.name}
                  </option>
                ))}
              </select>
              <p className="text-xs text-muted-foreground">
                Velero must already be installed on this cluster. Backups originate from
                here.
              </p>
            </div>
            <label className="flex items-center gap-2 cursor-pointer">
              <input
                type="checkbox"
                checked={form.isDefault}
                onChange={(e) => update('isDefault', e.target.checked)}
                className="rounded border-border text-primary focus:ring-ring"
              />
              <span className="text-sm text-foreground">Mark as default storage location</span>
            </label>
          </div>
        )}

        {step === 1 && (
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">
              Select the storage backend. S3-compatible covers MinIO, R2, Wasabi, and
              other endpoints that speak the AWS S3 API.
            </p>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              {BACKENDS.map((b) => {
                const selected = form.backend === b.key;
                const Icon = b.icon;
                return (
                  <button
                    key={b.key}
                    type="button"
                    onClick={() => update('backend', b.key)}
                    className={cn(
                      'flex items-start gap-3 p-4 rounded-lg border text-left transition-colors',
                      selected
                        ? 'border-primary bg-primary/5'
                        : 'border-border bg-background hover:border-foreground/40',
                    )}
                  >
                    <Icon
                      className={cn(
                        'h-5 w-5 mt-0.5 flex-shrink-0',
                        selected ? 'text-primary' : 'text-muted-foreground',
                      )}
                    />
                    <div className="min-w-0">
                      <p className="text-sm font-medium text-foreground">{b.label}</p>
                      <p className="text-xs text-muted-foreground mt-0.5">{b.description}</p>
                    </div>
                  </button>
                );
              })}
            </div>
          </div>
        )}

        {step === 2 && (
          <div className="space-y-4">
            <Field
              label="Bucket"
              required
              value={form.bucket}
              onChange={(v) => update('bucket', v)}
              placeholder="astronomer-velero-prod"
            />
            <Field
              label="Prefix (optional)"
              value={form.prefix}
              onChange={(v) => update('prefix', v)}
              placeholder="cluster-a/"
            />
            {(form.backend === 's3' || form.backend === 's3-compatible') && (
              <Field
                label="Region"
                required
                value={form.region}
                onChange={(v) => update('region', v)}
                placeholder="us-east-1"
              />
            )}
            {form.backend === 's3-compatible' && (
              <Field
                label="Endpoint URL"
                required
                value={form.endpointUrl}
                onChange={(v) => update('endpointUrl', v)}
                placeholder="https://minio.example.com"
              />
            )}
            <Field
              label="Access Key"
              value={form.accessKey}
              onChange={(v) => update('accessKey', v)}
              placeholder={form.backend === 'gcs' ? 'HMAC access ID' : 'AKIAIOSFODNN7EXAMPLE'}
              type="text"
            />
            <Field
              label="Secret Key"
              value={form.secretKey}
              onChange={(v) => update('secretKey', v)}
              placeholder="••••••••"
              type="password"
            />
            <p className="text-xs text-muted-foreground">
              Credentials are encrypted at rest with the platform's Fernet key and never
              re-emitted by the API.
            </p>
          </div>
        )}

        {step === 3 && (
          <div className="space-y-4">
            <p className="text-sm text-muted-foreground">
              The backend ran a real reachability probe against the bucket using the
              credentials you just supplied.
            </p>
            {!testRan && (
              <div className="flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
                Running probe…
              </div>
            )}
            {testRan && testResult && (
              <div
                className={cn(
                  'rounded-lg border p-4 space-y-2',
                  testResult.success
                    ? 'border-status-success/30 bg-status-success/5'
                    : 'border-status-warning/30 bg-status-warning/5',
                )}
              >
                <div className="flex items-center gap-2">
                  {testResult.success ? (
                    <CheckCircle2 className="h-4 w-4 text-status-success" />
                  ) : (
                    <XCircle className="h-4 w-4 text-status-warning" />
                  )}
                  <span className="text-sm font-medium text-foreground">
                    {testResult.success ? 'Bucket is reachable' : 'Probe failed'}
                  </span>
                </div>
                <p className="text-xs text-muted-foreground font-mono">
                  {testResult.message || (testResult.success ? 'OK' : 'No detail provided')}
                </p>
                {!testResult.success && (
                  <p className="text-xs text-status-warning flex items-start gap-1">
                    <AlertTriangle className="h-3.5 w-3.5 flex-shrink-0 mt-0.5" />
                    You can still proceed — Velero will surface the underlying error
                    when the BackupStorageLocation reconciles.
                  </p>
                )}
                <button
                  onClick={async () => {
                    if (!createdId) return;
                    setTestRan(false);
                    try {
                      const result = await test.mutateAsync(createdId);
                      setTestResult(result);
                    } catch (err) {
                      setTestResult({ success: false, message: (err as Error).message });
                    } finally {
                      setTestRan(true);
                    }
                  }}
                  disabled={!createdId || test.isPending}
                  className="text-xs text-muted-foreground hover:text-foreground underline-offset-2 hover:underline transition-colors disabled:opacity-50"
                >
                  Run probe again
                </button>
              </div>
            )}
          </div>
        )}

        {step === 4 && (
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">
              Final review. The storage location was created in step 3 and is already
              available — clicking <span className="text-foreground font-medium">Finish</span>
              just returns you to the overview.
            </p>
            <dl className="grid grid-cols-2 gap-3 text-sm">
              <Summary k="Name" v={form.name} />
              <Summary
                k="Cluster"
                v={clusters.find((c) => c.id === form.clusterId)?.displayName ?? form.clusterId}
              />
              <Summary k="Backend" v={BACKENDS.find((b) => b.key === form.backend)?.label ?? form.backend} />
              <Summary k="Bucket" v={form.bucket} mono />
              {form.prefix && <Summary k="Prefix" v={form.prefix} mono />}
              {form.region && <Summary k="Region" v={form.region} mono />}
              {form.endpointUrl && <Summary k="Endpoint" v={form.endpointUrl} mono />}
              <Summary k="Default" v={form.isDefault ? 'Yes' : 'No'} />
              <Summary k="Probe" v={testResult?.success ? 'Reachable' : testResult ? 'Not reachable' : '—'} />
            </dl>
          </div>
        )}
      </div>

      {/* Footer */}
      <div className="flex items-center justify-between">
        <button
          onClick={handleBack}
          disabled={create.isPending}
          className="inline-flex items-center gap-1.5 h-9 px-4 rounded-lg border border-border text-sm font-medium
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          {step === 0 ? 'Cancel' : 'Back'}
        </button>
        <button
          onClick={handleNext}
          disabled={!stepValid || create.isPending || (step === 3 && !testRan)}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
            text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {(create.isPending || (step === 3 && !testRan)) && (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          )}
          {step === STEPS.length - 1
            ? 'Finish'
            : step === 2
              ? 'Create & Test'
              : 'Continue'}
        </button>
      </div>

      {clustersQ.isLoading && (
        <p className="text-xs text-muted-foreground flex items-center gap-1.5">
          <HardDrive className="h-3 w-3" />
          Loading clusters…
        </p>
      )}
    </div>
  );
}

function Field({
  label,
  value,
  onChange,
  placeholder,
  required,
  type = 'text',
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  required?: boolean;
  type?: 'text' | 'password';
}) {
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">
        {label}
        {required && <span className="text-status-error ml-0.5">*</span>}
      </label>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        autoComplete={type === 'password' ? 'new-password' : 'off'}
        className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
          placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
      />
    </div>
  );
}

function Summary({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <>
      <dt className="text-xs text-muted-foreground">{k}</dt>
      <dd className={cn('text-sm text-foreground', mono && 'font-mono')}>{v}</dd>
    </>
  );
}

export const Route = createFileRoute('/dashboard/backups/storage/new/')({
  component: StorageWizardPage,
});
