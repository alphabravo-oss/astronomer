import { Orbit } from "lucide-react";

/** Operator-branded logo when configured, falling back to the built-in mark. */
export function BrandMark({
  logoUrl,
  productName,
}: {
  logoUrl: string | undefined;
  productName: string;
}) {
  if (logoUrl) {
    return (
      <img
        src={logoUrl}
        alt={productName}
        className="h-10 w-10 rounded-xl object-contain"
      />
    );
  }
  return (
    <div className="w-10 h-10 rounded-xl bg-linear-to-br from-blue-500 to-violet-600 flex items-center justify-center">
      <Orbit className="h-5 w-5 text-white" />
    </div>
  );
}

/** Operator-configured banner text shown above the login form, if any. */
export function LoginBanner({ text }: { text: string | undefined }) {
  if (!text) return null;
  return (
    <p
      role="status"
      className="rounded-md border border-border bg-muted/40 px-3 py-2 text-center text-xs text-muted-foreground"
    >
      {text}
    </p>
  );
}
