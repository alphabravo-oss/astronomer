'use client';

// ApplicationSet creation wizard. Four steps:
//
//   1. Name + project
//   2. Generator type — list / cluster (label-select against managed clusters) / git
//   3. Template — repo URL, path, target revision, destination namespace pattern
//   4. Review + create
//
// The "cluster" generator is the hero feature: it lets the operator pick a
// cluster label (from the labels stamped during cluster registration) and
// matching clusters get one Application each. We surface the labels we know
// about from `listArgoManagedClusters` so the operator never has to type
// them.

import { useMemo, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import {
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  Layers,
  Loader2,
  Plus,
  Trash2,
} from 'lucide-react';
import { listArgoManagedClusters, createArgoApplicationSet } from '@/lib/api';
import { queryKeys } from '@/lib/hooks';
import type {
  ArgoApplicationSetGenerator,
  ArgoCreateApplicationSetRequest,
  ArgoManagedCluster,
} from '@/types';

type GeneratorKind = 'list' | 'clusters' | 'git';
type ClusterSelectorPreset = 'all' | 'environment' | 'label' | 'canary' | 'custom';

interface WizardState {
 name: string;
 project: string;
 generatorKind: GeneratorKind;
  clusterSelectorPreset: ClusterSelectorPreset;
  // cluster generator fields
  clusterMatchLabels: { key: string; value: string }[];
  // git generator fields
  gitRepoURL: string;
  gitRevision: string;
  gitPath: string; // either a "directories" path or a "files" path with .json/.yaml
  gitMode: 'directories' | 'files';
  // list generator fields
  listElements: { name: string }[];
  // template
  tmplRepoURL: string;
  tmplPath: string;
  tmplTargetRevision: string;
  tmplDestServer: string;
  tmplDestNamespacePattern: string;
  tmplAutoSync: boolean;
}

const DEFAULT_STATE: WizardState = {
  name: '',
  project: 'default',
  generatorKind: 'clusters',
  clusterSelectorPreset: 'environment',
  clusterMatchLabels: [
    { key: 'astronomer.io/managed-by', value: 'astronomer' },
    { key: 'astronomer.io/environment', value: 'production' },
  ],
  gitRepoURL: '',
  gitRevision: 'HEAD',
  gitPath: 'apps/*',
  gitMode: 'directories',
  listElements: [{ name: 'cluster-1' }],
  tmplRepoURL: '',
  tmplPath: '{{path}}',
  tmplTargetRevision: 'HEAD',
  tmplDestServer: '{{server}}',
  tmplDestNamespacePattern: '{{name}}',
  tmplAutoSync: false,
};

export default function ApplicationSetWizardPage() {
  const params = useParams();
  const router = useRouter();
  const queryClient = useQueryClient();
  const instanceId = params.instanceId as string;

  const [step, setStep] = useState(1);
  const [state, setState] = useState<WizardState>(DEFAULT_STATE);

  const { data: managed = [] } = useQuery({
    queryKey: queryKeys.argocd.managedClusters(instanceId),
    queryFn: () => listArgoManagedClusters(instanceId),
  });

  // Aggregate the union of labels seen across our registered clusters so the
  // operator can pick from a known list rather than typing.
  const labelOptions = useMemo(() => collectLabelOptions(managed), [managed]);

  const create = useMutation({
    mutationFn: (body: ArgoCreateApplicationSetRequest) =>
      createArgoApplicationSet(instanceId, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.appsets(instanceId) });
      toastSuccess(`ApplicationSet ${state.name} created`);
      router.push(`/dashboard/argocd/${instanceId}`);
    },
    onError: (err: Error) => toastApiError('Create failed', err),
  });

  const submit = () => create.mutate(buildBody(state));

  return (
    <div className="space-y-6 max-w-3xl">
      <button
        onClick={() => router.push(`/dashboard/argocd/${instanceId}`)}
        className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3 w-3" />
        Back to instance
      </button>

      <div className="flex items-center gap-3">
        <div className="w-10 h-10 rounded-lg bg-muted flex items-center justify-center">
          <Layers className="h-5 w-5 text-muted-foreground" />
        </div>
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">
            New ApplicationSet
          </h1>
          <p className="text-sm text-muted-foreground">
            Fan a single template out across many clusters or git directories.
          </p>
        </div>
      </div>

      <Stepper step={step} />

      <div className="rounded-lg border border-border bg-card p-6">
        {step === 1 && <Step1 state={state} setState={setState} />}
        {step === 2 && <Step2 state={state} setState={setState} labelOptions={labelOptions} />}
        {step === 3 && <Step3 state={state} setState={setState} />}
        {step === 4 && <Step4 state={state} />}
      </div>

      <div className="flex items-center justify-between">
        <button
          onClick={() => setStep((s) => Math.max(1, s - 1))}
          disabled={step === 1 || create.isPending}
          className="inline-flex items-center gap-1 h-9 px-3 rounded-md text-sm
            text-muted-foreground hover:text-foreground hover:bg-accent disabled:opacity-50
            transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back
        </button>
        {step < 4 ? (
          <button
            onClick={() => setStep((s) => s + 1)}
            disabled={!canAdvance(state, step)}
            className="inline-flex items-center gap-1 h-9 px-4 rounded-md bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 disabled:opacity-50 transition-opacity"
          >
            Next
            <ArrowRight className="h-3.5 w-3.5" />
          </button>
        ) : (
          <button
            onClick={submit}
            disabled={create.isPending}
            className="inline-flex items-center gap-1.5 h-9 px-4 rounded-md bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 disabled:opacity-50 transition-opacity"
          >
            {create.isPending ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <CheckCircle2 className="h-3.5 w-3.5" />
            )}
            Create ApplicationSet
          </button>
        )}
      </div>
    </div>
  );
}

