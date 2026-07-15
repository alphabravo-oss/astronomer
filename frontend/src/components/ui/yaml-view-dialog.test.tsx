import type { MockedFunction } from 'vitest';
import { render, fireEvent } from '@testing-library/react';
import { YamlPanel } from './yaml-view-dialog';
import * as hooks from '@/lib/hooks';

// Mock the data hooks so we can drive `useK8sGetYaml`'s returned value.
vi.mock('@/lib/hooks', () => ({
  useK8sGetYaml: vi.fn(),
  useK8sApplyYaml: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
  useK8sDryRunYaml: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
}));

// Replace the heavy editor with a plain textarea that surfaces value/onChange.
vi.mock('@/components/ui/yaml-editor', () => ({
  YamlEditor: ({ value, onChange }: { value: string; onChange?: (v: string) => void }) => (
    <textarea
      data-testid="yaml-editor"
      value={value}
      readOnly={!onChange}
      onChange={(e) => onChange && onChange(e.target.value)}
    />
  ),
}));

const mockedGetYaml = hooks.useK8sGetYaml as MockedFunction<typeof hooks.useK8sGetYaml>;

// Regression: a background refetch (window-focus refetch or a k8s.all cache
// invalidation from any mutation) must NOT overwrite the operator's in-progress
// edits while the editor is in edit mode.
describe('YamlPanel — edit-mode preservation', () => {
  afterEach(() => vi.clearAllMocks());

  it('keeps in-progress edits when the server YAML refetches during editing', () => {
    const refetch = vi.fn();
    mockedGetYaml.mockReturnValue({
      data: 'name: v1',
      isLoading: false,
      error: null,
      refetch,
    } as unknown as ReturnType<typeof hooks.useK8sGetYaml>);

    const { getByText, getByTestId, rerender } = render(
      <YamlPanel clusterId="c1" k8sPath="api/v1/namespaces/default/pods/p" />,
    );

    // Enter edit mode and type changes.
    fireEvent.click(getByText('Edit'));
    fireEvent.change(getByTestId('yaml-editor'), { target: { value: 'name: my-edits' } });
    expect((getByTestId('yaml-editor') as HTMLTextAreaElement).value).toBe('name: my-edits');

    // Background refetch delivers a *different* server copy (changed
    // resourceVersion / managedFields timestamps in real life).
    mockedGetYaml.mockReturnValue({
      data: 'name: v2-from-server',
      isLoading: false,
      error: null,
      refetch,
    } as unknown as ReturnType<typeof hooks.useK8sGetYaml>);
    rerender(<YamlPanel clusterId="c1" k8sPath="api/v1/namespaces/default/pods/p" />);

    // Edits survive — the editor was NOT re-seeded from the server copy.
    expect((getByTestId('yaml-editor') as HTMLTextAreaElement).value).toBe('name: my-edits');
  });

  it('does seed the editor from the server copy while in view mode', () => {
    const refetch = vi.fn();
    mockedGetYaml.mockReturnValue({
      data: 'name: v1',
      isLoading: false,
      error: null,
      refetch,
    } as unknown as ReturnType<typeof hooks.useK8sGetYaml>);

    const { getByText, getByTestId, rerender } = render(
      <YamlPanel clusterId="c1" k8sPath="api/v1/namespaces/default/pods/p" />,
    );
    // Stay in view mode; a refetch should reflect the fresh server copy once
    // the user switches to edit.
    mockedGetYaml.mockReturnValue({
      data: 'name: v2-from-server',
      isLoading: false,
      error: null,
      refetch,
    } as unknown as ReturnType<typeof hooks.useK8sGetYaml>);
    rerender(<YamlPanel clusterId="c1" k8sPath="api/v1/namespaces/default/pods/p" />);

    fireEvent.click(getByText('Edit'));
    expect((getByTestId('yaml-editor') as HTMLTextAreaElement).value).toBe('name: v2-from-server');
  });
});
