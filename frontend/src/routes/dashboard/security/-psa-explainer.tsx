import { Info } from "lucide-react";
import { cn } from "@/lib/utils";
import { psaLevelColors, psaLevelDefs, psaModeDefs } from "./-psa-constants";

/**
 * PSAExplainer renders the on-page definition of Pod Security Admission: a
 * short intro, the three Pod Security Standards (levels), and the three
 * admission modes. Styled as a callout card consistent with the rest of the
 * dashboard.
 */
export function PSAExplainer() {
  return (
    <div className="rounded-lg border border-border bg-muted/30 p-4 space-y-4">
      <div className="flex items-start gap-2">
        <Info className="h-4 w-4 text-primary mt-0.5 shrink-0" />
        <div className="space-y-1">
          <p className="text-sm font-medium text-foreground">
            What is Pod Security Admission (PSA)?
          </p>
          <p className="text-xs text-muted-foreground leading-relaxed">
            PSA is the built-in Kubernetes admission controller (the successor
            to PodSecurityPolicy) that enforces the three Pod Security Standards
            on a per-namespace basis. A template defines which standard applies
            in each of three modes; it only takes effect once you assign and
            apply it to a cluster from the Security Policies tab.
          </p>
        </div>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <p className="text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
            Standards (levels)
          </p>
          <ul className="space-y-1.5">
            {psaLevelDefs.map((d) => (
              <li key={d.level} className="flex items-start gap-2">
                <span
                  className={cn(
                    "text-2xs px-1.5 py-0.5 rounded-sm font-medium capitalize shrink-0",
                    psaLevelColors[d.level],
                  )}
                >
                  {d.level}
                </span>
                <span className="text-xs text-muted-foreground leading-relaxed">
                  {d.summary}
                </span>
              </li>
            ))}
          </ul>
        </div>

        <div className="space-y-2">
          <p className="text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
            Modes
          </p>
          <ul className="space-y-1.5">
            {psaModeDefs.map((d) => (
              <li key={d.mode} className="flex items-start gap-2">
                <span className="text-2xs px-1.5 py-0.5 rounded-sm font-medium capitalize shrink-0 bg-accent text-foreground">
                  {d.mode}
                </span>
                <span className="text-xs text-muted-foreground leading-relaxed">
                  {d.summary}
                </span>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}