// ============================================================
// Steps
// ============================================================

function Stepper({ step }: { step: number }) {
  const labels = ['Identity', 'Generator', 'Template', 'Review'];
  return (
    <ol className="flex items-center gap-3 text-xs">
      {labels.map((label, i) => {
        const n = i + 1;
        const active = step === n;
        const done = step > n;
        return (
          <li key={n} className="flex items-center gap-2">
            <span
              className={`inline-flex items-center justify-center h-6 w-6 rounded-full text-xs font-medium ${
                done
                  ? 'bg-primary text-primary-foreground'
                  : active
                    ? 'bg-foreground text-background'
                    : 'bg-muted text-muted-foreground'
              }`}
            >
              {done ? <CheckCircle2 className="h-3.5 w-3.5" /> : n}
            </span>
            <span className={active || done ? 'text-foreground' : 'text-muted-foreground'}>
              {label}
            </span>
            {n < labels.length && <span className="text-muted-foreground">›</span>}
          </li>
        );
      })}
    </ol>
  );
}

interface StepProps {
  state: WizardState;
  setState: (s: WizardState) => void;
}

function Step1({ state, setState }: StepProps) {
  return (
    <div className="space-y-4">
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Name</label>
        <input
          type="text"
          value={state.name}
          onChange={(e) => setState({ ...state, name: e.target.value })}
          placeholder="prod-platform-stack"
          className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
            focus:outline-none focus:ring-1 focus:ring-ring"
        />
      </div>
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">AppProject</label>
        <input
          type="text"
          value={state.project}
          onChange={(e) => setState({ ...state, project: e.target.value })}
          className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
            focus:outline-none focus:ring-1 focus:ring-ring"
        />
        <p className="text-xs text-muted-foreground">
          Generated Applications inherit this project.
        </p>
      </div>
    </div>
  );
}

