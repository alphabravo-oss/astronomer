/**
 * Lifecycle-screen tests for all three monitoring-stack families.
 *
 * The same six behaviours are asserted per family because the three surfaces
 * are one component driven by three specs — if the shared panel regresses, it
 * regresses everywhere, and if a family's spec is wrong (wrong required field,
 * wrong permission) only that family's run catches it.
 *
 * The last test in each family is the security-relevant one: the six endpoints
 * carry DIFFERENT permissions, and a caller holding only monitoring:read must
 * not be shown Install / Upgrade / Replace / Uninstall — while Preview, which
 * that grant really does allow, must stay.
 */
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('@/lib/toast', () => ({
  toastSuccess: vi.fn(),
  toastApiError: vi.fn(),
  toastError: vi.fn(),
  toastInfo: vi.fn(),
  toastWarning: vi.fn(),
  formatToastError: vi.fn(),
}));

// The real Link needs a RouterProvider; these tests assert page content.
vi.mock('@/lib/link', () => ({
  Link: ({ href, children, ...rest }: React.ComponentProps<'a'>) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

vi.mock('@/lib/hooks', () => ({
  useClusters: vi.fn(),
  useCluster: vi.fn(),
}));

vi.mock('@/components/backups/hooks', () => ({
  useB2StorageLocations: vi.fn(),
}));

vi.mock('@/lib/api/monitoring-stack', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api/monitoring-stack')>();
  return {
    ...actual,
    getStackStatus: vi.fn(),
    previewStack: vi.fn(),
    runStackLifecycle: vi.fn(),
    listMonitoringOperations: vi.fn(),
    getMonitoringOperation: vi.fn(),
    retryMonitoringOperation: vi.fn(),
  };
});

import {
  getMonitoringOperation,
  getStackStatus,
  listMonitoringOperations,
  previewStack,
  runStackLifecycle,
  type MonitoringOperation,
  type MonitoringStackStatusBase,
  type MonitoringStackTarget,
} from '@/lib/api/monitoring-stack';
import { useCluster, useClusters } from '@/lib/hooks';
import { useB2StorageLocations } from '@/components/backups/hooks';
import { useAuthStore } from '@/lib/store';
import { SharedMonitoringStacksPage } from '@/components/monitoring/shared-stacks-page';
import { ClusterMonitoringStackPage } from '@/components/monitoring/cluster-stack-page';

const stackStatus = vi.mocked(getStackStatus);
const stackPreview = vi.mocked(previewStack);
const stackLifecycle = vi.mocked(runStackLifecycle);
const listOperations = vi.mocked(listMonitoringOperations);
const getOperation = vi.mocked(getMonitoringOperation);
const clustersHook = vi.mocked(useClusters);
const clusterHook = vi.mocked(useCluster);
const storageHook = vi.mocked(useB2StorageLocations);

const CLUSTER_ID = 'cluster-1';

// ─────────────────────────────────────────────────────────────────────
// Fixtures
// ─────────────────────────────────────────────────────────────────────

function operation(overrides: Partial<MonitoringOperation> = {}): MonitoringOperation {
  const now = new Date().toISOString();
  return {
    id: 'op-1',
    targetType: 'cluster_stack',
    targetKey: CLUSTER_ID,
    operationType: 'install',
    status: 'pending',
    attemptCount: 1,
    startedAt: null,
    completedAt: null,
    errorMessage: '',
    createdAt: now,
    updatedAt: now,
    ...overrides,
  };
}

function grant(verbs: string[]) {
  act(() => {
    useAuthStore.setState({
      isAuthenticated: true,
      user: {
        id: 'u1',
        email: 'ops@astronomer.io',
        username: 'ops',
        is_active: true,
        is_superuser: false,
        roles: {
          global: [{ roleName: 'monitoring-operator', roleRules: [{ resources: ['monitoring'], verbs }] }],
          cluster: [],
          project: [],
        },
      } as never,
    });
  });
}

/** Grant the verbs ONLY through a binding scoped to one cluster. */
function grantOnCluster(clusterId: string, verbs: string[]) {
  act(() => {
    useAuthStore.setState({
      isAuthenticated: true,
      user: {
        id: 'u1',
        email: 'ops@astronomer.io',
        username: 'ops',
        is_active: true,
        is_superuser: false,
        roles: {
          global: [],
          cluster: [
            {
              clusterId,
              roleName: 'cluster-monitoring-operator',
              roleRules: [{ resources: ['monitoring'], verbs }],
            },
          ],
          project: [],
        },
      } as never,
    });
  });
}

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  });
}

