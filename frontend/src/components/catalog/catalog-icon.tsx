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
  const failed = Boolean(src && failedSrc === src);
  const fallback = useMemo(() => initials(label), [label]);

  return (
    <div
      className={cn(
        "flex flex-none items-center justify-center overflow-hidden rounded-lg bg-muted/60 text-muted-foreground",
        className,
      )}
      aria-label={`${label} icon`}
    >
      {src && !failed ? (
        <img
          src={src}
          alt=""
          loading="lazy"
          className={cn("object-contain", imageClassName)}
          onError={() => setFailedSrc(src)}
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
