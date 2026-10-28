import eslint from "@eslint/js";
import reactHooks from "eslint-plugin-react-hooks";
import globals from "globals";
import tseslint from "typescript-eslint";

export default tseslint.config(
  {
    ignores: ["dist/**", "src/routeTree.gen.ts"],
  },
  eslint.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx}"],
    plugins: {
      "react-hooks": reactHooks,
    },
    languageOptions: {
      ecmaVersion: "latest",
      globals: globals.browser,
    },
    rules: {
      "react-hooks/rules-of-hooks": "error",
      // Design-system guards (docs/UI_DESIGN_PLAN.md T1.8). Confirmation and
      // notices go through ConfirmDialog / toast, never the browser globals.
      "no-restricted-globals": [
        "error",
        { name: "confirm", message: "Use ConfirmDialog (components/confirm-dialog) instead of window.confirm." },
        { name: "alert", message: "Use toast (sonner) or an Alert instead of window.alert." },
      ],
      "no-restricted-properties": [
        "error",
        { object: "window", property: "confirm", message: "Use ConfirmDialog (components/confirm-dialog) instead of window.confirm." },
        { object: "window", property: "alert", message: "Use toast (sonner) or an Alert instead of window.alert." },
      ],
      // Colour lives in styles/globals.css tokens only.
      "no-restricted-syntax": [
        "error",
        {
          selector: "Literal[value=/#[0-9a-fA-F]{3,8}\\b/]",
          message: "No hex colour literals in src/. Use a token from styles/globals.css (bg-primary, var(--sev-high), …).",
        },
        {
          selector: "TemplateElement[value.raw=/#[0-9a-fA-F]{3,8}\\b/]",
          message: "No hex colour literals in src/. Use a token from styles/globals.css.",
        },
      ],
      // Underscore-prefixed parameters document intentionally unused callback
      // slots while still catching accidental unused bindings.
      "@typescript-eslint/no-unused-vars": [
        "error",
        {
          argsIgnorePattern: "^_",
          caughtErrorsIgnorePattern: "^_",
          varsIgnorePattern: "^_",
        },
      ],
    },
  },
  {
    // Tests assert on literal values (for example the <meta theme-color> hex).
    files: ["**/*.test.{ts,tsx}"],
    rules: { "no-restricted-syntax": "off" },
  },
  {
    files: ["vite.config.ts", "vitest.config.ts"],
    languageOptions: {
      globals: globals.node,
    },
  },
);
