import { createContext, useContext } from "react";
import type { CharlieContextOption } from "@/lib/api/charlie";

type CharlieState = {
  open: boolean;
  setOpen: (v: boolean) => void;
  resources: CharlieContextOption[];
  remove: (id: string) => void;
  add: (v: CharlieContextOption) => void;
};

export const CharlieContext = createContext<CharlieState | null>(null);
export const useCharlie = () => {
  const v = useContext(CharlieContext);
  if (!v) throw new Error("useCharlie must be inside CharlieShell");
  return v;
};
