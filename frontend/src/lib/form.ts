// App-wide TanStack Form kit (P5.1). Forms call `useAppForm` and render fields
// via `form.AppField` — the components below receive the field API from
// context, so pages never wire value/onChange/error plumbing by hand.
import { createFormHook, createFormHookContexts } from "@tanstack/react-form";
import { FormErrorSummary } from "@/components/form/error-summary";

// Re-exported so form consumers subscribe to form state (values, isSubmitting)
// from the same import point as the hook itself.
export { useStore } from "@tanstack/react-form";
import {
  CheckboxField,
  NumberField,
  PasswordField,
  SecretField,
  SelectField,
  SubmitButton,
  SwitchField,
  TextareaField,
  TextField,
} from "@/components/form/fields";

export const { fieldContext, formContext, useFieldContext, useFormContext } =
  createFormHookContexts();

/**
 * Permissive URL-with-scheme check for onChange field validators (P023.5):
 * parses with the platform `URL` parser and checks the scheme against an
 * allowlist. Internal/unqualified hostnames (e.g. `https://gitea.internal`)
 * are accepted — only the scheme is restricted.
 */
export function isValidUrlWithScheme(
  value: string,
  schemes: readonly string[],
): boolean {
  if (!value.trim()) return false;
  try {
    return schemes.includes(new URL(value).protocol.replace(/:$/, ""));
  } catch {
    return false;
  }
}

export const { useAppForm, withForm } = createFormHook({
  fieldContext,
  formContext,
  fieldComponents: {
    TextField,
    NumberField,
    PasswordField,
    SecretField,
    TextareaField,
    SelectField,
    SwitchField,
    CheckboxField,
  },
  formComponents: { SubmitButton, FormErrorSummary },
});
