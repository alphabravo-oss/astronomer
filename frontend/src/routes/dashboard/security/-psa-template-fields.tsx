import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";

// Structural subset of TanStack Form's FieldApi that these presentational
// wrappers need. Using a narrow interface (rather than importing the real
// generic FieldApi type) keeps these components trivially reusable across
// every string-valued form.Field render callback in this file.
export interface StringFieldLike {
  name: string;
  state: { value: string };
  handleChange: (value: string) => void;
  handleBlur: () => void;
}

export function LabeledTextField({
  field,
  id,
  label,
  placeholder,
  className,
  small,
}: {
  field: StringFieldLike;
  id: string;
  label: string;
  placeholder?: string;
  className?: string;
  small?: boolean;
}) {
  return (
    <div className="space-y-1.5">
      <label
        className={
          small
            ? "text-xs text-muted-foreground"
            : "text-sm font-medium text-foreground"
        }
        htmlFor={id}
      >
        {label}
      </label>
      <Input
        name={field.name}
        id={id}
        type="text"
        value={field.state.value}
        onChange={(e) => field.handleChange(e.target.value)}
        onBlur={field.handleBlur}
        placeholder={placeholder}
        className={className}
      />
    </div>
  );
}

interface SelectFieldLike<T extends string> {
  name: string;
  state: { value: T };
  handleChange: (value: T) => void;
  handleBlur: () => void;
}

export function LabeledSelectField<T extends string>({
  field,
  id,
  label,
  options,
}: {
  field: SelectFieldLike<T>;
  id: string;
  label: string;
  options: readonly T[];
}) {
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground" htmlFor={id}>
        {label}
      </label>
      <Select
        name={field.name}
        id={id}
        value={field.state.value}
        onChange={(e) => field.handleChange(e.target.value as T)}
        onBlur={field.handleBlur}
        className="capitalize"
      >
        {options.map((option) => (
          <option key={option} value={option}>
            {option}
          </option>
        ))}
      </Select>
    </div>
  );
}
