// Route files are the eslint-exempted surface for direct router imports.
// Body lives in the `-`-prefixed co-located module so the route stays
// code-splittable while the co-located test imports the component directly.
import { createFileRoute } from '@tanstack/react-router';
import RBACPage from './-page';

export const Route = createFileRoute('/dashboard/rbac/')({
  // ?tab= deep-link (P2.4): typed passthrough — useTabParam's allowlist stays the real validator.
  validateSearch: (search: Record<string, unknown>) =>
    search as { tab?: string } & Record<string, unknown>,
  component: RBACPage,
});
