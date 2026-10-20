import { useState } from "react";
import { ActionButton } from "@/components/ui/action-button";
import { Select } from "@/components/ui/select";
import {
  changedValuePaths,
  mergeCatalogValues,
  type CatalogConfiguration,
} from "@/lib/catalog-configuration";
import {
  dumpHelmValuesYAML,
  parseHelmValuesYAML,
} from "@/lib/helm-values-schema";

export function CatalogPresets({
  configuration,
  valuesYaml,
  onChange,
}: {
  configuration: CatalogConfiguration;
  valuesYaml: string;
  onChange: (value: string) => void;
}) {
  const [selection, setSelection] = useState("");
  const preset = configuration.presets.find((item) => item.id === selection);
  let current;
  try {
    current = parseHelmValuesYAML(valuesYaml);
  } catch {
    current = null;
  }
  const next =
    current && preset ? mergeCatalogValues(current, preset.values) : null;
  const changes = next && current ? changedValuePaths(current, next) : [];
  return (
    <section
      className="rounded-lg border border-border p-3 space-y-2"
      aria-label="Configuration presets"
    >
      <label htmlFor="catalog-preset" className="text-sm font-medium">
        Configuration preset
      </label>
      <Select
        id="catalog-preset"
        value={selection}
        onChange={(event) => setSelection(event.target.value)}
      >
        <option value="">Keep current configuration</option>
        {configuration.presets.map((item) => (
          <option key={item.id} value={item.id}>
            {item.label}
          </option>
        ))}
      </Select>
      {preset && (
        <>
          <p className="text-xs text-muted-foreground">{preset.description}</p>
          <p className="text-xs">
            {changes.length
              ? `Changes ${changes.length} value paths. Other values are preserved.`
              : "No existing values will change."}
          </p>
          {changes.length > 0 && (
            <ul className="text-xs font-mono max-h-32 overflow-auto break-all">
              {changes.map((path) => (
                <li key={path}>{path}</li>
              ))}
            </ul>
          )}
          <ActionButton
            size="sm"
            disabled={!next}
            onClick={() => {
              if (next) {
                onChange(dumpHelmValuesYAML(next));
                setSelection("");
              }
            }}
          >
            Apply preset
          </ActionButton>
        </>
      )}
    </section>
  );
}
