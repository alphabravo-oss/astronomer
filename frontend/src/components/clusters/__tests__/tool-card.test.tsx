import { fireEvent, render, screen } from '@testing-library/react';
import { ToolCard } from '@/components/clusters/tool-card';
import type { ClusterTool } from '@/types';

const tool = { slug: 'trivy-operator', name: 'Trivy Operator', description: 'Scanner' } as ClusterTool;

// The preset (development/staging/production) is an install-time chart-values
// choice and belongs in the install dialog. It used to render on every card,
// where it read as a per-tool environment switch — and appeared next to tools
// that were not installed at all. The card must stay free of it.
describe('ToolCard', () => {
  it('renders no preset dropdown', () => {
    render(<ToolCard tool={tool} onInstall={() => {}} onUninstall={() => {}} onAdopt={() => {}} />);
    expect(screen.queryByRole('combobox')).toBeNull();
    expect(screen.queryByText(/staging/i)).toBeNull();
  });

  it('asks to install by slug alone, leaving the preset to the dialog', () => {
    const onInstall = vi.fn();
    render(<ToolCard tool={tool} onInstall={onInstall} onUninstall={() => {}} onAdopt={() => {}} />);
    fireEvent.click(screen.getByRole('button', { name: /enable/i }));
    expect(onInstall).toHaveBeenCalledWith('trivy-operator');
  });
});
