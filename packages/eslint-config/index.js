// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Shared ESLint flat config for the Dropway workspace.
//
// Dependency-light by design: this base only encodes project-wide conventions
// that need no plugins. App-specific packages (the Next.js dashboard, the Worker)
// extend this and add their own framework plugins on top.
//
// Usage in a workspace package's eslint.config.js:
//   import dropway from "@dropway/eslint-config";
//   export default [...dropway, /* package-specific overrides */];

/** @type {import("eslint").Linter.Config[]} */
const config = [
  {
    // Build artifacts and vendored code are never linted.
    ignores: [
      "**/dist/**",
      "**/build/**",
      "**/.next/**",
      "**/node_modules/**",
      "**/*.tsbuildinfo",
    ],
  },
  {
    files: ["**/*.{js,mjs,cjs,ts,tsx}"],
    rules: {
      // Conventions that hold repo-wide regardless of framework.
      eqeqeq: ["error", "always"],
      "no-var": "error",
      "prefer-const": "error",
      "no-console": ["warn", { allow: ["warn", "error"] }],
    },
  },
];

export default config;
