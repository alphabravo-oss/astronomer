import {
  lazy,
  Suspense,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { useLocation } from "@tanstack/react-router";
import { Loader2, Sparkles } from "lucide-react";
import type { CharlieContextOption } from "@/lib/api/charlie";
import { DrawerShell } from "@/components/ui/drawer-shell";
import { contextForRoute } from "./context-registry";
import { CharlieContext as Context } from "./charlie-context";

const CharlieDrawer = lazy(() =>
  import("./charlie-drawer").then((module) => ({
    default: module.CharlieDrawer,
  })),
);

export { useCharlie } from "./charlie-context";
export { productModeCopy, productModePresentation } from "./charlie-mode";

export function CharlieShell({
  children,
  enabled = true,
}: {
  children: ReactNode;
  enabled?: boolean;
}) {
  return (
    <>
      {children}
      {enabled && <CharlieCapability />}
    </>
  );
}

function CharlieCapability() {
  const pathname = useLocation({ select: (location) => location.pathname });
  const [open, setOpen] = useState(false);
  const [manual, setManual] = useState<CharlieContextOption[]>([]);
  const [removed, setRemoved] = useState<string[]>([]);
  const routeResources = useMemo(() => contextForRoute(pathname), [pathname]);
  const resources = [...routeResources, ...manual]
    .filter((resource) => !removed.includes(`${resource.type}:${resource.id}`))
    .filter(
      (value, index, all) =>
        all.findIndex(
          (candidate) =>
            candidate.type === value.type && candidate.id === value.id,
        ) === index,
    );
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      const target = e.target;
      const editing =
        target instanceof HTMLElement &&
        (target.isContentEditable ||
          ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName));
      if (!editing && (e.metaKey || e.ctrlKey) && e.shiftKey && e.key === ".") {
        e.preventDefault();
        setOpen((v) => !v);
      }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, []);
  const value = {
    open,
    setOpen,
    resources,
    remove: (id: string) => setRemoved((v) => [...v, id]),
    add: (v: CharlieContextOption) => {
      const id = `${v.type}:${v.id}`;
      setRemoved((current) => current.filter((value) => value !== id));
      setManual((current) => [...current, v]);
    },
  };
  return (
    <Context.Provider value={value}>
      <button
        onClick={() => setOpen(true)}
        aria-label="Open Charlie assistant"
        aria-expanded={open}
        aria-controls="charlie-assistant-drawer"
        title="Open Charlie (Ctrl/⌘ Shift .)"
        className="fixed bottom-5 right-5 z-40 flex h-12 items-center gap-2 rounded-full bg-primary px-4 text-primary-foreground shadow-lg focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none"
      >
        <Sparkles className="h-5 w-5" />
        <span className="hidden sm:inline">Charlie</span>
      </button>
      {open && (
        <Suspense
          fallback={
            <DrawerShell
              title="Charlie"
              onClose={() => setOpen(false)}
              panelClassName="max-w-xl max-sm:max-w-none"
            >
              <div
                role="status"
                className="flex h-full min-h-48 items-center justify-center gap-2 text-sm text-muted-foreground"
              >
                <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" />
                Loading Charlie…
              </div>
            </DrawerShell>
          }
        >
          <CharlieDrawer />
        </Suspense>
      )}
    </Context.Provider>
  );
}
