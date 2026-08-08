import type { ReactNode } from "react";

export function safeLink(href: string): string | null {
  const value = href.trim();
  if (value.startsWith("/") || /^https:\/\//i.test(value)) return value;
  return null;
}

function inline(text: string): ReactNode[] {
  const clean = text.replace(/<[^>]*>/g, "");
  const parts = clean.split(/(\[[^\]]+\]\([^)]+\)|`[^`]+`|\*\*[^*]+\*\*)/g);
  return parts.map((part, i) => {
    const link = part.match(/^\[([^\]]+)\]\(([^)]+)\)$/);
    if (link) {
      const href = safeLink(link[2]);
      return href ? (
        <a
          key={i}
          href={href}
          target={href.startsWith("/") ? undefined : "_blank"}
          rel="noopener noreferrer"
          className="text-primary underline"
        >
          {link[1]}
        </a>
      ) : (
        <span key={i}>{link[1]}</span>
      );
    }
    if (part.startsWith("`") && part.endsWith("`"))
      return (
        <code key={i} className="rounded bg-muted px-1 font-mono text-xs">
          {part.slice(1, -1)}
        </code>
      );
    if (part.startsWith("**") && part.endsWith("**"))
      return <strong key={i}>{part.slice(2, -2)}</strong>;
    return part;
  });
}

export function SafeMarkdown({
  children,
  streaming = false,
}: {
  children: string;
  streaming?: boolean;
}) {
  const lines = children.split("\n");
  return (
    <div
      className="space-y-2 break-words text-sm select-text"
      aria-live={streaming ? "polite" : undefined}
      aria-busy={streaming}
    >
      {lines.map((line, i) =>
        line.startsWith("- ") ? (
          <div key={i} className="flex gap-2">
            <span aria-hidden>•</span>
            <span>{inline(line.slice(2))}</span>
          </div>
        ) : (
          <p key={i}>{inline(line)}</p>
        ),
      )}
    </div>
  );
}
