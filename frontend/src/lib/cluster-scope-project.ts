import { useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "@tanstack/react-router";
import { getProject } from "@/lib/api/projects";
import {
  projectInCluster,
  projectSelectionSearch,
} from "./cluster-scope-collection";
import { useClusterScopeStore } from "./cluster-scope";
import { toastApiError } from "./toast";

let latestSelection = 0;

/** All pickers replace project and namespaces together; a late lookup cannot win. */
export function useProjectSelection(clusterId?: string) {
  const location = useLocation();
  const navigate = useNavigate();
  const ownedSelection = useRef(0);
  useEffect(
    () => () => {
      if (ownedSelection.current === latestSelection) latestSelection++;
    },
    [clusterId, location.pathname],
  );
  const [pending, setPending] = useState(false);
  return {
    pending,
    select: async (id: string) => {
      const request = ++latestSelection;
      ownedSelection.current = request;
      const commit = (namespaces?: readonly string[]) => {
        const search = projectSelectionSearch(
          new URLSearchParams(location.searchStr),
          id,
          namespaces,
        );
        if (clusterId)
          useClusterScopeStore
            .getState()
            .setClusterScope(
              clusterId,
              id ? [...(namespaces ?? [])] : null,
              id || null,
            );
        void navigate({
          to: `${location.pathname}${search.size ? `?${search}` : ""}`,
          replace: true,
        });
      };
      commit();
      if (!id) {
        setPending(false);
        return;
      }
      setPending(true);
      try {
        const project = await getProject(id);
        if (request !== latestSelection) return;
        if (clusterId && !projectInCluster(project, clusterId))
          throw new Error("Project does not belong to this cluster");
        commit(project.namespaces);
      } catch (error) {
        if (request === latestSelection)
          toastApiError("Project scope unavailable", error);
      } finally {
        setPending(false);
      }
    },
  };
}
