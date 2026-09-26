import { Package } from "lucide-react";
import { useMemo, useState } from "react";
import { cn } from "@/lib/utils";

function initials(label: string): string {
  return label
    .split(/[\s._/-]+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase())
    .join("");
}

/** Match the management UI's img-src 'self' data: policy before rendering. */
export function isCatalogIconSourceAllowed(src?: string): boolean {
  const value = src?.trim();
  if (!value) return false;
  if (value.startsWith("data:image/")) return true;
  if (typeof window === "undefined") return value.startsWith("/");
  try {
    const url = new URL(value, window.location.origin);
    return (
      (url.protocol === "http:" || url.protocol === "https:") &&
      url.origin === window.location.origin
    );
  } catch {
    return false;
  }
}

export function CatalogIcon({
  src,
  label,
  className,
  imageClassName,
}: {
  src?: string;
  label: string;
  className?: string;
  imageClassName?: string;
}) {
  const [failedSrc, setFailedSrc] = useState<string>();
  const loadableSrc = isCatalogIconSourceAllowed(src) ? src?.trim() : undefined;
  const failed = Boolean(loadableSrc && failedSrc === loadableSrc);
  const fallback = useMemo(() => initials(label), [label]);

  return (
    <div
      className={cn(
        "flex flex-none items-center justify-center overflow-hidden rounded-lg bg-muted/60 text-muted-foreground",
        className,
      )}
      aria-label={`${label} icon`}
    >
      {loadableSrc && !failed ? (
        <img
          src={loadableSrc}
          alt=""
          loading="lazy"
          className={cn("object-contain", imageClassName)}
          onError={() => setFailedSrc(loadableSrc)}
        />
      ) : fallback ? (
        <span
          className="text-xs font-semibold tracking-tight"
          aria-hidden="true"
        >
          {fallback}
        </span>
      ) : (
        <Package className="h-4 w-4" aria-hidden="true" />
      )}
    </div>
  );
}
