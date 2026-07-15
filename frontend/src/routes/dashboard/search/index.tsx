// Route files are the eslint-exempted surface for direct router imports.
// Body lives in the `-`-prefixed co-located module so the route stays
// code-splittable while the co-located test imports the page's named exports.
import { createFileRoute } from '@tanstack/react-router';
import { SearchPage } from './-page';

export const Route = createFileRoute('/dashboard/search/')({
  // Deep-link contract (P2.4): typed passthrough — unrelated params survive.
  validateSearch: (search: Record<string, unknown>) =>
    search as {
      type?: string;
      namespace?: string;
      label?: string;
      name?: string;
    } & Record<string, unknown>,
  component: SearchPage,
});