function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={makeClient()}>{children}</QueryClientProvider>;
}

// ─────────────────────────────────────────────────────────────────────
// Family table
// ─────────────────────────────────────────────────────────────────────

interface FamilyCase {
  key: 'cluster' | 'thanos' | 'alertmanager';
  name: string;
  targetType: string;
  releaseName: string;
  namespace: string;
  installedStatus: MonitoringStackStatusBase;
  renderPage: () => void;
  /** Fill any required field the not-installed form leaves empty. */
  prepareInstall?: (panel: HTMLElement) => void;
  /** Grants that unlock every mutating control for this family. */
  fullGrant: string[];
}

const FAMILIES: FamilyCase[] = [
  {
    key: 'cluster',
    name: 'per-cluster stack',
    targetType: 'cluster_stack',
    releaseName: 'prometheus',
    namespace: 'monitoring',
    installedStatus: {
      status: 'healthy',
      namespace: 'monitoring',
      releaseName: 'prometheus',
      // NO chartVersion. GetStackStatus
      // (internal/handler/monitoring_stack_cluster.go:325-341) does not emit
      // one for this family, so a fixture carrying it would assert against a
      // response shape the server cannot produce — and the panel's "Chart
      // version" row really is a permanent "—" here until the backend adds it.
      pods: 6,
      observedRelease: {
        clusterId: CLUSTER_ID,
        namespace: 'monitoring',
        releaseName: 'prometheus',
        observedAt: new Date().toISOString(),
        status: 'deployed',
        revision: 4,
      },
    },
    renderPage: () => render(<ClusterMonitoringStackPage clusterId={CLUSTER_ID} />, { wrapper: Wrapper }),
    // create + update + delete: the per-cluster routes use the full split.
    fullGrant: ['read', 'create', 'update', 'delete'],
  },
  {
    key: 'thanos',
    name: 'shared Thanos',
    targetType: 'shared_thanos',
    releaseName: 'thanos',
    namespace: 'monitoring',
    installedStatus: {
      status: 'healthy',
      namespace: 'monitoring',
      releaseName: 'thanos',
      chartVersion: '1.23.0',
      managementClusterId: CLUSTER_ID,
      storageConfigId: 'storage-1',
    } as MonitoringStackStatusBase,
    renderPage: () => render(<SharedMonitoringStacksPage />, { wrapper: Wrapper }),
    prepareInstall: (panel) => {
      fireEvent.change(within(panel).getByLabelText('Object storage'), {
        target: { value: 'storage-1' },
      });
    },
    // The shared families gate install and uninstall on monitoring:update too.
    fullGrant: ['read', 'update'],
  },
  {
    key: 'alertmanager',
    name: 'shared Alertmanager',
    targetType: 'shared_alertmanager',
    releaseName: 'astronomer-alertmanager',
    namespace: 'monitoring',
    installedStatus: {
      status: 'healthy',
      namespace: 'monitoring',
      releaseName: 'astronomer-alertmanager',
      chartVersion: '1.18.0',
      managementClusterId: CLUSTER_ID,
    } as MonitoringStackStatusBase,
    renderPage: () => render(<SharedMonitoringStacksPage />, { wrapper: Wrapper }),
    fullGrant: ['read', 'update'],
  },
];

/** Answer status per family so the shared page's two panels differ. */
function statusPerTarget(byKey: Partial<Record<FamilyCase['key'], MonitoringStackStatusBase>>) {
  stackStatus.mockImplementation((target: MonitoringStackTarget) =>
    Promise.resolve(byKey[target.kind] ?? { status: 'not_configured' }),
  );
}

