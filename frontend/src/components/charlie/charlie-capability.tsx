import { LoadingPanel } from "@/components/charlie/loading-panel";
import {
  lazy,
  Suspense,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { useLocation } from "@tanstack/react-router";
import type { CharlieContextOption } from "@/lib/api/charlie";
import { DrawerShell } from "@/components/ui/drawer-shell";
import { contextForRoute } from "./context-registry";
import { CharlieContext as Context } from "./charlie-context";

const CharlieDrawer = lazy(() =>
  import("./charlie-drawer").then((module) => ({
    default: module.CharlieDrawer,
  })),
);

export function CharlieCapability({ children }: { children?: ReactNode }) {
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
      // ⌘J / Ctrl+J toggles the assistant drawer (header icon does the same).
      if (!editing && (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "j") {
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
      {children}
      {open && (
        <Suspense
          fallback={
            <DrawerShell
              title="Charlie"
              onClose={() => setOpen(false)}
              panelClassName="max-w-xl max-sm:max-w-none"
            >
              <LoadingPanel title="Loading Charlie…" className="m-4" />
            </DrawerShell>
          }
        >
          <CharlieDrawer />
        </Suspense>
      )}
    </Context.Provider>
  );
}
