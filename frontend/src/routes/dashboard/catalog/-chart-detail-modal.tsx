import { useState } from 'react';
import { ActionButton } from '@/components/ui/action-button';
import { ModalShell } from '@/components/ui/modal-shell';
import { Select } from '@/components/ui/select';
import { useHelmChartVersions } from '@/lib/hooks';
import type { HelmChart, HelmChartVersion } from '@/types';
import { Download, Package } from 'lucide-react';
import { CategoryChip } from './-category';

export function ChartDetailModal({
  projectId,
  chart,
  onClose,
  onInstall,
}: {
  projectId: string;
  chart: HelmChart;
  onClose: () => void;
  onInstall: (chart: HelmChart, version: HelmChartVersion) => void;
}) {
  const { data: versions, isLoading: versionsLoading } = useHelmChartVersions(projectId, chart.id);
  const [selectedVersionId, setSelectedVersionId] = useState<string>('');

  const selectedVersion = versions?.find((v) => v.id === selectedVersionId) || versions?.[0];

  const footer = (
    <>
      <ActionButton onClick={onClose}>Close</ActionButton>
      <ActionButton
        intent="primary"
        icon={<Download className="h-4 w-4" />}
        disabled={!selectedVersion}
        onClick={() => {
          if (selectedVersion) {
            onInstall(chart, selectedVersion);
          }
        }}
      >
        Install
      </ActionButton>
    </>
  );

  return (
    <ModalShell
      title={chart.displayName || chart.name}
      subtitle={chart.repositoryName}
      onClose={onClose}
      size="lg"
      footer={footer}
      footerClassName="flex items-center justify-end gap-2"
      titleIcon={
        <div className="h-10 w-10 rounded-lg bg-muted/60 flex items-center justify-center overflow-hidden">
          {chart.iconUrl ? (
            <img
              src={chart.iconUrl}
              alt={chart.displayName}
              width={32}
              height={32}
              loading="lazy"
              className="h-8 w-8 object-contain"
            />
          ) : (
            <Package className="h-5 w-5 text-muted-foreground" />
          )}
        </div>
      }
    >
      <div className="flex items-center gap-3 flex-wrap">
        <CategoryChip category={chart.category} className="text-xs px-2 py-0.5" />
        {chart.keywords.map((kw) => (
          <span key={kw} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
            {kw}
          </span>
        ))}
      </div>

      <p className="text-sm text-muted-foreground">{chart.description}</p>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Version</label>
        {versionsLoading ? (
          <div className="h-9 w-48 rounded-md bg-muted animate-pulse" />
        ) : (
          <Select
            value={selectedVersionId || versions?.[0]?.id || ''}
            onChange={(e) => setSelectedVersionId(e.target.value)}
            className="w-48"
          >
            {(versions || []).map((v) => (
              <option key={v.id} value={v.id}>
                {v.version} (App: {v.appVersion})
              </option>
            ))}
          </Select>
        )}
      </div>

      {selectedVersion?.readme && (
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">README</label>
          <div className="rounded-lg border border-border bg-muted/30 p-4 max-h-64 overflow-y-auto">
            <pre className="text-xs text-muted-foreground whitespace-pre-wrap font-mono">
              {selectedVersion.readme}
            </pre>
          </div>
        </div>
      )}

      {selectedVersion?.defaultValues && (
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Default Values</label>
          <div className="rounded-lg border border-border bg-muted/30 p-4 max-h-48 overflow-y-auto">
            <pre className="text-xs text-muted-foreground whitespace-pre-wrap font-mono">
              {selectedVersion.defaultValues}
            </pre>
          </div>
        </div>
      )}
    </ModalShell>
  );
}
