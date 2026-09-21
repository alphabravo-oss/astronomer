import { createFileRoute, Outlet } from "@tanstack/react-router";
/**
 * Project detail layout — adds the tab strip shared by every sub-route under
 * `/dashboard/projects/[id]/`. Each tab is its own page so deep-links keep
 * working; the layout just renders the project header + nav and slots in
 * the active page below.
 *
 * Tabs:
 *   Overview · Policy · Cloud Credentials · Quota
 *
 * The Overview tab is the historical default. The three new tabs (Policy /
 * Cloud Credentials / Quota) ship as part of the project-detail-tabs sprint.
 * Adding more is a one-line change to the `tabs` array.
 */
import { Link as RouterLink } from "@tanstack/react-router";
import { useLocation } from "@tanstack/react-router";
import {
  ArrowLeft,
  FolderKanban,
  Shield,
  KeyRound,
  Gauge,
  Loader2,
  LayoutDashboard,
  Package,
} from "lucide-react";
import { useProject } from "@/lib/hooks/projects";
import { cn } from "@/lib/utils";
import { PageHeader } from "@/components/ui/page";
import { TabsList } from "@/components/ui/tabs";

const tabs = [
  { key: "overview", label: "Overview", icon: LayoutDashboard, segment: "" },
  { key: "policy", label: "Policy", icon: Shield, segment: "/policy" },
  {
    key: "cloud-credentials",
    label: "Cloud Credentials",
    icon: KeyRound,
    segment: "/cloud-credentials",
  },
  { key: "catalogs", label: "Catalogs", icon: Package, segment: "/catalogs" },
  { key: "quota", label: "Quota", icon: Gauge, segment: "/quota" },
] as const;

function ProjectDetailLayout() {
  const params = Route.useParams();
  const id = params.id;
  const pathname = useLocation({ select: (location) => location.pathname });
  const { data: project, isLoading } = useProject(id);

  const base = `/dashboard/projects/${id}`;

  // Match the tab whose full path is the longest prefix of the current URL.
  // Overview matches when nothing else does (i.e. we're sitting on the bare
  // /projects/[id] route or any unknown nested path).
  const activeKey = (() => {
    const remaining = pathname.startsWith(base)
      ? pathname.slice(base.length)
      : "";
    const match = tabs
      .filter(
        (t) =>
          t.segment &&
          (remaining === t.segment || remaining.startsWith(`${t.segment}/`)),
      )
      .sort((a, b) => b.segment.length - a.segment.length)[0];
    return match?.key ?? "overview";
  })();

  return (
    <div className="space-y-6">
      <RouterLink
        to="/dashboard/projects"
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to Projects
      </RouterLink>

      <PageHeader
        eyebrow="Project"
        title={
          <span className="inline-flex items-center gap-2">
            <FolderKanban className="h-5 w-5 shrink-0 text-muted-foreground" />
            {isLoading ? (
              <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
            ) : (
              project?.displayName || project?.name || "Project"
            )}
          </span>
        }
        description={project?.description}
      />

      <div className="border-b border-border">
        <TabsList aria-label="Project">
          {tabs.map((tab) => {
            const Icon = tab.icon;
            const href = `${base}${tab.segment}`;
            const active = activeKey === tab.key;
            return (
              <RouterLink
                key={tab.key}
                to={href}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "flex items-center gap-2 pb-3 text-sm font-medium border-b-2 transition-colors",
                  active
                    ? "border-foreground text-foreground"
                    : "border-transparent text-muted-foreground hover:text-foreground",
                )}
              >
                <Icon className="h-4 w-4" />
                {tab.label}
              </RouterLink>
            );
          })}
        </TabsList>
      </div>

      <div className="animate-fade-in">
        <Outlet />
      </div>
    </div>
  );
}

export const Route = createFileRoute("/dashboard/projects/$id")({
  component: ProjectDetailLayout,
});