async function panelFor(family: FamilyCase): Promise<HTMLElement> {
  return waitFor(() => screen.getByTestId(`stack-panel-${family.key}`));
}

beforeEach(() => {
  vi.clearAllMocks();
  listOperations.mockResolvedValue([]);
  getOperation.mockResolvedValue(operation({ status: 'running' }));
  stackPreview.mockResolvedValue({
    clusterId: CLUSTER_ID,
    chart: { repoUrl: 'https://charts.example.io', chartName: 'kube-prometheus-stack' },
    values: { grafana: { enabled: true } },
    desiredSpecHash: 'abcdef1234567890',
    requiresReplace: false,
    replaceReasons: null,
  });
  stackLifecycle.mockResolvedValue(operation({ status: 'pending' }));
  clustersHook.mockReturnValue({
    data: { data: [{ id: CLUSTER_ID, name: 'mgmt', displayName: 'Management', isLocal: true }] },
  } as never);
  clusterHook.mockReturnValue({ data: { id: CLUSTER_ID, displayName: 'Management' } } as never);
  storageHook.mockReturnValue({
    data: { data: [{ id: 'storage-1', name: 'metrics', bucket: 'astronomer-metrics' }] },
  } as never);
});

afterEach(() => {
  act(() => useAuthStore.setState({ user: null, isAuthenticated: false }));
});

