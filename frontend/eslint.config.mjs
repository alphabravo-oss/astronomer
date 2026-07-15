import js from '@eslint/js';
import globals from 'globals';
import tseslint from 'typescript-eslint';
import reactHooks from 'eslint-plugin-react-hooks';
import jsxA11y from 'eslint-plugin-jsx-a11y';
import pluginQuery from '@tanstack/eslint-plugin-query';

const config = [
  {
    ignores: [
      '.next/**',
      'dist/**',
      'node_modules/**',
      'public/**',
      'coverage/**',
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  reactHooks.configs['recommended-latest'],
  jsxA11y.flatConfigs.recommended,
  ...pluginQuery.configs['flat/recommended'],
  {
    // Node scripts and CommonJS/ESM config files at the frontend root.
    // shoot.mjs also evaluates snippets in a browser page context, hence the
    // browser globals.
    files: ['scripts/**/*.mjs', '*.config.{js,cjs,mjs}'],
    languageOptions: {
      globals: { ...globals.node, ...globals.commonjs, ...globals.browser },
    },
  },
  {
    rules: {
      // Underscore-prefixed bindings are the deliberate-discard convention
      // (e.g. `const { token: _legacyToken, ...rest }` in the auth-store
      // migration).
      '@typescript-eslint/no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
          caughtErrorsIgnorePattern: '^_',
        },
      ],
      // jsx-a11y flat/recommended is stricter than the six aria/alt rules
      // eslint-config-next enforced. The rules below fail on ~360 pre-existing
      // sites across the tree; keep them visible as warnings (still more a11y
      // coverage than before the migration) rather than blocking the gate.
      // Tightening them back to errors is post-merge cleanup.
      'jsx-a11y/label-has-associated-control': 'warn',
      'jsx-a11y/no-autofocus': 'warn',
      'jsx-a11y/click-events-have-key-events': 'warn',
      'jsx-a11y/no-static-element-interactions': 'warn',
      'jsx-a11y/no-noninteractive-element-interactions': 'warn',
      'jsx-a11y/interactive-supports-focus': 'warn',
      // Two latent issues these rules surfaced (clusters.pods key omitted its
      // namespace param; the pod-logs hook spread its whole query result) have
      // been fixed, so both rules are enforced as errors.
      '@tanstack/query/exhaustive-deps': 'error',
      '@tanstack/query/no-rest-destructuring': 'error',
      // Ban inline `queryKey: [...]` array literals at call sites. Cache keys
      // must come from the factory in src/lib/query-keys.ts so reads and
      // invalidations can never drift apart. The query-keys.ts file itself is
      // exempted via the override block below.
      'no-restricted-syntax': [
        'error',
        {
          selector: "Property[key.name='queryKey'] > ArrayExpression",
          message:
            'Do not inline queryKey arrays. Add/use a factory entry in src/lib/query-keys.ts instead.',
        },
      ],
      // Ban direct imports of @tanstack/react-router. All navigation must go
      // through the adapter layer (@/lib/navigation, @/lib/link) so behavior
      // stays centralized and test mocks keep working. The adapter files, the
      // route tree, and the router itself are exempted via the override block
      // below.
      // Next.js is gone (P2.6): the packages are uninstalled, so any surviving
      // or reintroduced `next/*` import must fail lint, not resolution.
      'no-restricted-imports': [
        'error',
        {
          paths: [
            {
              name: '@tanstack/react-router',
              message:
                'Do not import @tanstack/react-router directly. Use @/lib/navigation or @/lib/link instead.',
            },
          ],
          patterns: [
            {
              group: ['next', 'next/*', 'next-themes'],
              message:
                'Next.js was removed in the Vite/TanStack migration. Use the adapter layer (@/lib/navigation, @/lib/link) and native equivalents instead.',
            },
          ],
        },
      ],
    },
  },
  {
    // The adapter layer, route files, and the router module are the only
    // places allowed to import @tanstack/react-router directly; everything
    // else must go through the wrappers.
    files: [
      'src/lib/navigation.ts',
      'src/lib/link.tsx',
      'src/routes/**',
      'src/router.tsx',
      'src/main.tsx',
    ],
    rules: {
      'no-restricted-imports': 'off',
    },
  },
  {
    // The query-key factory is the single source of truth, so it is the one
    // place where inline queryKey arrays (and array construction) are allowed.
    files: ['src/lib/query-keys.ts'],
    rules: {
      'no-restricted-syntax': 'off',
    },
  },
];

export default config;
