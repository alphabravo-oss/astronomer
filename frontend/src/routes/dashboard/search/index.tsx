// Route files are the eslint-exempted surface for direct router imports.
// Body lives in the `-`-prefixed co-located module so the route stays
// code-splittable while the co-located test imports the page's named exports.
import { createFileRoute } from '@tanstack/react-router';
import { SearchPage } from './-page';

export const Route = createFileRoute('/dashboard/search/')({
  component: SearchPage,
});
