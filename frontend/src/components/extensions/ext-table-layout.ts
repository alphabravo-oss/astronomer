import type { ColumnKind } from "@/components/ui/data-table-layout";
import type { FieldBinding, FieldFormat } from "@/lib/api/extensions";

/** Map an extension-declared field format to a DataTable column kind. */
export function extFieldKind(format: FieldFormat | undefined): ColumnKind {
  switch (format) {
    case "number":
    case "currency":
      return "count";
    case "bytes":
      return "bytes";
    case "datetime":
      return "date";
    case "duration":
      return "age";
    case "badge":
      return "badge";
    default:
      return "text";
  }
}

export interface ExtColumnLayout {
  kind: ColumnKind;
  grow: boolean;
  minSize?: number;
}

/**
 * Layout for each declared field. The first text field absorbs the spare
 * width (it holds the identifying name); when there is none, the first column
 * does. Exactly one column grows.
 */
export function extColumnLayouts(
  fields: Pick<FieldBinding, "format">[],
): ExtColumnLayout[] {
  const firstText = fields.findIndex((f) => extFieldKind(f.format) === "text");
  const growIndex = firstText >= 0 ? firstText : 0;
  return fields.map((field, index) => {
    const kind = extFieldKind(field.format);
    const grow = index === growIndex;
    return grow
      ? { kind, grow, minSize: 200 }
      : { kind, grow, ...(kind === "text" ? { minSize: 140 } : {}) };
  });
}
