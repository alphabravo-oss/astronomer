import { createFileRoute } from "@tanstack/react-router";
/**
 * /dashboard/settings/group-mappings/new — bind an SSO group to a role.
 */
import { useState } from "react";
import { Link as RouterLink, useNavigate } from "@tanstack/react-router";
import { ArrowLeft, Users } from "lucide-react";
import { toastError } from "@/lib/toast";
import { ActionButton } from "@/components/ui/action-button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { PageHeader, PageShell } from "@/components/ui/page";
import { useDexConnectors } from "@/components/auth/hooks";
import { RemoteRolePicker } from "@/components/rbac/remote-role-picker";
import { RemoteClusterPicker } from "@/components/clusters/remote-cluster-picker";
import { RemoteProjectPicker } from "@/components/projects/remote-project-picker";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { useCreateGroupMapping } from "@/components/settings/hooks";
import type { GroupScope } from "@/lib/api/settings";

function NewGroupMappingForm() {
  const navigate = useNavigate();
  const create = useCreateGroupMapping();
  const { data: connectors } = useDexConnectors();

  const [connector, setConnector] = useState("");
  const [groupName, setGroupName] = useState("");
  const [scope, setScope] = useState<GroupScope>("global");
  const [role, setRole] = useState("");
  const [target, setTarget] = useState("");

  const handleCreate = async () => {
    if (!groupName) return toastError("Group name is required");
    if (!role) return toastError("Role is required");
    if (scope !== "global" && !target) {
      return toastError("Target is required for scoped mappings");
    }
    try {
      await create.mutateAsync({
        ...(connector ? { connector_id: connector } : {}),
        group_name: groupName,
        scope,
        role_id: role,
        ...(scope === "cluster" ? { cluster_id: target } : {}),
        ...(scope === "project" ? { project_id: target } : {}),
      });
      void navigate({ to: "/dashboard/settings/group-mappings" });
    } catch {
      // toast handled
    }
  };

  return (
    <div className="space-y-6">
      <Card radius="xl" padding="lg" className="space-y-4">
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="gm-connector"
          >
            Connector
          </label>
          <Select
            id="gm-connector"
            value={connector}
            onChange={(e) => setConnector(e.target.value)}
          >
            <option value="">Any connector</option>
            {(connectors ?? []).map((c) => (
              <option key={c.id} value={c.id}>
                {c.displayName} ({c.type})
              </option>
            ))}
          </Select>
        </div>

        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="gm-group"
          >
            Group name
          </label>
          <Input
            id="gm-group"
            value={groupName}
            onChange={(e) => setGroupName(e.target.value)}
            placeholder="platform-admins"
            className="font-mono"
            data-initial-focus
          />
        </div>

        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="gm-scope"
          >
            Scope
          </label>
          <Select
            id="gm-scope"
            value={scope}
            onChange={(e) => {
              setScope(e.target.value as GroupScope);
              setTarget("");
              setRole("");
            }}
          >
            <option value="global">Global</option>
            <option value="cluster">Cluster</option>
            <option value="project">Project</option>
          </Select>
        </div>

        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="gm-role"
          >
            Role
          </label>
          <RemoteRolePicker
            key={scope}
            scope={scope}
            id="gm-role"
            value={role}
            onChange={setRole}
          />
        </div>

        {scope !== "global" && (
          <div className="space-y-1.5">
            <p className="text-sm font-medium text-foreground capitalize">
              {scope} target
            </p>
            {scope === "cluster" ? (
              <RemoteClusterPicker
                value={target}
                onChange={setTarget}
                ariaLabel="Cluster target"
              />
            ) : (
              <RemoteProjectPicker
                value={target}
                onChange={setTarget}
                ariaLabel="Project target"
              />
            )}
          </div>
        )}
      </Card>

      <div className="flex items-center justify-end gap-2">
        <ActionButton
          onClick={() =>
            void navigate({ to: "/dashboard/settings/group-mappings" })
          }
        >
          Cancel
        </ActionButton>
        <ActionButton
          intent="primary"
          onClick={() => void handleCreate()}
          disabled={create.isPending}
          loading={create.isPending}
        >
          Create mapping
        </ActionButton>
      </div>
    </div>
  );
}

function NewGroupMappingPage() {
  return (
    <SettingsAuthGate>
      <PageShell>
        <RouterLink
          to="/dashboard/settings/group-mappings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to group mappings
        </RouterLink>
        <PageHeader
          eyebrow="Settings · Group mappings · New"
          title={
            <span className="flex items-center gap-2">
              <Users className="h-5 w-5 text-muted-foreground" />
              New group mapping
            </span>
          }
        />
        <NewGroupMappingForm />
      </PageShell>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute("/dashboard/settings/group-mappings/new/")(
  {
    component: NewGroupMappingPage,
  },
);
