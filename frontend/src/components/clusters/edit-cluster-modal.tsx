"use client";

import { useState } from "react";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { useUpdateCluster } from "@/lib/hooks";
import type { UpdateClusterInput } from "@/lib/api/clusters";
import type { Cluster, ClusterEnvironment } from "@/types";
import { Pencil } from "lucide-react";

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
}

export function buildClusterEditRequest(
  form: ClusterEditForm,
): UpdateClusterInput {
  return {
    display_name: form.displayName,
    environment: form.environment,
    description: form.description,
    api_server_url: form.apiServerUrl,
    ca_certificate: form.caCertificate,
  };
}

export function EditClusterModal({ cluster, onClose }: EditClusterModalProps) {
  const updateCluster = useUpdateCluster();
  const [form, setForm] = useState({
    displayName: cluster.displayName,
    environment: cluster.environment as ClusterEnvironment,
    description: cluster.description || "",
    apiServerUrl: cluster.apiServerUrl || "",
    caCertificate: cluster.caCertificate || "",
  });

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
