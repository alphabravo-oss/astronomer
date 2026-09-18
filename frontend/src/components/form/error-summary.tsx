import { useEffect, useId, useRef } from "react";
import { useStore } from "@tanstack/react-form";
import { useFormContext } from "@/lib/form";

export function errorMessages(errors: readonly unknown[]): string[] {
  return [
    ...new Set(
      errors.flatMap((error): string[] => {
        if (typeof error === "string") return error ? [error] : [];
        if (Array.isArray(error)) return errorMessages(error);
        if (
          error &&
          typeof error === "object" &&
          "message" in error &&
          typeof error.message === "string"
        ) {
          return [error.message];
        }
        return [];
      }),
    ),
  ];
}

/** Place inside form.AppForm. Validation and server failures stay visible after submission. */
export function FormErrorSummary({
  serverError,
}: {
  serverError?: string | null;
}) {
  const form = useFormContext();
  const errors = useStore(form.store, (state) => state.errors);
  const fields = useStore(form.store, (state) => state.fieldMeta);
  const attempts = useStore(form.store, (state) => state.submissionAttempts);
  const summary = useRef<HTMLDivElement>(null);
  const titleId = useId();
  const previousAttempt = useRef(attempts);
  const fieldErrors = Object.entries(fields).flatMap(([name, meta]) =>
    errorMessages(
      meta &&
        typeof meta === "object" &&
        "errors" in meta &&
        Array.isArray(meta.errors)
        ? meta.errors
        : [],
    ).map((message) => ({ name, message })),
  );
  const fieldMessages = new Set(fieldErrors.map(({ message }) => message));
  const messages = errorMessages([...errors, serverError]).filter(
    (message) => !fieldMessages.has(message),
  );
  const visible =
    (attempts > 0 && (messages.length > 0 || fieldErrors.length > 0)) ||
    !!serverError;

  useEffect(() => {
    if (visible && (attempts !== previousAttempt.current || serverError)) {
      summary.current?.focus();
      previousAttempt.current = attempts;
    }
  }, [attempts, serverError, visible]);

  if (!visible) return null;
  const focusField = (name: string) => {
    const scope =
      summary.current?.closest("form") ?? summary.current?.parentElement;
    const control = scope?.querySelector<HTMLElement>(
      `[name="${CSS.escape(name)}"]`,
    );
    control?.focus();
    control?.scrollIntoView({ block: "center", behavior: "instant" });
  };
  return (
    <div
      ref={summary}
      role="alert"
      tabIndex={-1}
      aria-labelledby={titleId}
      className="rounded-lg border border-status-error/40 bg-status-error/10 p-4 text-sm text-status-error focus:outline-hidden focus:ring-2 focus:ring-ring"
    >
      <h2 id={titleId} className="font-semibold">
        Unable to submit
      </h2>
      <ul className="mt-2 list-disc space-y-1 pl-5">
        {messages.map((message) => (
          <li key={message}>{message}</li>
        ))}
        {fieldErrors.map(({ name, message }) => (
          <li key={`${name}:${message}`}>
            <button
              type="button"
              className="text-left underline underline-offset-2"
              onClick={() => focusField(name)}
            >
              {message}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
