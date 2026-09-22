import { useEffect } from "react";

// Sets document.title from the given parts (most specific first), joined
// with the product name, and restores whatever title was set before this
// hook took effect once its caller unmounts.
export function useDocumentTitle(parts: readonly string[]): void {
  useEffect(() => {
    const previous = document.title;
    document.title = [...parts.filter(Boolean), "Astronomer"].join(" · ");
    return () => {
      document.title = previous;
    };
  }, [parts]);
}
