// App-wide TanStack Form kit (P5.1). Forms call `useAppForm` and render fields
// via `form.AppField` — the components below receive the field API from
// context, so pages never wire value/onChange/error plumbing by hand.
import { createFormHook, createFormHookContexts } from '@tanstack/react-form';

// Re-exported so form consumers subscribe to form state (values, isSubmitting)
// from the same import point as the hook itself.
export { useStore } from '@tanstack/react-form';
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
} from '@/components/form/fields';

export const { fieldContext, formContext, useFieldContext, useFormContext } =
  createFormHookContexts();

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
  formComponents: { SubmitButton },
});