function Step2({
  state,
  setState,
  labelOptions,
}: StepProps & { labelOptions: { key: string; values: Set<string> }[] }) {
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-3 gap-2">
        {(['clusters', 'git', 'list'] as GeneratorKind[]).map((k) => (
          <button
            key={k}
            type="button"
            onClick={() => setState({ ...state, generatorKind: k })}
            className={`px-3 py-2 rounded-md text-sm font-medium transition-colors text-left ${
              state.generatorKind === k
                ? 'bg-primary text-primary-foreground'
                : 'bg-muted text-muted-foreground hover:text-foreground'
            }`}
          >
            <span className="block font-semibold capitalize">{k}</span>
            <span className="block text-2xs opacity-80">
              {k === 'clusters' && 'one Application per matching managed cluster'}
              {k === 'git' && 'one Application per directory or file in a repo'}
              {k === 'list' && 'one Application per element you list manually'}
            </span>
          </button>
        ))}
      </div>

      {state.generatorKind === 'clusters' && (
        <div className="space-y-2">
          <div className="flex flex-wrap gap-2">
            {([
              ['all', 'All adopted'],
              ['environment', 'Environment'],
              ['label', 'Label'],
              ['canary', 'Canary'],
            ] as const).map(([preset, label]) => (
              <button
                key={preset}
                type="button"
                onClick={() =>
                  setState({
                    ...state,
                    clusterSelectorPreset: preset,
                    clusterMatchLabels: clusterPresetLabels(preset, labelOptions),
                  })
                }
                className={`h-8 rounded border px-3 text-xs font-medium transition-colors ${
                  state.clusterSelectorPreset === preset
                    ? 'border-primary bg-primary text-primary-foreground'
                    : 'border-border text-muted-foreground hover:bg-accent hover:text-foreground'
                }`}
              >
                {label}
              </button>
            ))}
          </div>
          <label className="text-sm font-medium text-foreground">Cluster label match</label>
          {state.clusterMatchLabels.map((row, i) => {
            const choices = labelOptions.find((o) => o.key === row.key);
            return (
              <div key={i} className="flex items-center gap-2">
                <input
                  type="text"
                  list="argo-label-keys"
                  value={row.key}
                  onChange={(e) => {
                    const next = [...state.clusterMatchLabels];
                    next[i] = { ...next[i], key: e.target.value };
                    setState({ ...state, clusterSelectorPreset: 'custom', clusterMatchLabels: next });
                  }}
                  placeholder="astronomer.io/environment"
                  className="flex-1 h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                    focus:outline-none focus:ring-1 focus:ring-ring"
                />
                <select
                  value={row.value}
                  onChange={(e) => {
                    const next = [...state.clusterMatchLabels];
                    next[i] = { ...next[i], value: e.target.value };
                    setState({ ...state, clusterSelectorPreset: 'custom', clusterMatchLabels: next });
                  }}
                  className="h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                    focus:outline-none focus:ring-1 focus:ring-ring min-w-[140px]"
                >
                  <option value={row.value}>{row.value || '(value)'}</option>
                  {choices &&
                    Array.from(choices.values).map((v) => (
                      <option key={v} value={v}>
                        {v}
                      </option>
                    ))}
                </select>
                <button
                  onClick={() =>
                    setState({
                      ...state,
                      clusterSelectorPreset: 'custom',
                      clusterMatchLabels: state.clusterMatchLabels.filter((_, j) => j !== i),
                    })
                  }
                  className="p-2 text-muted-foreground hover:text-status-error transition-colors"
                >
                  <Trash2 className="h-4 w-4" />
                </button>
              </div>
            );
          })}
          <datalist id="argo-label-keys">
            {labelOptions.map((o) => (
              <option key={o.key} value={o.key} />
            ))}
          </datalist>
          <button
            onClick={() =>
              setState({
                ...state,
                clusterSelectorPreset: 'custom',
                clusterMatchLabels: [...state.clusterMatchLabels, { key: '', value: '' }],
              })
            }
            className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
          >
            <Plus className="h-3 w-3" /> Add label
          </button>
          <p className="text-xs text-muted-foreground">
            Cluster labels stamped at registration time. Available keys:{' '}
            <code>{labelOptions.map((o) => o.key).join(', ') || '(none yet)'}</code>
          </p>
        </div>
      )}

      {state.generatorKind === 'git' && (
        <div className="space-y-3">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Repository URL</label>
            <input
              type="text"
              value={state.gitRepoURL}
              onChange={(e) => setState({ ...state, gitRepoURL: e.target.value })}
              placeholder="https://github.com/org/manifests"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Revision</label>
              <input
                type="text"
                value={state.gitRevision}
                onChange={(e) => setState({ ...state, gitRevision: e.target.value })}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                  focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Match</label>
              <select
                value={state.gitMode}
                onChange={(e) =>
                  setState({ ...state, gitMode: e.target.value as 'directories' | 'files' })
                }
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  focus:outline-none focus:ring-1 focus:ring-ring"
              >
                <option value="directories">directories</option>
                <option value="files">files</option>
              </select>
            </div>
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Path Glob</label>
            <input
              type="text"
              value={state.gitPath}
              onChange={(e) => setState({ ...state, gitPath: e.target.value })}
              placeholder={state.gitMode === 'directories' ? 'apps/*' : 'config/**/values.yaml'}
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>
        </div>
      )}

      {state.generatorKind === 'list' && (
        <div className="space-y-2">
          <label className="text-sm font-medium text-foreground">Elements</label>
          {state.listElements.map((el, i) => (
            <div key={i} className="flex items-center gap-2">
              <input
                type="text"
                value={el.name}
                onChange={(e) => {
                  const next = [...state.listElements];
                  next[i] = { name: e.target.value };
                  setState({ ...state, listElements: next });
                }}
                placeholder="name"
                className="flex-1 h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                  focus:outline-none focus:ring-1 focus:ring-ring"
              />
              <button
                onClick={() =>
                  setState({
                    ...state,
                    listElements: state.listElements.filter((_, j) => j !== i),
                  })
                }
                className="p-2 text-muted-foreground hover:text-status-error transition-colors"
              >
                <Trash2 className="h-4 w-4" />
              </button>
            </div>
          ))}
          <button
            onClick={() =>
              setState({ ...state, listElements: [...state.listElements, { name: '' }] })
            }
            className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
          >
            <Plus className="h-3 w-3" /> Add element
          </button>
        </div>
      )}
    </div>
  );
}

