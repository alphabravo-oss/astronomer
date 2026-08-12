import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

export function safeLink(href: string): string | null {
  const value = href.trim();
  if (
    value.startsWith("/") ||
    value.startsWith("#") ||
    /^https:\/\//i.test(value)
  )
    return value;
  return null;
}

const markdownComponents: Components = {
  h1: ({ children }) => (
    <h1 className="mt-4 text-base font-semibold first:mt-0">{children}</h1>
  ),
  h2: ({ children }) => (
    <h2 className="mt-4 text-sm font-semibold first:mt-0">{children}</h2>
  ),
  h3: ({ children }) => (
    <h3 className="mt-3 text-sm font-semibold first:mt-0">{children}</h3>
  ),
  h4: ({ children }) => (
    <h4 className="mt-3 text-sm font-medium first:mt-0">{children}</h4>
  ),
  p: ({ children }) => <p className="my-2 first:mt-0 last:mb-0">{children}</p>,
  ul: ({ children }) => (
    <ul className="my-2 list-disc space-y-1 pl-5">{children}</ul>
  ),
  ol: ({ children }) => (
    <ol className="my-2 list-decimal space-y-1 pl-5">{children}</ol>
  ),
  li: ({ children }) => <li className="pl-0.5">{children}</li>,
  blockquote: ({ children }) => (
    <blockquote className="my-2 border-l-2 border-border pl-3 text-muted-foreground">
      {children}
    </blockquote>
  ),
  a: ({ href, children }) => {
    const safe = typeof href === "string" ? safeLink(href) : null;
    return safe ? (
      <a
        href={safe}
        target={
          safe.startsWith("/") || safe.startsWith("#") ? undefined : "_blank"
        }
        rel="noopener noreferrer"
        className="text-primary underline underline-offset-2"
      >
        {children}
      </a>
    ) : (
      <span>{children}</span>
    );
  },
  // Model-authored Markdown must not create ambient network requests or tracking
  // pixels. Product evidence is attached through the explicit citation surface.
  img: ({ alt }) => (
    <span className="text-muted-foreground">
      {alt ? `[Image omitted: ${alt}]` : "[Image omitted]"}
    </span>
  ),
  code: ({ children, className }) => (
    <code
      className={`${className ?? ""} rounded bg-muted px-1 py-0.5 font-mono text-xs`}
    >
      {children}
    </code>
  ),
  pre: ({ children }) => (
    <pre className="my-2 max-w-full overflow-x-auto rounded-md border bg-muted/50 p-3 text-xs [&>code]:bg-transparent [&>code]:p-0">
      {children}
    </pre>
  ),
  hr: () => <hr className="my-3 border-border" />,
  table: ({ children }) => (
    <div className="my-2 max-w-full overflow-x-auto">
      <table className="w-full border-collapse text-left text-xs">
        {children}
      </table>
    </div>
  ),
  th: ({ children }) => (
    <th className="border border-border bg-muted px-2 py-1 font-semibold">
      {children}
    </th>
  ),
  td: ({ children }) => (
    <td className="border border-border px-2 py-1 align-top">{children}</td>
  ),
};

export function SafeMarkdown({
  children,
  streaming = false,
}: {
  children: string;
  streaming?: boolean;
}) {
  return (
    <div
      className="break-words text-sm select-text"
      aria-live={streaming ? "polite" : undefined}
      aria-busy={streaming}
    >
      <ReactMarkdown
        skipHtml
        remarkPlugins={[remarkGfm]}
        components={markdownComponents}
        urlTransform={(url) => safeLink(url) ?? ""}
      >
        {children}
      </ReactMarkdown>
    </div>
  );
}
