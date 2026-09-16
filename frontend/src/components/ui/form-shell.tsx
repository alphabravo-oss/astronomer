import type { FormHTMLAttributes } from "react";

/** Shared semantic form boundary for route-level operator workflows. */
export function FormShell({
  children,
  ...props
}: FormHTMLAttributes<HTMLFormElement>) {
  return <form {...props}>{children}</form>;
}
