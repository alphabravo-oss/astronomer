// Route files are the eslint-exempted surface for direct router imports.
// Body lives in the `-`-prefixed co-located module so the route stays
// code-splittable while the co-located test imports the component directly.
import { createFileRoute } from '@tanstack/react-router';
import SCIMTokensPage from './-page';

export const Route = createFileRoute('/dashboard/settings/auth/scim-tokens/')({
  component: SCIMTokensPage,
});
