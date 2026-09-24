import { useLocation, useNavigate } from "@tanstack/react-router";

export function usePipelinePageParam() {
  const location = useLocation();
  const navigate = useNavigate();
  const params = new URLSearchParams(location.searchStr);
  const raw = Number(params.get("pipelinePage") ?? 1);
  const page =
    Number.isSafeInteger(raw) && raw > 0 && raw <= 1_000_000 ? raw : 1;
  const setPageIndex = (index: number) => {
    const search = new URLSearchParams(location.searchStr);
    search.set("pipelinePage", String(index + 1));
    void navigate({
      to: `${location.pathname}?${search}`,
      replace: true,
      resetScroll: false,
    });
  };
  return { pageIndex: page - 1, page, setPageIndex };
}