function Step3({ state, setState }: StepProps) {
  return (
    <div className="space-y-4">
      <p className="text-xs text-muted-foreground">
        Generator parameters are exposed as <code>{'{{name}}'}</code>, <code>{'{{server}}'}</code>{' '}
        (and <code>{'{{path}}'}</code> for git). Use them in the fields below to template the
        generated Applications.
      </p>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Source Repo URL</label>
        <input
          type="text"
          value={state.tmplRepoURL}
          onChange={(e) => setState({ ...state, tmplRepoURL: e.target.value })}
          placeholder="https://github.com/org/charts"
          className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
            focus:outline-none focus:ring-1 focus:ring-ring"
        />
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Path</label>
          <input
            type="text"
            value={state.tmplPath}
            onChange={(e) => setState({ ...state, tmplPath: e.target.value })}
            className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
              focus:outline-none focus:ring-1 focus:ring-ring"
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Target Revision</label>
          <input
            type="text"
            value={state.tmplTargetRevision}
            onChange={(e) => setState({ ...state, tmplTargetRevision: e.target.value })}
            className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
              focus:outline-none focus:ring-1 focus:ring-ring"
          />
        </div>
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Destination Server</label>
          <input
            type="text"
            value={state.tmplDestServer}
            onChange={(e) => setState({ ...state, tmplDestServer: e.target.value })}
            className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
              focus:outline-none focus:ring-1 focus:ring-ring"
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Namespace Pattern</label>
          <input
            type="text"
            value={state.tmplDestNamespacePattern}
            onChange={(e) => setState({ ...state, tmplDestNamespacePattern: e.target.value })}
            className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
              focus:outline-none focus:ring-1 focus:ring-ring"
          />
        </div>
      </div>

      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={state.tmplAutoSync}
          onChange={(e) => setState({ ...state, tmplAutoSync: e.target.checked })}
          className="h-4 w-4 rounded border-border"
        />
        <span className="text-foreground">Enable automated sync on generated Applications</span>
      </label>
    </div>
  );
}

function Step4({ state }: { state: WizardState }) {
  const body = buildBody(state);
  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        Confirm the spec below. The backend will POST it to the upstream
        <code className="font-mono">/api/v1/applicationsets</code> endpoint.
      </p>
      <pre className="bg-muted text-xs font-mono p-4 rounded-md overflow-x-auto whitespace-pre-wrap">
        {JSON.stringify(body, null, 2)}
      </pre>
    </div>
  );
}

