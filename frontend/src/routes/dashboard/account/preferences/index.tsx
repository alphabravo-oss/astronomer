import { createFileRoute } from "@tanstack/react-router";
import { Check, Loader2, Star } from "lucide-react";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import { Card, CardContent } from "@/components/ui/card";
import { Select } from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { useUserPreferences } from "@/lib/user-preferences";
import {
  favoriteNavigationOptions,
  landingRouteOptions,
  type FavoriteRoute,
} from "@/lib/api/user-preferences";

function AccountPreferencesPage() {
  const { preferences, isLoading, isSaving, saveError, updatePreferences } =
    useUserPreferences();

  const toggleFavorite = (route: FavoriteRoute) => {
    const favorites = preferences.favorites.includes(route)
      ? preferences.favorites.filter((item) => item !== route)
      : [...preferences.favorites, route];
    updatePreferences({ favorites });
  };

  return (
    <PageShell>
      <PageHeader
        title="Preferences"
        description="Your console preferences follow your account across browsers and devices."
        actions={
          <span
            className={cn(
              "inline-flex items-center gap-1.5 text-xs",
              saveError ? "text-status-error" : "text-muted-foreground",
            )}
            role="status"
          >
            {isSaving ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : !saveError ? (
              <Check className="h-3.5 w-3.5 text-status-success" />
            ) : null}
            {isSaving
              ? "Saving"
              : saveError
                ? "Could not save preferences"
                : "Saved automatically"}
          </span>
        }
      />

      {isLoading ? (
        <div className="flex h-40 items-center justify-center">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      ) : (
        <>
          <PageSection
            title="Appearance and behavior"
            description="Choose how dense, visual, and time-aware the console should be."
          >
            <Card>
              <CardContent className="grid gap-5 p-5 md:grid-cols-2">
                <PreferenceSelect
                  id="preference-theme"
                  label="Theme"
                  value={preferences.theme}
                  onChange={(theme) =>
                    updatePreferences({
                      theme: theme as typeof preferences.theme,
                    })
                  }
                  options={[
                    ["system", "Use system setting"],
                    ["dark", "Dark"],
                    ["light", "Light"],
                  ]}
                />
                <PreferenceSelect
                  id="preference-density"
                  label="Table density"
                  value={preferences.table_density}
                  onChange={(table_density) =>
                    updatePreferences({
                      table_density:
                        table_density as typeof preferences.table_density,
                    })
                  }
                  options={[
                    ["comfortable", "Comfortable"],
                    ["compact", "Compact"],
                  ]}
                />
                <PreferenceSelect
                  id="preference-landing"
                  label="Landing page"
                  value={preferences.landing_route}
                  onChange={(landing_route) =>
                    updatePreferences({
                      landing_route:
                        landing_route as typeof preferences.landing_route,
                    })
                  }
                  options={landingRouteOptions.map((option) => [
                    option.value,
                    option.label,
                  ])}
                />
                <PreferenceSelect
                  id="preference-time"
                  label="Time display"
                  value={preferences.time_format}
                  onChange={(time_format) =>
                    updatePreferences({
                      time_format:
                        time_format as typeof preferences.time_format,
                    })
                  }
                  options={[
                    ["locale", "Browser locale"],
                    ["12h", "12-hour clock"],
                    ["24h", "24-hour clock"],
                  ]}
                />
              </CardContent>
            </Card>
          </PageSection>

          <PageSection
            title="Favorite destinations"
            description="Pin up to 12 frequently used areas to the top of global navigation."
          >
            <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
              {favoriteNavigationOptions.map((option) => {
                const selected = preferences.favorites.includes(option.href);
                return (
                  <button
                    key={option.href}
                    type="button"
                    aria-pressed={selected}
                    onClick={() => toggleFavorite(option.href)}
                    className={cn(
                      "flex items-center gap-3 rounded-lg border px-4 py-3 text-left text-sm transition-colors",
                      selected
                        ? "border-primary/50 bg-primary/10 text-foreground"
                        : "border-border bg-card text-muted-foreground hover:bg-accent hover:text-foreground",
                    )}
                  >
                    <Star
                      className={cn(
                        "h-4 w-4",
                        selected && "fill-current text-status-warning",
                      )}
                    />
                    {option.label}
                  </button>
                );
              })}
            </div>
          </PageSection>
        </>
      )}
    </PageShell>
  );
}

function PreferenceSelect({
  id,
  label,
  value,
  onChange,
  options,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: ReadonlyArray<readonly [string, string]>;
}) {
  return (
    <label htmlFor={id} className="space-y-2">
      <span className="block text-sm font-medium text-foreground">{label}</span>
      <Select
        id={id}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      >
        {options.map(([optionValue, optionLabel]) => (
          <option key={optionValue} value={optionValue}>
            {optionLabel}
          </option>
        ))}
      </Select>
    </label>
  );
}

export const Route = createFileRoute("/dashboard/account/preferences/")({
  component: AccountPreferencesPage,
});
