import type {
  HTMLAttributes,
  TableHTMLAttributes,
  TdHTMLAttributes,
  ThHTMLAttributes,
} from "react";
import { cn } from "@/lib/utils";

export function Table({
  className,
  layout = "fit",
  ...props
}: TableHTMLAttributes<HTMLTableElement> & {
  layout?: "fit" | "scroll";
}) {
  return (
    <table
      className={cn(
        "app-data-table w-full text-sm",
        layout === "fit" && "app-data-table-fit table-fixed max-w-full",
        className,
      )}
      data-layout={layout}
      {...props}
    />
  );
}

export function TableHeader({
  className,
  ...props
}: HTMLAttributes<HTMLTableSectionElement>) {
  return (
    <thead className={cn("border-b border-border", className)} {...props} />
  );
}

export function TableBody({
  className,
  ...props
}: HTMLAttributes<HTMLTableSectionElement>) {
  return (
    <tbody className={cn("divide-y divide-border", className)} {...props} />
  );
}

export function TableRow({
  className,
  ...props
}: HTMLAttributes<HTMLTableRowElement>) {
  return (
    <tr
      className={cn(
        "transition-colors hover:bg-[var(--table-row-hover)]",
        className,
      )}
      {...props}
    />
  );
}

export function TableHead({
  className,
  scope = "col",
  ...props
}: ThHTMLAttributes<HTMLTableCellElement>) {
  return (
    <th
      scope={scope}
      className={cn("h-10 px-3 text-left text-xs font-semibold", className)}
      {...props}
    />
  );
}

export function TableCell({
  className,
  ...props
}: TdHTMLAttributes<HTMLTableCellElement>) {
  return (
    <td className={cn("px-3 py-2.5 text-sm leading-5", className)} {...props} />
  );
}
