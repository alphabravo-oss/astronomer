import { FormShell } from "@/components/ui/form-shell";
import { Textarea } from "@/components/ui/textarea";
import { useAppForm } from "@/lib/form";
import {
  ErrorMessage,
  primaryButton,
  secondaryButton,
  textareaClass,
} from "./shared";

/** One controlled, auditable reason boundary for generation-fenced actions. */
export function AuditReasonForm({
  action,
  onSubmit,
  pending,
  error,
  onClose,
}: {
  action: string;
  onSubmit: (reason: string) => void;
  pending: boolean;
  error: unknown;
  onClose: () => void;
}) {
  const form = useAppForm({
    defaultValues: { reason: "" },
    onSubmit: ({ value }) => onSubmit(value.reason.trim()),
  });
  return (
    <FormShell
      form={form}
      className="space-y-4"
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <label className="block space-y-1.5 text-sm">
        <span className="font-medium">Audit reason code</span>
        <form.Field name="reason">
          {(field) => (
            <Textarea
              name={field.name}
              value={field.state.value}
              onChange={(event) => field.handleChange(event.target.value)}
              onBlur={field.handleBlur}
              required
              maxLength={96}
              className={textareaClass}
              placeholder={`${action}_requested`}
            />
          )}
        </form.Field>
      </label>
      {error != null && <ErrorMessage error={error} />}
      <div className="flex justify-end gap-2">
        <button type="button" className={secondaryButton} onClick={onClose}>
          Cancel
        </button>
        <button type="submit" className={primaryButton} disabled={pending}>
          Confirm {action}
        </button>
      </div>
    </FormShell>
  );
}
