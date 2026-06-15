import nextVitals from 'eslint-config-next/core-web-vitals';
import pluginQuery from '@tanstack/eslint-plugin-query';

const config = [
  {
    ignores: [
      '.next/**',
      'node_modules/**',
      'next-env.d.ts',
      'public/**',
      'coverage/**',
    ],
  },
  ...nextVitals,
  ...pluginQuery.configs['flat/recommended'],
  {
    rules: {
      'import/no-anonymous-default-export': 'off',
      'react-hooks/immutability': 'off',
      'react-hooks/preserve-manual-memoization': 'off',
      'react-hooks/purity': 'off',
      'react-hooks/refs': 'off',
      'react-hooks/set-state-in-render': 'off',
      'react-hooks/set-state-in-effect': 'off',
      // Headless TanStack libraries (react-table, react-virtual) return
      // non-memoizable functions from their hooks by design; this React
      // Compiler rule flags that pattern. Off alongside the rest of the
      // experimental React Compiler rule family already disabled above.
      'react-hooks/incompatible-library': 'off',
      'react/no-unescaped-entities': 'off',
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
      // Ban direct imports of next/navigation and next/link. All navigation
      // must go through the adapter layer so behavior stays centralized. The
      // adapter files themselves are exempted via the override block below.
      'no-restricted-imports': [
        'error',
        {
          paths: [
            {
              name: 'next/navigation',
              message:
                'Do not import next/navigation directly. Use @/lib/navigation (client) or @/lib/navigation-server (server) instead.',
            },
            {
              name: 'next/link',
              message:
                'Do not import next/link directly. Use @/lib/link instead.',
            },
          ],
        },
      ],
    },
  },
  {
    // The adapter layer is the single place allowed to import next/navigation
    // and next/link directly; everything else must go through these wrappers.
    files: [
      'src/lib/navigation.ts',
      'src/lib/navigation-server.ts',
      'src/lib/link.tsx',
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
