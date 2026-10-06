import { useMemo, useState } from "react";
import { Input } from "@/components/ui/input";
import { useAppForm, useStore } from "@/lib/form";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toastApiError, toastError, toastSuccess } from "@/lib/toast";
import { extractApiErrorMessage } from "@/lib/api/errors";
import { ActionButton } from "@/components/ui/action-button";
import { Eye, EyeOff, Lock, XCircle } from "lucide-react";
import { queryKeys } from "@/lib/query-keys";
import { useClusterNamespaces } from "@/lib/hooks/clusters";
import {
  createClusterRegistry,
  updateClusterRegistry,
  type ClusterRegistry,
  type CreateRegistryRequest,
  type UpdateRegistryRequest,
} from "@/lib/api/cluster-registries";
import { cn } from "@/lib/utils";
import { ModalShell } from "@/components/ui/modal-shell";
import { BARE_BUTTON } from "@/lib/bare-button";

const PASSWORD_SENTINEL = "<set>";

function PasswordToggle({
  shown,
  onToggle,
}: {
  shown: boolean;
  onToggle: () => void;
}) {
  return (
    <ActionButton
      {...BARE_BUTTON}
      onClick={onToggle}
      className="absolute right-2 top-1/2 inline-block -translate-y-1/2 font-normal text-muted-foreground hover:text-foreground"
      aria-label={shown ? "Hide password" : "Show password"}
    >
      {shown ? (
        <EyeOff className="h-3.5 w-3.5" />
      ) : (
        <Eye className="h-3.5 w-3.5" />
      )}
    </ActionButton>
  );
}

