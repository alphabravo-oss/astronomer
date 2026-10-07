import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Field } from "@/components/form/fields";
import { inputClass, textareaClass } from "@/components/delivery/shared";
import type {
  DeliveryAuthMode,
  SourceCredentialInput,
} from "@/lib/api/delivery-sources";

export function CredentialFields({ mode }: { mode: DeliveryAuthMode }) {
  if (mode === "none" || mode === "workload_identity")
    return (
      <p className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground">
        {mode === "none"
          ? "No credential will be stored."
          : "Authentication is provided by the configured workload identity."}
      </p>
    );
  if (mode === "basic")
    return (
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Username">
          <Input
            name="username"
            required
            autoComplete="off"
            className={inputClass}
          />
        </Field>
        <Field label="Password">
          <Input
            name="password"
            required
            type="password"
            autoComplete="new-password"
            className={inputClass}
          />
        </Field>
      </div>
    );
  if (mode === "bearer")
    return (
      <Field label="Bearer token">
        <Input
          name="token"
          required
          type="password"
          autoComplete="new-password"
          className={inputClass}
        />
      </Field>
    );
  return (
    <>
      <Field label="SSH private key">
        <Textarea name="private_key" required className={textareaClass} />
      </Field>
      <Field label="Known hosts">
        <Textarea name="known_hosts" required className={textareaClass} />
      </Field>
      <Field label="Key passphrase (optional)">
        <Input
          name="passphrase"
          type="password"
          autoComplete="new-password"
          className={inputClass}
        />
      </Field>
    </>
  );
}

export function credentialFromForm(
  form: FormData,
  mode: DeliveryAuthMode,
): SourceCredentialInput {
  const value = (key: string) => String(form.get(key) ?? "");
  if (mode === "basic")
    return { username: value("username"), password: value("password") };
  if (mode === "bearer") return { token: value("token") };
  if (mode === "ssh")
    return {
      private_key: value("private_key"),
      known_hosts: value("known_hosts"),
      passphrase: value("passphrase") || undefined,
    };
  return {};
}
