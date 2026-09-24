import { PagedSelect } from "@/components/ui/paged-select";
import {
  listComponentBundles,
  listComponentBundleVersions,
} from "@/lib/api/delivery-bundles";
import { listDeliverySources } from "@/lib/api/delivery-sources";
import { listDeliveryConfigurationTemplates } from "@/lib/api/delivery-configuration";
import { queryKeys } from "@/lib/query-keys";

export function BundlePicker({
  projectId,
  value,
  onChange,
}: {
  projectId: string;
  value: string;
  onChange: (id: string) => void;
}) {
  return (
    <PagedSelect
      key={projectId}
      label="Bundle"
      id="delivery-bundle"
      value={value}
      onChange={onChange}
      required
      permission="delivery_bundles:list"
      placeholder="Select bundle"
      queryKey={(params) => queryKeys.delivery.bundles(projectId, params)}
      fetchPage={(params, signal) =>
        listComponentBundles(projectId, params, signal)
      }
      optionLabel={(row) => row.name}
    />
  );
}
export function BundleVersionPicker({
  projectId,
  bundleId,
}: {
  projectId: string;
  bundleId: string;
}) {
  return (
    <PagedSelect
      key={`${projectId}:${bundleId}`}
      label="Bundle version"
      id="delivery-bundle-version"
      name="bundle_version_id"
      required
      disabled={!bundleId}
      permission="delivery_bundles:list"
      placeholder="Select version"
      queryKey={(params) =>
        queryKeys.delivery.bundleVersions(projectId, bundleId, params)
      }
      fetchPage={(params, signal) =>
        listComponentBundleVersions(projectId, bundleId, params, signal)
      }
      eligible={(row) =>
        row.state === "ready" && row.verificationStatus === "verified"
      }
      optionLabel={(row) => `${row.version} · ${row.resolvedRevision}`}
    />
  );
}
export function SourcePicker({ projectId }: { projectId: string }) {
  return (
    <PagedSelect
      key={projectId}
      label="Source"
      id="delivery-source"
      name="source_id"
      required
      permission="delivery_sources:list"
      placeholder="Select source"
      queryKey={(params) => queryKeys.delivery.sources(projectId, params)}
      fetchPage={(params, signal) =>
        listDeliverySources(projectId, params, signal)
      }
      optionLabel={(row) => `${row.name} · ${row.type}`}
    />
  );
}
export function ConfigurationTemplatePicker({
  projectId,
  defaultValue,
}: {
  projectId: string;
  defaultValue?: string;
}) {
  return (
    <PagedSelect
      key={projectId}
      label="Configuration template"
      name="templateId"
      defaultValue={defaultValue}
      permission="delivery_configuration:list"
      placeholder="Any selected template"
      queryKey={(params) =>
        queryKeys.delivery.configurationTemplates(projectId, params)
      }
      fetchPage={(params, signal) =>
        listDeliveryConfigurationTemplates(projectId, params, signal)
      }
      optionLabel={(row) => row.name}
    />
  );
}
