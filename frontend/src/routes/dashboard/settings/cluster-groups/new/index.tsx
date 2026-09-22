import { createFileRoute } from "@tanstack/react-router";
/**
 * /dashboard/settings/cluster-groups/new — create a cluster group.
 * Shares the read hooks with the list page; editing an existing group
 * stays a modal there (see settings/cluster-groups/index.tsx).
 */
import { useMemo, useState } from "react";
import { Link as RouterLink, useNavigate } from "@tanstack/react-router";
import { ArrowLeft, Folder } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { PageHeader, PageShell } from "@/components/ui/page";
import {
  CLUSTER_GROUP_COLORS,
  CLUSTER_GROUP_ICONS,
} from "@/lib/api/cluster-groups";
import {
  MAX_DEPTH,
  useClusterGroups,
  useCreateClusterGroup,
} from "../index";

function NewClusterGroupForm() {
  const navigate = useNavigate();
  const { data: allGroups } = useClusterGroups();
  const create = useCreateClusterGroup();

  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugTouched, setSlugTouched] = useState(false);
  const [description, setDescription] = useState("");
  const [parentId, setParentId] = useState("");
  const [color, setColor] = useState<(typeof CLUSTER_GROUP_COLORS)[number]>(
    CLUSTER_GROUP_COLORS[0],
  );
  const [icon, setIcon] = useState<(typeof CLUSTER_GROUP_ICONS)[number]>(
    CLUSTER_GROUP_ICONS[0],
  );

  const parentOptions = useMemo(
    () => (allGroups ?? []).filter((g) => g.depth < MAX_DEPTH),
    [allGroups],
  );

  const handleName = (v: string) => {
    setName(v);
    if (!slugTouched) {
      setSlug(
        v
          .toLowerCase()
          .replace(/[^a-z0-9]+/g, "-")
          .replace(/^-+|-+$/g, ""),
      );
    }
  };

  const handleCreate = async () => {
    try {
      await create.mutateAsync({
        name,
        slug,
        description,
        parent_id: parentId || undefined,
        color,
        icon,
      });
      void navigate({ to: "/dashboard/settings/cluster-groups" });
    } catch {
      // mutation toasts on error
    }
  };

  return (
    <div className="space-y-6">
      <Card radius="xl" padding="lg" className="space-y-3">
        <label className="block">
          <span className="text-xs font-medium text-muted-foreground">
            Name
          </span>
          <Input
            type="text"
            value={name}
            onChange={(e) => handleName(e.target.value)}
            className="mt-1"
            data-initial-focus
          />
        </label>
        <label className="block">
          <span className="text-xs font-medium text-muted-foreground">
            Slug{" "}
            <span className="text-muted-foreground">
              (URL-safe identifier)
            </span>
          </span>
          <Input
            type="text"
            value={slug}
            onChange={(e) => {
              setSlug(e.target.value);
              setSlugTouched(true);
            }}
            className="mt-1 font-mono"
          />
        </label>
        <label className="block">
          <span className="text-xs font-medium text-muted-foreground">
            Description
          </span>
          <Textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            rows={2}
            className="mt-1 min-h-0"
          />
        </label>
        <label className="block">
          <span className="text-xs font-medium text-muted-foreground">
            Parent
          </span>
          <Select
            value={parentId}
            onChange={(e) => setParentId(e.target.value)}
            containerClassName="mt-1"
          >
            <option value="">— Top-level —</option>
            {parentOptions.map((p) => (
              <option key={p.id} value={p.id}>
                {"— ".repeat(p.depth)}
                {p.name}
              </option>
            ))}
          </Select>
        </label>
        <div className="grid grid-cols-2 gap-3">
          <label className="block">
            <span className="text-xs font-medium text-muted-foreground">
              Color
            </span>
            <div className="mt-1 flex flex-wrap gap-1">
              {CLUSTER_GROUP_COLORS.map((c) => (
                <button
                  type="button"
                  key={c}
                  onClick={() => setColor(c)}
                  className="h-7 w-7 rounded-sm border-2"
                  style={{
                    background: c,
                    borderColor: color === c ? "#fff" : "transparent",
                    outline: color === c ? `2px solid ${c}` : "none",
                  }}
                  aria-label={`Color ${c}`}
                />
              ))}
            </div>
          </label>
          <label className="block">
            <span className="text-xs font-medium text-muted-foreground">
              Icon
            </span>
            <Select
              value={icon}
              onChange={(e) =>
                setIcon(e.target.value as (typeof CLUSTER_GROUP_ICONS)[number])
              }
              containerClassName="mt-1"
            >
              {CLUSTER_GROUP_ICONS.map((i) => (
                <option key={i} value={i}>
                  {i}
                </option>
              ))}
            </Select>
          </label>
        </div>
      </Card>

      <div className="flex items-center justify-end gap-2">
        <ActionButton
          onClick={() =>
            void navigate({ to: "/dashboard/settings/cluster-groups" })
          }
        >
          Cancel
        </ActionButton>
        <ActionButton
          intent="primary"
          onClick={() => void handleCreate()}
          disabled={!name || !slug || create.isPending}
          loading={create.isPending}
        >
          Create
        </ActionButton>
      </div>
    </div>
  );
}

function NewClusterGroupPage() {
  return (
    <PageShell>
      <RouterLink
        to="/dashboard/settings/cluster-groups"
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to cluster groups
      </RouterLink>
      <PageHeader
        eyebrow="Settings · Cluster groups · New"
        title={
          <span className="flex items-center gap-2">
            <Folder className="h-5 w-5 text-muted-foreground" />
            New cluster group
          </span>
        }
      />
      <NewClusterGroupForm />
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/settings/cluster-groups/new/")({
  component: NewClusterGroupPage,
});
