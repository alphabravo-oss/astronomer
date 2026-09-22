import { useState } from "react";
import { X } from "lucide-react";
import { Link as RouterLink } from "@tanstack/react-router";

import { Card } from "@/components/ui/card";
import { ActionButton } from "@/components/ui/action-button";
import { useProductName } from "@/lib/hooks/public-settings";

const DISMISS_KEY = "astronomer.home.welcomeDismissed";

function readDismissed(): boolean {
  try {
    return localStorage.getItem(DISMISS_KEY) === "true";
  } catch {
    return false;
  }
}

function persistDismissed(): void {
  try {
    localStorage.setItem(DISMISS_KEY, "true");
  } catch {
    // Private browsing / disabled storage — the dismissal just won't stick
    // across reloads, which is a harmless degrade.
  }
}

/**
 * Dismissible first-run banner on the Home overview. Dismissal is
 * per-browser (localStorage), not a server-side preference — there's no
 * multi-device requirement for "I've seen this" state.
 */
export function WelcomeBanner({ estateEmpty }: { estateEmpty: boolean }) {
  const [dismissed, setDismissed] = useState(readDismissed);
  const productName = useProductName();
  if (dismissed) return null;

  return (
    <Card
      padding="md"
      className="relative flex items-start justify-between gap-4"
    >
      <div className="space-y-1 pr-6">
        <p className="text-sm font-medium text-foreground">
          Welcome to {productName}
        </p>
        <p className="text-sm text-muted-foreground">
          Manage every Kubernetes cluster, workload, and policy from one
          console.{" "}
          <a
            href="https://docs.astronomer.io"
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-2 hover:text-foreground"
          >
            Read the docs
          </a>
          {estateEmpty && (
            <>
              {" · "}
              <RouterLink
                to="/dashboard/clusters/register"
                className="underline underline-offset-2 hover:text-foreground"
              >
                Register your first cluster
              </RouterLink>
            </>
          )}
        </p>
      </div>
      <ActionButton
        size="icon"
        intent="ghost"
        aria-label="Dismiss welcome message"
        className="absolute right-3 top-3"
        icon={<X className="h-4 w-4" />}
        onClick={() => {
          setDismissed(true);
          persistDismissed();
        }}
      />
    </Card>
  );
}
