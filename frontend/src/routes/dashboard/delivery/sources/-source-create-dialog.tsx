import { useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Select } from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { FormShell } from "@/components/ui/form-shell";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import { Field } from "@/components/form/fields";
import {
  ErrorMessage,
  inputClass,
  textareaClass,
} from "@/components/delivery/shared";
import {
  createDeliverySource,
  type CreateDeliverySourceRequest,
  type DeliveryAuthMode,
  type DeliverySourceType,
} from "@/lib/api/delivery-sources";
import type { SignatureProvider } from "@/lib/api/delivery-common";
import { queryKeys } from "@/lib/query-keys";
import { toastSuccess } from "@/lib/toast";
import { CredentialFields, credentialFromForm } from "./-credential-fields";

export function SourceCreateDialog({
  projectId,
  onClose,
}: {
  projectId: string;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [draft, setDraft] = useState<{
    kind: DeliverySourceType;
    authMode: DeliveryAuthMode;
    allowUnsigned: boolean;
    provider: SignatureProvider;
  }>({ kind: "git", authMode: "none", allowUnsigned: false, provider: "git" });
  const { kind, authMode, allowUnsigned, provider } = draft;
  const patchDraft = (patch: Partial<typeof draft>) =>
    setDraft((current) => ({ ...current, ...patch }));
  const mutation = useMutation({
    mutationFn: (body: CreateDeliverySourceRequest) =>
      createDeliverySource(body, crypto.randomUUID()),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.sourcesAll(projectId),
      });
      toastSuccess("Delivery source created");
      onClose();
    },
  });
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const credential = credentialFromForm(form, authMode);
    const body: CreateDeliverySourceRequest = {
      project_id: projectId,
      name: String(form.get("name") ?? "").trim(),
      description: String(form.get("description") ?? "").trim() || undefined,
      type: kind,
      url: String(form.get("url") ?? "").trim(),
      auth_mode: authMode,
      credential: Object.keys(credential).length ? credential : undefined,
      ca_bundle: String(form.get("ca_bundle") ?? "").trim() || undefined,
      proxy_ref: String(form.get("proxy_ref") ?? "").trim() || undefined,
      trust_policy: allowUnsigned
        ? { allow_unsigned: true }
        : {
            allow_unsigned: false,
            provider,
            identity: String(form.get("identity") ?? "").trim() || undefined,
            issuer: String(form.get("issuer") ?? "").trim() || undefined,
            key_ref: String(form.get("key_ref") ?? "").trim() || undefined,
          },
    };
    mutation.mutate(body);
  };
  return (
    <ModalShell
      title="Add delivery source"
      size="lg"
      onClose={onClose}
      subtitle="Secret fields are encrypted on submit and are never returned to this browser."
    >
      <FormShell className="space-y-4" onSubmit={submit}>
        <Field label="Name">
          <Input name="name" required maxLength={128} className={inputClass} />
        </Field>
        <Field label="Description">
          <Textarea
            name="description"
            maxLength={4096}
            className={textareaClass}
          />
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Source kind">
            <Select
              value={kind}
              onChange={(e) => {
                const next = e.target.value as DeliverySourceType;
                patchDraft({
                  kind: next,
                  provider: next === "git" ? "git" : "cosign_keyless",
                  ...(next !== "git" && authMode === "ssh"
                    ? { authMode: "none" as const }
                    : {}),
                });
              }}
              className={inputClass}
            >
              {sourceKinds.map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="Authentication">
            <Select
              value={authMode}
              onChange={(e) =>
                patchDraft({ authMode: e.target.value as DeliveryAuthMode })
              }
              className={inputClass}
            >
              {authModesFor(kind).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </Select>
          </Field>
        </div>
        <Field label="URL">
          <Input
            name="url"
            required
            type="url"
            className={inputClass}
            placeholder={
              kind === "git"
                ? "https://github.example/team/repo.git"
                : kind === "helm_http"
                  ? "https://charts.example.com"
                  : "oci://registry.example.com/team/artifact"
            }
          />
        </Field>
        <CredentialFields mode={authMode} />
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Enterprise CA bundle (optional)">
            <Textarea
              name="ca_bundle"
              className={textareaClass}
              placeholder="PEM certificate chain"
            />
          </Field>
          <Field label="Registered proxy reference (optional)">
            <Input name="proxy_ref" className={inputClass} />
          </Field>
        </div>
        <fieldset className="space-y-3 rounded-md border border-border p-4">
          <legend className="px-1 text-sm font-medium">
            Supply-chain trust
          </legend>
          <label className="flex items-center gap-2 text-sm">
            <Input
              type="checkbox"
              checked={allowUnsigned}
              onChange={(e) => patchDraft({ allowUnsigned: e.target.checked })}
            />{" "}
            Allow unsigned content
          </label>
          {!allowUnsigned && (
            <>
              <Field label="Verification provider">
                <Select
                  value={provider}
                  onChange={(e) =>
                    patchDraft({
                      provider: e.target.value as SignatureProvider,
                    })
                  }
                  className={inputClass}
                >
                  <option value="git">Git signature</option>
                  <option value="cosign_key">Cosign public key</option>
                  <option value="cosign_keyless">Cosign keyless</option>
                </Select>
              </Field>
              <div className="grid gap-4 sm:grid-cols-2">
                {provider === "git" && (
                  <Field label="Trusted identity (optional)">
                    <Input name="identity" className={inputClass} />
                  </Field>
                )}
                {provider === "cosign_keyless" && (
                  <Field label="Trusted identity">
                    <Input name="identity" required className={inputClass} />
                  </Field>
                )}
                {provider === "cosign_keyless" && (
                  <Field label="OIDC issuer">
                    <Input
                      name="issuer"
                      required
                      type="url"
                      className={inputClass}
                    />
                  </Field>
                )}
                {(provider === "git" || provider === "cosign_key") && (
                  <Field label="Registered public key reference">
                    <Input
                      name="key_ref"
                      required
                      className={inputClass}
                      placeholder="team-signing-key"
                    />
                  </Field>
                )}
              </div>
            </>
          )}
        </fieldset>
        {mutation.isError && <ErrorMessage error={mutation.error} />}
        <div className="flex justify-end gap-2">
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            type="submit"
            intent="primary"
            disabled={mutation.isPending}
            loading={mutation.isPending}
            loadingLabel="Creating…"
          >
            Create source
          </ActionButton>
        </div>
      </FormShell>
    </ModalShell>
  );
}

const sourceKinds: Array<[DeliverySourceType, string]> = [
  ["git", "Git repository"],
  ["oci_artifact", "OCI artifact"],
  ["helm_http", "Helm HTTP repository"],
  ["helm_oci", "Helm OCI repository"],
];
function authModesFor(
  kind: DeliverySourceType,
): Array<[DeliveryAuthMode, string]> {
  const values: Array<[DeliveryAuthMode, string]> = [
    ["none", "Public / none"],
    ["basic", "Username and password"],
    ["bearer", "Bearer token"],
    ["workload_identity", "Workload identity"],
  ];
  if (kind === "git") values.splice(3, 0, ["ssh", "SSH key"]);
  return values;
}
