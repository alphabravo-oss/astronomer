import { useCallback } from "react";
import { useLocation, useNavigate } from "@tanstack/react-router";

const WORKLOADS = new Set([
  "deployments",
  "statefulsets",
  "daemonsets",
  "replicasets",
  "jobs",
  "cronjobs",
]);
export function safeWorkloadOrigin(
  value: string | null,
  clusterId: string,
): string | undefined {
  if (!value || !value.startsWith(`/dashboard/clusters/${clusterId}/`)) return;
  if (/[#\\]/.test(value) || hasControlOrSpace(value)) return;
  const [path, query] = value.split("?");
  let parts: string[];
  try {
    parts = path.split("/").filter(Boolean).map(decodeURIComponent);
  } catch {
    return;
  }
  if (
    parts.length !== 6 ||
    !WORKLOADS.has(parts[3]) ||
    parts.some(
      (part) =>
        !part ||
        part === "." ||
        part === ".." ||
        /[/%#\\]/.test(part) ||
        hasControlOrSpace(part),
    )
  )
    return;
  const search = new URLSearchParams(query);
  const safe = new URLSearchParams();
  for (const key of ["namespaces", "project", "tab"])
    if (search.has(key)) safe.set(key, search.get(key)!);
  safe.set("tab", "workload-pods");
  return `${path}?${safe}`;
}
export function podInvestigationHref(
  path: string,
  origin: string,
  search: string,
) {
  const next = new URLSearchParams(search);
  for (const key of [...next.keys()])
    if (!["namespaces", "project"].includes(key)) next.delete(key);
  next.set("origin", origin);
  return `${path}?${next}`;
}
export function useInvestigationParam(key: string, fallback = "") {
  const location = useLocation({ select: (location) => location });
  const navigate = useNavigate();
  const search = new URLSearchParams(location.searchStr);
  const setValue = useCallback(
    (value: string) => {
      const next = new URLSearchParams(location.searchStr);
      if (value) next.set(key, value);
      else next.delete(key);
      void navigate({
        to: `${location.pathname}?${next}`,
        replace: true,
        resetScroll: false,
      });
    },
    [key, location.pathname, location.searchStr, navigate],
  );
  return [search.get(key) ?? fallback, setValue] as const;
}

function hasControlOrSpace(value: string) {
  return [...value].some((character) => character.charCodeAt(0) <= 32);
}
