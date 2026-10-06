import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { getCharlieOverview } from "@/lib/api/charlie";
import { productModePresentation } from "@/components/charlie/charlie-shell";
import { capitalize, cn } from "@/lib/utils";
import { queryKeys } from "@/lib/query-keys";
import { mergeCharlieSearch } from "@/components/charlie/admin-utils";
import { TabStrip } from "@/components/ui/tabs";
import { ResourceMasthead, PageShell } from "@/components/ui/page";
import { Approvals } from "./-approvals";
import { Conversations } from "./-conversations";
import { Findings } from "./-findings";
import { Investigations } from "./-investigations";

export const CHARLIE_HUB_TABS = [
  "conversations",
  "investigations",
  "findings",
  "approvals",
] as const;
type Tab = (typeof CHARLIE_HUB_TABS)[number];
function isCharlieTab(value: unknown): value is Tab {
  return (
    typeof value === "string" && CHARLIE_HUB_TABS.some((tab) => tab === value)
  );
}
export function normalizeCharlieTab(value: string | null): Tab {
  return isCharlieTab(value) ? value : "conversations";
}
export const Route = createFileRoute("/dashboard/charlie/")({
  validateSearch: (search: Record<string, unknown>) => ({
    tab: isCharlieTab(search.tab) ? search.tab : undefined,
    session: typeof search.session === "string" ? search.session : undefined,
  }),
  component: CharlieHub,
});

function CharlieHub() {
  const params = new URLSearchParams(
    useLocation({ select: (location) => location.searchStr }),
  );
  const navigate = useNavigate();
  const tab = normalizeCharlieTab(params.get("tab"));
  const overview = useQuery({
    queryKey: queryKeys.charlie.overview,
    queryFn: getCharlieOverview,
  });
  const mode = productModePresentation(overview.data?.mode);
  const set = (updates: Record<string, string | undefined>) => {
    void navigate({
      to: `/dashboard/charlie?${mergeCharlieSearch(params, updates)}`,
    });
  };
  return (
    <PageShell>
      <ResourceMasthead
        title="Charlie"
        status={
          <span
            className={cn(
              "rounded-full border px-2.5 py-0.5 text-xs font-semibold",
              mode.badgeClass,
            )}
            aria-label={`Current Charlie mode: ${mode.label}`}
            data-testid="charlie-hub-mode-badge"
            data-mode={mode.key}
          >
            Mode: {mode.label}
          </span>
        }
        description={
          <>
            Conversations, investigations, findings, and explicitly authorized
            actions.
            <span className="mt-1 block text-xs">{mode.ceiling}</span>
          </>
        }
      />
      <TabStrip
        tabs={CHARLIE_HUB_TABS.map((t) => ({ key: t, label: capitalize(t) }))}
        value={tab}
        onChange={(next) => set({ tab: next })}
        aria-label="Charlie sections"
      />
      <div
        id={`charlie-hub-panel-${tab}`}
        role="tabpanel"
        tabIndex={0}
        aria-labelledby={`tab-${tab}`}
      >
        {tab === "conversations" && (
          <Conversations
            selected={params.get("session")}
            onSelect={(id) => set({ session: id })}
          />
        )}
        {tab === "investigations" && (
          <Investigations
            selected={params.get("incident")}
            onSelect={(id) => set({ incident: id })}
            params={params}
            set={set}
          />
        )}
        {tab === "findings" && (
          <Findings
            selected={params.get("finding")}
            onSelect={(id) => set({ finding: id })}
            params={params}
            set={set}
          />
        )}
        {tab === "approvals" && <Approvals selected={params.get("approval")} />}
      </div>
    </PageShell>
  );
}
