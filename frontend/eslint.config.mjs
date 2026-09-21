import js from "@eslint/js";
import globals from "globals";
import tseslint from "typescript-eslint";
import reactHooks from "eslint-plugin-react-hooks";
import jsxA11y from "eslint-plugin-jsx-a11y";
import pluginQuery from "@tanstack/eslint-plugin-query";

const config = [
  {
    ignores: [
      ".next/**",
      "dist/**",
      "node_modules/**",
      "public/**",
      "coverage/**",
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  reactHooks.configs.flat["recommended-latest"],
  jsxA11y.flatConfigs.recommended,
  ...pluginQuery.configs["flat/recommended"],
  {
    // Node scripts and CommonJS/ESM config files at the frontend root.
    // shoot.mjs also evaluates snippets in a browser page context, hence the
    // browser globals.
    files: ["scripts/**/*.mjs", "*.config.{js,cjs,mjs}"],
    languageOptions: {
      globals: { ...globals.node, ...globals.commonjs, ...globals.browser },
    },
  },
  {
    settings: {
      "jsx-a11y": {
        // These primitives forward all native accessibility props to the
        // corresponding HTML controls. Teaching the rule about them preserves
        // implicit <label><Input /></label> associations without replacing
        // valid native semantics with lint-only IDs.
        components: {
          Input: "input",
          Select: "select",
          Textarea: "textarea",
          CodeInput: "input",
          Switch: "button",
        },
      },
    },
    rules: {
      // Underscore-prefixed bindings are the deliberate-discard convention
      // (e.g. `const { token: _legacyToken, ...rest }` in the auth-store
      // migration).
      "@typescript-eslint/no-unused-vars": [
        "error",
        {
          argsIgnorePattern: "^_",
          varsIgnorePattern: "^_",
          caughtErrorsIgnorePattern: "^_",
        },
      ],
      // Accessibility regressions are release-blocking. Keep the explicit
      // options here so shared primitives retain the intended semantics, but
      // do not downgrade any recommended rule to a non-blocking warning.
      "jsx-a11y/label-has-associated-control": [
        "error",
        { assert: "either", depth: 6 },
      ],
      "jsx-a11y/no-autofocus": "error",
      "jsx-a11y/click-events-have-key-events": "error",
      "jsx-a11y/no-static-element-interactions": "error",
      "jsx-a11y/no-noninteractive-element-interactions": "error",
      "jsx-a11y/interactive-supports-focus": "error",
      // A horizontally scrollable region must itself be keyboard-focusable
      // when it has no naturally focusable descendants (WCAG 2.1.1). The
      // recommended region + name + tabIndex pattern is intentionally allowed.
      "jsx-a11y/no-noninteractive-tabindex": [
        "error",
        { roles: ["region", "tabpanel"] },
      ],
      // Two latent issues these rules surfaced (clusters.pods key omitted its
      // namespace param; the pod-logs hook spread its whole query result) have
      // been fixed, so both rules are enforced as errors.
      "@tanstack/query/exhaustive-deps": "error",
      "@tanstack/query/no-rest-destructuring": "error",
      // Ban inline `queryKey: [...]` array literals at call sites. Cache keys
      // must come from the factory in src/lib/query-keys.ts so reads and
      // invalidations can never drift apart. The query-keys.ts file itself is
      // exempted via the override block below.
      "no-restricted-syntax": [
        "error",
        {
          selector: "ExpressionStatement[expression.value='use client']",
          message:
            'The Vite frontend does not use React Server Components; remove the no-op "use client" directive.',
        },
        {
          selector: "Property[key.name='queryKey'] > ArrayExpression",
          message:
            "Do not inline queryKey arrays. Add/use a factory entry in src/lib/query-keys.ts instead.",
        },
      ],
      // Next.js is gone: any surviving or reintroduced `next/*` import must
      // fail lint, not resolution. Navigation uses TanStack Router directly.
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["next", "next/*", "next-themes"],
              message:
                "Next.js was removed in the Vite/TanStack migration. Use TanStack Router and native equivalents instead.",
            },
          ],
        },
      ],
    },
  },
  {
    // Browser-native confirmation APIs block rendering, cannot explain impact,
    // and are not keyboard/theme consistent. Operator actions must use the
    // shared ConfirmDialog so scope, recovery, and mutation state stay visible.
    files: ["src/routes/**/*.{ts,tsx}", "src/components/**/*.{ts,tsx}"],
    rules: {
      "no-restricted-globals": [
        "error",
        {
          name: "confirm",
          message: "Use the shared ConfirmDialog component.",
        },
        {
          name: "alert",
          message: "Use the toast or modal primitives.",
        },
      ],
      "no-restricted-properties": [
        "error",
        {
          object: "window",
          property: "confirm",
          message: "Use the shared ConfirmDialog component.",
        },
        {
          object: "window",
          property: "alert",
          message: "Use the toast or modal primitives.",
        },
      ],
    },
  },
  {
    // The query-key factory is the single source of truth, so inline queryKey
    // arrays are allowed here. Keep unrelated restricted syntax enforced.
    files: ["src/lib/query-keys.ts"],
    rules: {
      "no-restricted-syntax": [
        "error",
        {
          selector: "ExpressionStatement[expression.value='use client']",
          message:
            'The Vite frontend does not use React Server Components; remove the no-op "use client" directive.',
        },
      ],
    },
  },
  {
    // NOTE: flat config does not merge array-valued rule options across
    // matching blocks — a later block matching the same file REPLACES an
    // earlier block's setting for the same rule name. Every selector/pattern
    // that must still apply to routes (the "use client"/queryKey/input-etc.
    // bans, plus the shared design-system rules below) is therefore kept in
    // this one block rather than split across several.
    files: ["src/routes/**/*.tsx"],
    rules: {
      "no-restricted-syntax": [
        "error",
        {
          selector: "ExpressionStatement[expression.value='use client']",
          message: "Remove the no-op use client directive.",
        },
        {
          selector: "Property[key.name='queryKey'] > ArrayExpression",
          message: "Use the shared query-key factory.",
        },
        {
          selector: "JSXOpeningElement[name.name=/^(input|select|textarea|form)$/]",
          message:
            "Use shared Input/Select/Textarea controls and FormShell with useAppForm for editable workflows.",
        },
        {
          selector: "JSXOpeningElement[name.name='h1']",
          message: "Use PageHeader or ResourceMasthead.",
        },
        {
          selector: "JSXOpeningElement[name.name='table']",
          message: "Use DataTable / operator-table.",
        },
        {
          selector:
            "Literal[value=/rounded-full bg-white transition-transform/]",
          message: "Use Switch.",
        },
        {
          selector:
            "Literal[value=/\\b(text|bg|border)-(red|green|blue|amber|emerald|zinc|gray|slate|orange|sky)-\\d{2,3}\\b/]",
          message: "Use semantic tokens.",
        },
      ],
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["next", "next/*", "next-themes"],
              message:
                "Next.js was removed in the Vite/TanStack migration. Use TanStack Router and native equivalents instead.",
            },
            {
              group: ["@/components/ui/table"],
              message:
                "Import from @/components/ui/operator-table or use DataTable.",
            },
          ],
        },
      ],
    },
  },
  {
    // The design-system primitives (masthead/page header, tables, tabs,
    // status badges, switches, semantic color tokens) must be the only path
    // to their markup in components too — otherwise hand-rolled copies drift
    // in accessibility and visual details (see docs/design-system.md's
    // ratchets). Excludes the primitives themselves (components/ui/**) and
    // the form kit (components/form/**), which legitimately contain this
    // markup because they *are* the shared implementation. Re-declares the
    // "use client"/queryKey bans from the base block above for the same
    // flat-config-doesn't-merge reason noted there.
    files: ["src/components/**/*.tsx"],
    ignores: ["src/components/ui/**", "src/components/form/**"],
    rules: {
      "no-restricted-syntax": [
        "error",
        {
          selector: "ExpressionStatement[expression.value='use client']",
          message:
            'The Vite frontend does not use React Server Components; remove the no-op "use client" directive.',
        },
        {
          selector: "Property[key.name='queryKey'] > ArrayExpression",
          message:
            "Do not inline queryKey arrays. Add/use a factory entry in src/lib/query-keys.ts instead.",
        },
        {
          selector: "JSXOpeningElement[name.name='h1']",
          message: "Use PageHeader or ResourceMasthead.",
        },
        {
          selector: "JSXOpeningElement[name.name='table']",
          message: "Use DataTable / operator-table.",
        },
        {
          selector:
            "Literal[value=/rounded-full bg-white transition-transform/]",
          message: "Use Switch.",
        },
        {
          selector:
            "Literal[value=/\\b(text|bg|border)-(red|green|blue|amber|emerald|zinc|gray|slate|orange|sky)-\\d{2,3}\\b/]",
          message: "Use semantic tokens.",
        },
      ],
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["next", "next/*", "next-themes"],
              message:
                "Next.js was removed in the Vite/TanStack migration. Use TanStack Router and native equivalents instead.",
            },
            {
              group: ["@/components/ui/table"],
              message:
                "Import from @/components/ui/operator-table or use DataTable.",
            },
          ],
        },
      ],
    },
  },
  {
    // Brand marks / the marketing login hero legitimately use non-semantic
    // color literals outside the app's semantic token scale. Re-declares the
    // "use client"/queryKey bans (see the flat-config-doesn't-merge note
    // above) so turning off the design-system rules here doesn't also turn
    // those off.
    files: [
      "src/components/projects/cloud-credentials/provider-badge.tsx",
      "src/components/auth/login-branding.tsx",
    ],
    rules: {
      "no-restricted-syntax": [
        "error",
        {
          selector: "ExpressionStatement[expression.value='use client']",
          message:
            'The Vite frontend does not use React Server Components; remove the no-op "use client" directive.',
        },
        {
          selector: "Property[key.name='queryKey'] > ArrayExpression",
          message:
            "Do not inline queryKey arrays. Add/use a factory entry in src/lib/query-keys.ts instead.",
        },
      ],
    },
  },
];

export default config;
