/**
 * DIR-02: schema-lite form for ConfigMap create (name + data keys) as an
 * alternative to pure YAML for common day-2 edits. YAML power mode remains
 * available via CreateResourceDialog.
 */
import { useState } from "react";
import { ModalShell } from "@/components/ui/modal-shell";
import { useK8sCreate } from "@/lib/hooks/kubernetes-proxy";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { ActionButton } from "@/components/ui/action-button";
import { BARE_BUTTON } from "@/lib/bare-button";

type Props = {
  open: boolean;
  onClose: () => void;
  clusterId: string;
  namespace?: string;
};

export function ConfigMapFormDialog({
  open,
  onClose,
  clusterId,
  namespace = "default",
}: Props) {
  const [name, setName] = useState("");
  const [key, setKey] = useState("config");
  const [value, setValue] = useState("");
  const create = useK8sCreate();

  if (!open) return null;

  const submit = async () => {
    if (!name.trim()) return;
    const body = {
      apiVersion: "v1",
      kind: "ConfigMap",
      metadata: { name: name.trim(), namespace },
      data: { [key || "config"]: value },
    };
    try {
      await create.mutateAsync({
        clusterId,
        path: `api/v1/namespaces/${namespace}/configmaps`,
        body,
      });
      toastSuccess("ConfigMap created");
      onClose();
    } catch (e) {
      toastApiError("ConfigMap create failed", e);
    }
  };

  return (
    <ModalShell title="Create ConfigMap" onClose={onClose}>
      <div className="space-y-3 p-4">
        <label className="block text-sm">
          Name
          <input
            className="mt-1 w-full border rounded-sm px-2 py-1 bg-background"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <label className="block text-sm">
          Data key
          <input
            className="mt-1 w-full border rounded-sm px-2 py-1 bg-background"
            value={key}
            onChange={(e) => setKey(e.target.value)}
          />
        </label>
        <label className="block text-sm">
          Value
          <textarea
            className="mt-1 w-full border rounded-sm px-2 py-1 bg-background font-mono text-xs min-h-30"
            value={value}
            onChange={(e) => setValue(e.target.value)}
          />
        </label>
        <ActionButton
          {...BARE_BUTTON}
          className="rounded-sm bg-primary text-primary-foreground px-3 py-1.5 text-sm inline-block font-normal"
          onClick={submit}
          disabled={create.isPending}
        >
          Create
        </ActionButton>
      </div>
    </ModalShell>
  );
}
