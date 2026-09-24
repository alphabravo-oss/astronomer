import { useState } from "react";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { useUpdateCluster } from "@/lib/hooks/clusters";
import type { UpdateClusterInput } from "@/lib/api/clusters";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { Cluster, ClusterEnvironment } from "@/types";
import { Pencil } from "lucide-react";
import {
  CLUSTER_BADGE_TONES,
  StatusBadge,
  type ClusterBadgeTone,
} from "@/components/ui/status-badge";

interface EditClusterModalProps {
  cluster: Cluster;
  onClose: () => void;
}

interface ClusterEditForm {
  displayName: string;
  environment: ClusterEnvironment;
  description: string;
  apiServerUrl: string;
  caCertificate: string;
  badgeText: string;
  badgeColor: ClusterBadgeTone;
  imageScanning?: { enabled: boolean; annotations: Record<string, string> };
  agentOverrides?: OpenAPIComponents["schemas"]["AgentOverrides"];
}

export function buildClusterEditRequest(
  form: ClusterEditForm,
): UpdateClusterInput {
  return {
    ...(form.imageScanning && {
      annotations: {
        ...form.imageScanning.annotations,
        "astronomer.io/image-scanning": form.imageScanning.enabled
          ? "enabled"
          : "disabled",
      },
    }),
    display_name: form.displayName,
    environment: form.environment,
    description: form.description,
    api_server_url: form.apiServerUrl,
    ca_certificate: form.caCertificate,
    badge_text: form.badgeText.trim(),
    badge_color: form.badgeText.trim() ? form.badgeColor : "",
    agent_overrides: form.agentOverrides ?? {},
  };
}