describe.each(FAMILIES)('$name lifecycle screen', (family) => {
  it('renders the recorded stack status', async () => {
    grant(family.fullGrant);
    statusPerTarget({ [family.key]: family.installedStatus });
    family.renderPage();

    const panel = await panelFor(family);
    await waitFor(() => expect(within(panel).getByText('Healthy')).toBeInTheDocument());
    expect(within(panel).getByText(family.releaseName)).toBeInTheDocument();
    if (family.installedStatus.chartVersion) {
      expect(
        within(panel).getByText(family.installedStatus.chartVersion as string),
      ).toBeInTheDocument();
    }
  });

  it('previews the rendered Helm values without leaving the screen', async () => {
    grant(family.fullGrant);
    statusPerTarget({ [family.key]: family.installedStatus });
    family.renderPage();

    const panel = await panelFor(family);
    const previewButton = within(panel).getByRole('button', { name: 'Preview' });
    // Preview is blocked until the form has the family's required fields, which
    // arrive with the status response.
    await waitFor(() => expect(previewButton).not.toBeDisabled());
    fireEvent.click(previewButton);

    await waitFor(() => expect(stackPreview).toHaveBeenCalled());
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Rendered Helm values')).toBeInTheDocument();
    expect(within(dialog).getByText('enabled: true')).toBeInTheDocument();
    expect(stackPreview.mock.calls[0][0].kind).toBe(family.key);
  });

  it('queues an install and shows it in flight instead of reporting success', async () => {
    grant(family.fullGrant);
    statusPerTarget({ [family.key]: { status: 'not_configured' } });
    getOperation.mockResolvedValue(
      operation({ targetType: family.targetType, status: 'running', startedAt: new Date().toISOString() }),
    );
    family.renderPage();

    const panel = await panelFor(family);
    // Progressive disclosure: the config form is hidden until an intentional
    // "Set up …" action reveals it, so click that before filling/installing.
    fireEvent.click(within(panel).getByRole('button', { name: /^Set up / }));
    family.prepareInstall?.(panel);

    const install = within(panel).getByRole('button', { name: 'Install' });
    await waitFor(() => expect(install).not.toBeDisabled());
    fireEvent.click(install);

    await waitFor(() => expect(stackLifecycle).toHaveBeenCalled());
    expect(stackLifecycle.mock.calls[0][1]).toBe('install');
    // The 202 is a receipt, not a result: the panel must show the operation
    // still running, driven by the tracker's poll of the detail endpoint.
    await waitFor(() =>
      expect(within(panel).getByText('Install in progress')).toBeInTheDocument(),
    );
  });

  it('surfaces the reconciler failure message verbatim with a retry', async () => {
    grant(family.fullGrant);
    statusPerTarget({ [family.key]: { status: 'installing' } });
    listOperations.mockImplementation((params) =>
      Promise.resolve(
        params?.targetType === family.targetType
          ? [
              operation({
                targetType: family.targetType,
                status: 'failed',
                errorMessage: 'release readiness check timed out: 1/3 ready pods for prometheus',
                startedAt: '2026-07-20T10:00:00Z',
                completedAt: '2026-07-20T10:04:00Z',
                updatedAt: '2026-07-20T10:04:00Z',
              }),
            ]
          : [],
      ),
    );
    family.renderPage();

    const panel = await panelFor(family);
    await waitFor(() =>
      expect(
        within(panel).getByText(
          'release readiness check timed out: 1/3 ready pods for prometheus',
        ),
      ).toBeInTheDocument(),
    );
    expect(within(panel).getByRole('button', { name: 'Retry' })).toBeInTheDocument();
  });

  it('requires a typed confirmation naming the release before uninstalling', async () => {
    grant(family.fullGrant);
    statusPerTarget({ [family.key]: family.installedStatus });
    family.renderPage();

    const panel = await panelFor(family);
    const uninstall = await within(panel).findByRole('button', { name: 'Uninstall' });
    fireEvent.click(uninstall);

    // The dialog names the release and the namespace being destroyed...
    expect(
      screen.getByText(new RegExp(`deletes the Helm release "${family.releaseName}"`)),
    ).toBeInTheDocument();
    expect(screen.getByText(new RegExp(`namespace ${family.namespace}`))).toBeInTheDocument();

    // ...and nothing is enqueued until the release name is typed back.
    const confirm = screen.getAllByRole('button', { name: 'Uninstall' }).at(-1) as HTMLElement;
    expect(confirm).toBeDisabled();
    expect(stackLifecycle).not.toHaveBeenCalled();

    // The form field for "Release name" carries the same placeholder; the
    // dialog's type-to-confirm input is the one rendered last.
    const confirmInput = screen.getAllByPlaceholderText(family.releaseName).at(-1) as HTMLElement;
    fireEvent.change(confirmInput, { target: { value: family.releaseName } });
    fireEvent.click(screen.getAllByRole('button', { name: 'Uninstall' }).at(-1) as HTMLElement);

    await waitFor(() => expect(stackLifecycle).toHaveBeenCalled());
    expect(stackLifecycle.mock.calls[0][1]).toBe('uninstall');
  });

  it('guards Replace with the same typed confirmation as Uninstall', async () => {
    // Replace is uninstall + install: the PVCs and the Alertmanager silences go
    // either way, so a one-click confirm on an action reachable from two places
    // is a weaker guard than the identical destruction warrants.
    grant(family.fullGrant);
    statusPerTarget({ [family.key]: family.installedStatus });
    family.renderPage();

    const panel = await panelFor(family);
    // Reveal the installed stack's actions before reaching Replace.
    fireEvent.click(await within(panel).findByRole('button', { name: 'Edit configuration' }));
    fireEvent.click(await within(panel).findByRole('button', { name: 'Replace' }));

    const confirm = screen.getAllByRole('button', { name: 'Replace' }).at(-1) as HTMLElement;
    expect(confirm).toBeDisabled();
    expect(stackLifecycle).not.toHaveBeenCalled();

    const confirmInput = screen.getAllByPlaceholderText(family.releaseName).at(-1) as HTMLElement;
    fireEvent.change(confirmInput, { target: { value: family.releaseName } });
    fireEvent.click(screen.getAllByRole('button', { name: 'Replace' }).at(-1) as HTMLElement);

    await waitFor(() => expect(stackLifecycle).toHaveBeenCalled());
    expect(stackLifecycle.mock.calls[0][1]).toBe('replace');
  });

  it('hides every control a monitoring:read caller would be 403d on, keeping preview', async () => {
    grant(['read']);
    statusPerTarget({ [family.key]: family.installedStatus });
    family.renderPage();

    const panel = await panelFor(family);
    await waitFor(() => expect(within(panel).getByText('Healthy')).toBeInTheDocument());

    expect(within(panel).queryByRole('button', { name: 'Install' })).not.toBeInTheDocument();
    expect(within(panel).queryByRole('button', { name: 'Upgrade' })).not.toBeInTheDocument();
    expect(within(panel).queryByRole('button', { name: 'Replace' })).not.toBeInTheDocument();
    expect(within(panel).queryByRole('button', { name: 'Uninstall' })).not.toBeInTheDocument();
    // The desired-configuration form is a mutation affordance too.
    expect(within(panel).queryByText('Desired configuration')).not.toBeInTheDocument();
    // Preview is monitoring:read on the backend, so it stays.
    expect(within(panel).getByRole('button', { name: 'Preview' })).toBeInTheDocument();
  });
});

