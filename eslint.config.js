// Flat ESLint config (ESLint 9). Lightweight: catch real errors, stay out of
// the way of the ported-verbatim core (which deliberately mirrors upstream).
import js from '@eslint/js';

export default [
  // overlay.js is browser code served verbatim to clients — not server code.
  { ignores: ['src/overlay.js', 'node_modules/**', 'data/**'] },
  js.configs.recommended,
  {
    files: ['src/**/*.js', 'test/**/*.js', 'bench/**/*.js', 'migrations/**/*.js', 'bin/**/*.js'],
    languageOptions: {
      ecmaVersion: 2023,
      sourceType: 'module',
      globals: {
        process: 'readonly', console: 'readonly', fetch: 'readonly',
        Request: 'readonly', Response: 'readonly', Blob: 'readonly',
        FormData: 'readonly', URL: 'readonly', URLSearchParams: 'readonly',
        Buffer: 'readonly', setTimeout: 'readonly', clearTimeout: 'readonly',
        performance: 'readonly', crypto: 'readonly', structuredClone: 'readonly',
      },
    },
    rules: {
      'no-unused-vars': ['warn', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
      'no-empty': ['warn', { allowEmptyCatch: true }],
      'no-control-regex': 'off',
    },
  },
];
