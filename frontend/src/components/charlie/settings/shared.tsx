import type { ReactNode } from "react";
import { AlertTriangle, KeyRound } from "lucide-react";
import { StatePanel } from "@/components/ui/empty-state";
import type { CharlieOnboardingInput } from "@/lib/api/charlie-admin";

export const button =
  "inline-flex min-h-9 items-center justify-center gap-2 rounded-md border border-border px-3 py-2 text-sm font-medium transition-colors motion-reduce:transition-none hover:bg-accent disabled:cursor-not-allowed disabled:opacity-50";
export const primary = `${button} border-primary bg-primary text-primary-foreground hover:bg-primary/90`;
export const field =
  "h-9 w-full rounded-md border border-border bg-background px-3 text-sm focus:outline-none focus:ring-2 focus:ring-ring";

export function Section({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <section className="space-y-4 rounded-xl border border-border bg-card p-5">
      <div>
        <h2 className="font-semibold">{title}</h2>
        {description && (
          <p className="mt-1 text-xs text-muted-foreground">{description}</p>
        )}
      </div>
      {children}
    </section>
  );
}

export function Unavailable({ name, retry }: { name: string; retry?: () => void }) {
  return (
    <StatePanel
      icon={AlertTriangle}
      tone="warning"
      title={`${name} unavailable`}
      description="The Astronomer gateway does not expose this Charlie capability yet, Charlie is unreachable, or access was denied. No configuration was changed."
      actionLabel={retry ? "Retry" : undefined}
      onAction={retry}
    />
  );
}

export function Meta({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 break-all text-sm font-medium">{value || "—"}</dd>
    </div>
  );
}

export const emptyOnboarding: CharlieOnboardingInput = {
  package: {},
  signingPublicKey: "",
  confirmedSigningKeyId: "",
  confirmedSigningFingerprint: "",
  expectedDeploymentId: "",
  expectedRouteId: "",
};

export function Field({
  label,
  value,
  set,
  type = "text",
  min,
  max,
}: {
  label: string;
  value: string | number;
  set: (v: string) => void;
  type?: string;
  min?: number;
  max?: number;
}) {
  return (
    <label className="space-y-1">
      <span className="text-xs text-muted-foreground">{label}</span>
      <input
        type={type}
        min={min}
        max={max}
        value={value}
        onChange={(e) => set(e.target.value)}
        className={field}
      />
    </label>
  );
}

export function NumberField({
  label,
  value,
  set,
  min = 0,
  max,
}: {
  label: string;
  value: number;
  set: (v: number) => void;
  min?: number;
  max?: number;
}) {
  return (
    <Field
      label={label}
      type="number"
      value={value}
      min={min}
      max={max}
      set={(v) =>
        set(
          Math.min(
            max ?? Number.MAX_SAFE_INTEGER,
            Math.max(min, Number(v) || min),
          ),
        )
      }
    />
  );
}

export function GrantList({
  title,
  items,
}: {
  title: string;
  items: Array<{ permission: string; scope: string; source: string }>;
}) {
  return (
    <Section
      title={title}
      description="Astronomer RBAC only. Charlie does not define its own roles."
    >
      {items.length ? (
        items.map((g, i) => (
          <div
            key={`${g.permission}:${g.scope}:${i}`}
            className="flex items-start gap-3 rounded border p-3"
          >
            <KeyRound className="mt-0.5 h-4 w-4 text-muted-foreground" />
            <div>
              <p className="text-sm font-medium">{g.permission}</p>
              <p className="text-xs text-muted-foreground">
                {g.scope} · {g.source}
              </p>
            </div>
          </div>
        ))
      ) : (
        <p className="text-sm text-muted-foreground">No Charlie grants on this identity.</p>
      )}
    </Section>
  );
}
