import { lazy, Suspense } from "react";
import { createFileRoute, redirect } from "@tanstack/react-router";

import { Skeleton } from "@/components/ui/skeleton";

// Dev-only component gallery (plan 031 phase 8). It is enabled by the Vite dev
// server or by building with VITE_UI_GALLERY=1 (the Playwright web server does
// this). `__UI_GALLERY__` is a vite `define` literal, so a normal production
// build folds it to `false`, drops the dynamic import and never emits the
// gallery chunk. Do not add static imports of `./-gallery` here: they would
// pull it into the eager route graph.
const Gallery = __UI_GALLERY__ ? lazy(() => import("./-gallery")) : () => null;

export const Route = createFileRoute("/dashboard/dev/ui/")({
  beforeLoad: () => {
    if (!__UI_GALLERY__) throw redirect({ to: "/dashboard" });
  },
  component: GalleryRoute,
});

function GalleryRoute() {
  return (
    <Suspense fallback={<Skeleton className="h-40 w-full" />}>
      <Gallery />
    </Suspense>
  );
}