// ============================================================
// Helpers
// ============================================================

function canAdvance(s: WizardState, step: number): boolean {
  if (step === 1) return s.name.trim().length > 0;
  if (step === 2) {
    if (s.generatorKind === 'clusters')
      return s.clusterMatchLabels.length > 0 && s.clusterMatchLabels.every((l) => l.key && l.value);
    if (s.generatorKind === 'git') return s.gitRepoURL.trim().length > 0 && s.gitPath.trim().length > 0;
    if (s.generatorKind === 'list') return s.listElements.length > 0 && s.listElements.every((e) => e.name);
  }
  if (step === 3) return s.tmplRepoURL.trim().length > 0;
  return true;
}

function buildBody(s: WizardState): ArgoCreateApplicationSetRequest {
  const generator: ArgoApplicationSetGenerator = {};
  if (s.generatorKind === 'clusters') {
    const matchLabels: Record<string, string> = {};
    for (const r of s.clusterMatchLabels) {
      if (r.key) matchLabels[r.key] = r.value;
    }
    generator.clusters = { selector: { matchLabels } };
  } else if (s.generatorKind === 'git') {
    generator.git = {
      repoURL: s.gitRepoURL,
      revision: s.gitRevision,
      ...(s.gitMode === 'directories'
        ? { directories: [{ path: s.gitPath }] }
        : { files: [{ path: s.gitPath }] }),
    };
  } else {
    generator.list = { elements: s.listElements.map((e) => ({ name: e.name })) };
  }

  return {
    name: s.name.trim(),
    spec: {
      generators: [generator],
      template: {
        metadata: { name: '{{name}}' },
        spec: {
          project: s.project || 'default',
          source: {
            repoURL: s.tmplRepoURL,
            path: s.tmplPath,
            targetRevision: s.tmplTargetRevision,
          },
          destination: {
            server: s.tmplDestServer,
            namespace: s.tmplDestNamespacePattern,
          },
          ...(s.tmplAutoSync
            ? { syncPolicy: { automated: { prune: true, selfHeal: true } } }
            : {}),
        },
      },
    },
  };
}

function clusterPresetLabels(
  preset: Exclude<ClusterSelectorPreset, 'custom'>,
  labelOptions: { key: string; values: Set<string> }[],
): { key: string; value: string }[] {
  const guarded = [{ key: 'astronomer.io/managed-by', value: 'astronomer' }];
  if (preset === 'all') return guarded;
  if (preset === 'environment') {
    return [
      ...guarded,
      {
        key: 'astronomer.io/environment',
        value: firstKnownLabelValue(labelOptions, 'astronomer.io/environment', 'production'),
      },
    ];
  }
  if (preset === 'canary') {
    return [...guarded, { key: 'astronomer.io/label-canary', value: 'true' }];
  }
  const userLabel = labelOptions.find((option) => option.key.startsWith('astronomer.io/label-'));
  return [
    ...guarded,
    {
      key: userLabel?.key ?? 'astronomer.io/label-tier',
      value: firstSetValue(userLabel?.values) ?? 'prod',
    },
  ];
}

function firstKnownLabelValue(
  labelOptions: { key: string; values: Set<string> }[],
  key: string,
  fallback: string,
): string {
  const option = labelOptions.find((item) => item.key === key);
  return firstSetValue(option?.values) ?? fallback;
}

function firstSetValue(values?: Set<string>): string | undefined {
  if (!values) return undefined;
  for (const value of values) {
    if (value) return value;
  }
  return undefined;
}

function collectLabelOptions(
  managed: ArgoManagedCluster[],
): { key: string; values: Set<string> }[] {
  const map = new Map<string, Set<string>>();
  for (const m of managed) {
    for (const [k, v] of Object.entries(m.labels ?? {})) {
      if (!map.has(k)) map.set(k, new Set());
      map.get(k)!.add(v);
    }
  }
  return Array.from(map.entries()).map(([key, values]) => ({ key, values }));
}
