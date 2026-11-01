import { registerHooks } from 'node:module';
import { defineConfig, globalIgnores } from 'eslint/config';
import globals from 'globals';

// typescript-eslint parses with the TypeScript 6 compiler API. TypeScript 7,
// which the build uses for `tsc`, no longer ships that JavaScript API, so this
// lint process alone resolves `typescript` to @typescript/typescript6.
registerHooks({
  resolve(specifier, context, nextResolve) {
    return nextResolve(specifier === 'typescript' ? '@typescript/typescript6' : specifier, context);
  },
});

// Imported after the hook is registered so the parser picks up TypeScript 6.
const { default: js } = await import('@eslint/js');
const { default: tseslint } = await import('typescript-eslint');
const { default: reactHooks } = await import('eslint-plugin-react-hooks');

export default defineConfig([
  globalIgnores(['dist', 'node_modules', 'test-results', 'playwright-report']),
  js.configs.recommended,
  tseslint.configs.recommended,
  {
    files: ['**/*.{ts,tsx}'],
    extends: [reactHooks.configs.flat.recommended],
    languageOptions: {
      globals: globals.browser,
    },
    rules: {
      'react-hooks/exhaustive-deps': 'error',
      'react-hooks/set-state-in-effect': 'error',
    },
  },
  {
    files: ['*.{js,ts}', 'test/e2e/**/*.ts'],
    languageOptions: {
      globals: globals.node,
    },
  },
  {
    files: ['public/**/*.js'],
    languageOptions: {
      sourceType: 'script',
      globals: globals.browser,
    },
  },
]);