// ─── Registry create/edit dialog ────────────────────────────────────────────
export function RegistryDialog({
  clusterId,
  existing,
  onClose,
}: {
  clusterId: string;
  existing?: ClusterRegistry;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const isEdit = !!existing;
  const { data: namespaces } = useClusterNamespaces(clusterId);

  const [showPassword, setShowPassword] = useState(false);

  const create = useMutation({
    mutationFn: (body: CreateRegistryRequest) =>
      createClusterRegistry(clusterId, body),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.clusterPages.registries(clusterId),
      });
      toastSuccess("Registry added");
      onClose();
    },
    onError: (e: Error) => toastApiError("Create failed", e),
  });
  const update = useMutation({
    mutationFn: (body: UpdateRegistryRequest) =>
      updateClusterRegistry(clusterId, existing!.id, body),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.clusterPages.registries(clusterId),
      });
      toastSuccess("Registry updated");
      onClose();
    },
    onError: (e: Error) => toastApiError("Update failed", e),
  });

  const loading = create.isPending || update.isPending;
  const submitError = create.error ?? update.error;

  const form = useAppForm({
    defaultValues: {
      registryUrl: existing?.registryUrl || "",
      username: existing?.username || "",
      // Edit seeds the sentinel — the password is only sent when the user
      // actually types a new one (round-trip variant, unchanged).
      password: isEdit ? PASSWORD_SENTINEL : "",
      selectedNs: (existing?.namespaces || []) as string[],
      secretName: existing?.secretName || "",
      injectDefaultSa: existing?.injectDefaultSa ?? false,
    },
    onSubmit: ({ value }) => {
      // Old imperative checks, ported 1:1 (same messages, same order).
      if (!value.registryUrl || !value.username) {
        toastError("Registry URL and username are required");
        return;
      }
      // The old `passwordTouched` flag maps onto the field's isDirty meta
      // (D14: survives across renders; this form never resets mid-session).
      const passwordTouched = form.getFieldMeta("password")?.isDirty ?? false;
      if (isEdit) {
        const body: UpdateRegistryRequest = {
          registry_url: value.registryUrl,
          username: value.username,
          namespaces: value.selectedNs,
          secret_name: value.secretName || undefined,
          inject_default_sa: value.injectDefaultSa,
        };
        if (passwordTouched && value.password !== PASSWORD_SENTINEL) {
          body.password = value.password;
        }
        update.mutate(body);
      } else {
        if (!value.password) {
          toastError("Password is required");
          return;
        }
        create.mutate({
          registry_url: value.registryUrl,
          username: value.username,
          password: value.password,
          namespaces: value.selectedNs,
          secret_name: value.secretName || undefined,
          inject_default_sa: value.injectDefaultSa,
        });
      }
    },
  });

  const selectedNs = useStore(form.store, (s) => s.values.selectedNs);
  const passwordTouched = useStore(
    form.store,
    (s) => s.fieldMeta.password?.isDirty ?? false,
  );

  return (
    <Modal
      title={isEdit ? `Edit ${existing.registryUrl}` : "Add registry"}
      icon={<Lock className="h-4 w-4" />}
      onClose={onClose}
    >
      <form.AppForm>
        <form.FormErrorSummary
          serverError={submitError ? extractApiErrorMessage(submitError) : null}
        />
      </form.AppForm>
      <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-31a30446-413"
        >
          Registry URL
        </label>
        <form.Field name="registryUrl">
          {(field) => (
            <Input
              id="field-31a30446-413"
              type="text"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="e.g. registry.example.com or 123.dkr.ecr.us-east-1.amazonaws.com"
              className="w-full h-(--control-h) px-3 rounded-lg border border-border bg-background text-sm font-mono
                placeholder:text-muted-foreground focus:outline-hidden focus:ring-2 focus:ring-ring"
            />
          )}
        </form.Field>
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-31a30446-430"
          >
            Username
          </label>
          <form.Field name="username">
            {(field) => (
              <Input
                id="field-31a30446-430"
                type="text"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                className="w-full h-(--control-h) px-3 rounded-lg border border-border bg-background text-sm font-mono
                  focus:outline-hidden focus:ring-2 focus:ring-ring"
              />
            )}
          </form.Field>
        </div>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-31a30446-445"
          >
            Password
          </label>
          <div className="relative">
            <form.Field name="password">
              {(field) => (
                <Input
                  id="field-31a30446-445"
                  type={showPassword ? "text" : "password"}
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onFocus={() => {
                    if (
                      isEdit &&
                      !field.state.meta.isDirty &&
                      field.state.value === PASSWORD_SENTINEL
                    ) {
                      field.handleChange("");
                    }
                  }}
                  onBlur={field.handleBlur}
                  className="w-full h-(--control-h) pl-3 pr-9 rounded-lg border border-border bg-background text-sm font-mono
                    focus:outline-hidden focus:ring-2 focus:ring-ring"
                />
              )}
            </form.Field>
            <PasswordToggle
              shown={showPassword}
              onToggle={() => setShowPassword((v) => !v)}
            />
          </div>
          {isEdit && !passwordTouched && (
            <p className="text-xs text-muted-foreground">
              Leave untouched to keep the existing password.
            </p>
          )}
        </div>
      </div>

      <NamespaceMultiSelect
        namespaces={namespaces?.map((n) => n.name) || []}
        selected={selectedNs}
        onChange={(ns) => form.setFieldValue("selectedNs", ns)}
      />

      <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-31a30446-488"
        >
          Secret name
        </label>
        <form.Field name="secretName">
          {(field) => (
            <Input
              id="field-31a30446-488"
              type="text"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="auto"
              className="w-full h-(--control-h) px-3 rounded-lg border border-border bg-background text-sm font-mono
                placeholder:text-muted-foreground focus:outline-hidden focus:ring-2 focus:ring-ring"
            />
          )}
        </form.Field>
        <p className="text-xs text-muted-foreground">
          Leave blank to auto-generate a secret name from the registry URL.
        </p>
      </div>

      <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer select-none">
        <form.Field name="injectDefaultSa">
          {(field) => (
            <Input
              type="checkbox"
              checked={field.state.value}
              onChange={(e) => field.handleChange(e.target.checked)}
              onBlur={field.handleBlur}
              className="h-4 w-4"
            />
          )}
        </form.Field>
        Attach to <code className="font-mono text-xs">default</code>{" "}
        ServiceAccount in each namespace
      </label>

      <ModalFooter
        onCancel={onClose}
        onSubmit={() => void form.handleSubmit()}
        loading={loading}
        submitLabel={isEdit ? "Save" : "Add registry"}
      />
    </Modal>
  );
}