// ─────────────────────────────────────────────────────────────────────
// Cross-family checks that only make sense once, on the shared page
// ─────────────────────────────────────────────────────────────────────

describe('per-cluster monitoring stack page', () => {
  it('does not offer Install to a caller whose monitoring grant is only cluster-scoped', async () => {
    // The server evaluates these routes at GLOBAL scope: RequirePermission only
    // falls back to the {id} route param for rbac.ResourceClusters
    // (internal/server/middleware/rbac.go:92-99), so for ResourceMonitoring the
    // cluster id is uuid.Nil and bindingApplies (internal/rbac/engine.go:371-393)
    // rejects every cluster-scoped binding. If the page asked at cluster scope
    // it would render Install here and the caller would be 403d — the UI being
    // more permissive than the API, which is the failure this pins.
    grantOnCluster(CLUSTER_ID, ['read', 'create', 'update', 'delete']);
    statusPerTarget({ cluster: { status: 'not_configured' } });
    render(<ClusterMonitoringStackPage clusterId={CLUSTER_ID} />, { wrapper: Wrapper });

    expect(await screen.findByText('Monitoring access required')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Install' })).not.toBeInTheDocument();
    expect(screen.queryByTestId('stack-panel-cluster')).not.toBeInTheDocument();
  });

});

describe('shared monitoring stacks page', () => {
  it('renders Thanos and Alertmanager as independent panels', async () => {
    grant(['read', 'update']);
    statusPerTarget({
      thanos: FAMILIES[1].installedStatus,
      alertmanager: { status: 'not_configured' },
    });
    render(<SharedMonitoringStacksPage />, { wrapper: Wrapper });

    const thanos = await screen.findByTestId('stack-panel-thanos');
    const alertmanager = await screen.findByTestId('stack-panel-alertmanager');
    await waitFor(() => expect(within(thanos).getByText('Healthy')).toBeInTheDocument());
    expect(within(alertmanager).getByText('Not installed')).toBeInTheDocument();
    // Progressive disclosure: an absent stack offers a "Set up …" entry point,
    // an installed one offers "Edit configuration" — the actual Install/Upgrade
    // buttons live inside the form each reveals.
    expect(within(alertmanager).getByRole('button', { name: /^Set up / })).toBeInTheDocument();
    expect(within(thanos).queryByRole('button', { name: /^Set up / })).not.toBeInTheDocument();
    expect(within(thanos).getByRole('button', { name: 'Edit configuration' })).toBeInTheDocument();
  });

  it('shows the whole page as permission-blocked without monitoring:read', async () => {
    grant(['list']);
    statusPerTarget({});
    render(<SharedMonitoringStacksPage />, { wrapper: Wrapper });

    expect(await screen.findByText('Monitoring access required')).toBeInTheDocument();
    expect(screen.queryByTestId('stack-panel-thanos')).not.toBeInTheDocument();
    // Nothing is fetched for a caller who cannot read.
    expect(stackStatus).not.toHaveBeenCalled();
  });
});