export function EditClusterModal({ cluster, onClose }: EditClusterModalProps) {
  const updateCluster = useUpdateCluster();
  const [form, setForm] = useState(() => clusterEditDefaults(cluster));
  const setAgentResource = (
    group: "requests" | "limits",
    resource: "cpu" | "memory",
    value: string,
  ) =>
    setForm((current) => ({
      ...current,
      agentOverrides: {
        ...current.agentOverrides,
        resources: {
          ...current.agentOverrides?.resources,
          [group]: {
            ...current.agentOverrides?.resources?.[group],
            [resource]: value,
          },
        },
      },
    }));
  const setAgentProxy = (
    field: "http_proxy" | "https_proxy" | "no_proxy",
    value: string,
  ) =>
    setForm((current) => ({
      ...current,
      agentOverrides: {
        ...current.agentOverrides,
        proxy: { ...current.agentOverrides?.proxy, [field]: value },
      },
    }));
  const handleSubmit = async () => {
    try {
      await updateCluster.mutateAsync({
        id: cluster.id,
        data: buildClusterEditRequest(form),
      });
      onClose();
    } catch {
      // Error handled by mutation
    }
  };

  return (
    <ModalShell
      title="Edit Cluster"
      onClose={onClose}
      panelClassName="max-w-lg bg-popover overflow-hidden"
      titleIcon={
        <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
          <Pencil className="h-4 w-4 text-muted-foreground" />
        </div>
      }
      footerClassName="flex items-center justify-end gap-2 bg-muted/30"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={handleSubmit}
            disabled={!form.displayName || updateCluster.isPending}
            loading={updateCluster.isPending}
          >
            Save Changes
          </ActionButton>
        </>
      }
    >
      {form.imageScanning && (
        <ImageScanningPreference
          enabled={form.imageScanning.enabled}
          onChange={(enabled) =>
            setForm((current) => ({
              ...current,
              imageScanning: { ...current.imageScanning!, enabled },
            }))
          }
        />
      )}
      <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-427d6b53-68"
        >
          Cluster Name
        </label>
        <Input
          id="field-427d6b53-68"
          type="text"
          value={cluster.name}
          disabled
          className="bg-muted/50 text-muted-foreground"
        />
        <p className="text-xs text-muted-foreground">
          Cluster name cannot be changed after creation.
        </p>
      </div>

      <div className="space-y-2">
        <div className="flex items-center justify-between gap-3">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="cluster-badge-text"
          >
            Cluster badge{" "}
            <span className="font-normal text-muted-foreground">
              (optional)
            </span>
          </label>
          <StatusBadge tone={form.badgeColor} label={form.badgeText} />
        </div>
        <div className="grid grid-cols-[minmax(0,1fr)_9rem] gap-2">
          <Input
            id="cluster-badge-text"
            type="text"
            maxLength={24}
            value={form.badgeText}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                badgeText: event.target.value,
              }))
            }
            placeholder="Production"
          />
          <Select
            aria-label="Cluster badge color"
            value={form.badgeColor}
            disabled={!form.badgeText.trim()}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                badgeColor: event.target.value as ClusterBadgeTone,
              }))
            }
          >
            {CLUSTER_BADGE_TONES.map((color) => (
              <option key={color} value={color}>
                {color[0].toUpperCase() + color.slice(1)}
              </option>
            ))}
          </Select>
        </div>
        <p className="text-xs text-muted-foreground">
          A short, color-coded marker helps distinguish similarly named
          production and non-production clusters.
        </p>
      </div>

      {!cluster.isLocal && (
        <>
          <div className="space-y-1.5">
            <label
              className="text-sm font-medium text-foreground"
              htmlFor="direct-api-server"
            >
              Direct Kubernetes API endpoint
            </label>
            <Input
              id="direct-api-server"
              type="url"
              value={form.apiServerUrl}
              onChange={(e) =>
                setForm((f) => ({ ...f, apiServerUrl: e.target.value }))
              }
              placeholder="https://api.example.com:6443"
            />
            <p className="text-xs text-muted-foreground">
              Optional. Enables a 15-minute read-only direct kubeconfig after
              TLS and reachability checks.
            </p>
          </div>
          <div className="space-y-1.5">
            <label
              className="text-sm font-medium text-foreground"
              htmlFor="direct-api-ca"
            >
              Direct API CA certificate
            </label>
            <Textarea
              id="direct-api-ca"
              value={form.caCertificate}
              onChange={(e) =>
                setForm((f) => ({ ...f, caCertificate: e.target.value }))
              }
              placeholder="-----BEGIN CERTIFICATE-----"
              rows={4}
              className="font-mono text-xs"
            />
          </div>
        </>
      )}

      {!cluster.isLocal && (
        <section className="space-y-3 rounded-lg border border-border bg-muted/20 p-4">
          <div>
            <h3 className="text-sm font-medium text-foreground">
              Agent runtime
            </h3>
            <p className="text-xs text-muted-foreground">
              Kubernetes quantities and outbound proxy settings applied by the
              next manifest or agent upgrade.
            </p>
          </div>
          <div className="grid grid-cols-2 gap-3">
            {(["requests", "limits"] as const).flatMap((group) =>
              (["cpu", "memory"] as const).map((resource) => (
                <div className="space-y-1" key={`${group}-${resource}`}>
                  <label
                    className="text-xs font-medium text-muted-foreground"
                    htmlFor={`agent-${group}-${resource}`}
                  >
                    {group === "requests" ? "Request" : "Limit"}{" "}
                    {resource.toUpperCase()}
                  </label>
                  <Input
                    id={`agent-${group}-${resource}`}
                    value={
                      form.agentOverrides?.resources?.[group]?.[resource] ?? ""
                    }
                    onChange={(event) =>
                      setAgentResource(group, resource, event.target.value)
                    }
                    placeholder={
                      group === "requests"
                        ? resource === "cpu"
                          ? "100m"
                          : "128Mi"
                        : resource === "cpu"
                          ? "500m"
                          : "512Mi"
                    }
                  />
                </div>
              )),
            )}
          </div>
          <div className="space-y-2">
            <Input
              aria-label="Agent HTTPS proxy"
              type="url"
              value={form.agentOverrides?.proxy?.https_proxy ?? ""}
              onChange={(event) =>
                setAgentProxy("https_proxy", event.target.value)
              }
              placeholder="HTTPS proxy, e.g. http://proxy.internal:3128"
            />
            <Input
              aria-label="Agent HTTP proxy"
              type="url"
              value={form.agentOverrides?.proxy?.http_proxy ?? ""}
              onChange={(event) =>
                setAgentProxy("http_proxy", event.target.value)
              }
              placeholder="HTTP proxy (optional)"
            />
            <Input
              aria-label="Agent no proxy"
              value={form.agentOverrides?.proxy?.no_proxy ?? ""}
              onChange={(event) =>
                setAgentProxy("no_proxy", event.target.value)
              }
              placeholder="NO_PROXY, comma separated"
            />
          </div>
          <p className="text-xs text-muted-foreground">
            Advanced tolerations and node affinity are available through the
            typed cluster API.
          </p>
        </section>
      )}

      <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-427d6b53-79"
        >
          Display Name
        </label>
        <Input
          id="field-427d6b53-79"
          type="text"
          value={form.displayName}
          onChange={(e) =>
            setForm((f) => ({ ...f, displayName: e.target.value }))
          }
          placeholder="My Production Cluster"
          data-initial-focus
        />
      </div>

      <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-427d6b53-90"
        >
          Environment
        </label>
        <Select
          id="field-427d6b53-90"
          value={form.environment}
          onChange={(e) =>
            setForm((f) => ({
              ...f,
              environment: e.target.value as ClusterEnvironment,
            }))
          }
        >
          <option value="production">Production</option>
          <option value="staging">Staging</option>
          <option value="development">Development</option>
          <option value="testing">Testing</option>
        </Select>
      </div>

      <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-427d6b53-103"
        >
          Description{" "}
          <span className="text-muted-foreground font-normal">(optional)</span>
        </label>
        <Textarea
          id="field-427d6b53-103"
          value={form.description}
          onChange={(e) =>
            setForm((f) => ({ ...f, description: e.target.value }))
          }
          placeholder="Brief description..."
          rows={2}
          className="min-h-0 resize-none"
        />
      </div>
    </ModalShell>
  );
}

function clusterEditDefaults(cluster: Cluster): ClusterEditForm {
  return {
    imageScanning: cluster.isLocal
      ? undefined
      : {
          enabled:
            cluster.annotations?.["astronomer.io/image-scanning"] !==
            "disabled",
          annotations: cluster.annotations ?? {},
        },
    displayName: cluster.displayName,
    environment: cluster.environment as ClusterEnvironment,
    description: cluster.description || "",
    apiServerUrl: cluster.apiServerUrl || "",
    caCertificate: cluster.caCertificate || "",
    badgeText: cluster.badgeText || "",
    badgeColor: cluster.badgeColor || "slate",
    agentOverrides: cluster.agentOverrides ?? {},
  };
}

function ImageScanningPreference({
  enabled,
  onChange,
}: {
  enabled: boolean;
  onChange: (enabled: boolean) => void;
}) {
  return (
    <label className="flex items-start gap-3 rounded-lg border border-border p-3">
      <Input
        type="checkbox"
        aria-label="Automatically install Trivy image scanning"
        checked={enabled}
        onChange={(event) => onChange(event.target.checked)}
        className="mt-0.5 h-4 w-4"
      />
      <span className="text-sm">
        Automatically install Trivy image scanning
        <span className="mt-1 block text-xs text-muted-foreground">
          Enabled by default. Turning this off prevents automatic installation;
          it does not uninstall an existing scanner or remove its reports.
        </span>
      </span>
    </label>
  );
}
