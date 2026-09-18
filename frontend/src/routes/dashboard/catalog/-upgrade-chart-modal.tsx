import { useMemo, useState } from "react";
import { ActionButton } from "@/components/ui/action-button";
import { ModalShell } from "@/components/ui/modal-shell";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import {
  useInstalledChartUpgradeVersions,
  useUpgradeInstalledChart,
} from "@/lib/hooks/catalog";
import type { InstalledChart } from "@/types";
import { GitBranch, ShieldCheck } from "lucide-react";

export function UpgradeChartModal({
  installation,
  onClose,
}: {
  installation: InstalledChart;
  onClose: () => void;
}) {
  const versionsQuery = useInstalledChartUpgradeVersions(installation.id);
  const upgrade = useUpgradeInstalledChart();
  const candidates = useMemo(
    () =>
      (versionsQuery.data || []).filter(
        (version) => version.id !== installation.chartVersionId,
      ),
    [installation.chartVersionId, versionsQuery.data],
  );
  const [selectedVersionId, setSelectedVersionId] = useState("");
  const [values, setValues] = useState(installation.valuesOverride || "");
  const selectedVersion =
    candidates.find((version) => version.id === selectedVersionId) ||
    candidates[0];

  return (
    <ModalShell
      title={`Upgrade ${installation.releaseName}`}
      subtitle={`${installation.namespace} · revision ${installation.revision}`}
      onClose={onClose}
      size="md"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            loading={upgrade.isPending}
            disabled={!selectedVersion}
            onClick={() => {
              if (!selectedVersion) return;
              void upgrade
                .mutateAsync({
                  id: installation.id,
                  data: {
                    chart_version_id: selectedVersion.id,
                    values_override: values || undefined,
                  },
                })
                .then(onClose);
            }}
          >
            Start Flux upgrade
          </ActionButton>
        </>
      }
    >
      <div className="rounded-lg border border-primary/20 bg-primary/5 p-3">
        <p className="flex items-center gap-2 text-sm font-medium text-foreground">
          <GitBranch className="h-4 w-4 text-primary" /> Flux-managed upgrade
        </p>
        <p className="mt-1 text-xs text-table-secondary">
          Astronomer creates an immutable application version and Flux converges
          it. The current version remains available for rollback.
        </p>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground" htmlFor="upgrade-version">
          Target version
        </label>
        <Select
          id="upgrade-version"
          value={selectedVersionId || candidates[0]?.id || ""}
          onChange={(event) => setSelectedVersionId(event.target.value)}
          disabled={versionsQuery.isLoading || candidates.length === 0}
        >
          {candidates.map((version) => (
            <option key={version.id} value={version.id}>
              {version.version}{version.appVersion ? ` (app ${version.appVersion})` : ""}
            </option>
          ))}
        </Select>
        {!versionsQuery.isLoading && candidates.length === 0 && (
          <p className="text-xs text-table-secondary">No alternate versions are available.</p>
        )}
      </div>

      {selectedVersion?.digest && (
        <p className="flex items-center gap-2 break-all font-mono text-xs text-table-secondary">
          <ShieldCheck className="h-4 w-4 flex-none text-status-success" />
          {selectedVersion.digest}
        </p>
      )}

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground" htmlFor="upgrade-values">
          Values
        </label>
        <Textarea
          id="upgrade-values"
          value={values}
          onChange={(event) => setValues(event.target.value)}
          rows={12}
          className="min-h-0 resize-none font-mono text-xs"
          placeholder="# Preserve or update values for the new version"
        />
      </div>
    </ModalShell>
  );
}
