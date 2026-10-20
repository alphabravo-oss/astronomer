import { useState } from "react";
import {
  changedValuePaths,
  type CatalogVersionProfile,
} from "@/lib/catalog-configuration";

export function CatalogConfigurationChange({
  profile,
}: {
  profile?: CatalogVersionProfile;
}) {
  const [previous, setPrevious] = useState(profile);
  const [changes, setChanges] = useState<string[]>([]);
  if (profile && profile.version !== previous?.version) {
    if (previous)
      setChanges(
        changedValuePaths(
          previous.configuration?.schema ?? {},
          profile.configuration?.schema ?? {},
        ),
      );
    setPrevious(profile);
  }
  return (
    <div className="text-xs text-muted-foreground space-y-1">
      {profile?.compatibility?.kubernetes && (
        <p>Catalog Kubernetes range: {profile.compatibility.kubernetes}</p>
      )}
      {profile?.compatibility?.architectures?.length ? (
        <p>Architectures: {profile.compatibility.architectures.join(", ")}</p>
      ) : null}
      {profile?.upgradeFrom && (
        <p>Upgrade from chart versions: {profile.upgradeFrom}</p>
      )}
      <p>
        Version changes preserve your values. Review configuration against the
        selected version before installing.
      </p>
      {changes.length > 0 && (
        <details>
          <summary>{changes.length} configuration definitions changed</summary>
          <ul className="max-h-32 overflow-auto break-all font-mono">
            {changes.map((path) => (
              <li key={path}>{path}</li>
            ))}
          </ul>
          <p>
            Removed fields remain in YAML. Resolve any validation errors before
            installation.
          </p>
        </details>
      )}
    </div>
  );
}