// ─── Reused multi-select (kept local for now — small enough not to share) ───
function NamespaceMultiSelect({
  namespaces,
  selected,
  onChange,
}: {
  namespaces: string[];
  selected: string[];
  onChange: (ns: string[]) => void;
}) {
  const sorted = useMemo(() => [...namespaces].sort(), [namespaces]);
  const [filter, setFilter] = useState("");
  const filtered = sorted.filter((n) =>
    n.toLowerCase().includes(filter.toLowerCase()),
  );
  const toggle = (n: string) =>
    onChange(
      selected.includes(n) ? selected.filter((x) => x !== n) : [...selected, n],
    );

  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">
        Namespaces{" "}
        <span className="text-xs text-muted-foreground font-normal">
          (empty = all project namespaces)
        </span>
      </label>
      <Input
        type="text"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        placeholder="Filter namespaces…"
        className="w-full h-8 px-2.5 rounded-md border border-border bg-background text-xs
          placeholder:text-muted-foreground focus:outline-hidden focus:ring-1 focus:ring-ring"
      />
      <div className="rounded-md border border-border bg-background max-h-40 overflow-y-auto">
        {filtered.length === 0 ? (
          <div className="text-xs text-muted-foreground px-3 py-2">
            No namespaces match.
          </div>
        ) : (
          filtered.map((ns) => (
            <label
              key={ns}
              className="flex items-center gap-2 px-3 py-1 text-xs hover:bg-accent/40 cursor-pointer"
            >
              <Input
                type="checkbox"
                checked={selected.includes(ns)}
                onChange={() => toggle(ns)}
                className="h-3.5 w-3.5"
              />
              <span className="font-mono">{ns}</span>
            </label>
          ))
        )}
      </div>
      {selected.length > 0 && (
        <div className="flex flex-wrap gap-1 pt-1">
          {selected.map((ns) => (
            <span
              key={ns}
              className={cn(
                "inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm text-xs border",
                "bg-muted border-border text-muted-foreground",
              )}
            >
              {ns}
              <ActionButton
                {...BARE_BUTTON}
                onClick={() => toggle(ns)}
                className="hover:text-foreground inline-block font-normal"
                aria-label={`Remove ${ns}`}
              >
                <XCircle className="h-3 w-3" />
              </ActionButton>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function Modal({
  title,
  icon,
  onClose,
  children,
}: {
  title: string;
  icon?: React.ReactNode;
  onClose: () => void;
  children: React.ReactNode;
}) {
  return (
    <ModalShell
      title={title}
      onClose={onClose}
      size="md"
      titleIcon={
        icon ? (
          <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center text-muted-foreground shrink-0">
            {icon}
          </div>
        ) : undefined
      }
    >
      {children}
    </ModalShell>
  );
}

function ModalFooter({
  onCancel,
  onSubmit,
  loading,
  submitLabel,
}: {
  onCancel: () => void;
  onSubmit: () => void;
  loading?: boolean;
  submitLabel: string;
}) {
  return (
    <div className="flex items-center justify-end gap-2 pt-3 -mx-6 px-6 border-t border-border">
      <div className="pt-3 flex items-center gap-2">
        <ActionButton onClick={onCancel} disabled={loading} intent="ghost">
          Cancel
        </ActionButton>
        <ActionButton
          onClick={onSubmit}
          disabled={loading}
          intent="primary"
          loading={loading}
        >
          {submitLabel}
        </ActionButton>
      </div>
    </div>
  );
}
