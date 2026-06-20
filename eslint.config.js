import js from '@eslint/js';
import tseslint from 'typescript-eslint';
import prettier from 'eslint-config-prettier';

/**
 * Flat ESLint config. Type-checked rules apply only to the TypeScript sources
 * under src/test/bench; everything else (browser overlay, skill shell scripts,
 * config files) is ignored so type-aware linting never runs on non-project files.
 */
export default tseslint.config(
  {
    ignores: [
      'dist/**',
      'node_modules/**',
      'data/**',
      'coverage/**',
      'src/overlay.js',
      'skill/**',
      'migrations/*.js',
      'bin/**',
      'bench/*.js',
      '*.config.ts',
      '*.config.js',
      'eslint.config.js',
    ],
  },
  {
    files: ['src/**/*.ts', 'test/**/*.ts', 'bench/**/*.ts', 'scripts/**/*.ts'],
    extends: [js.configs.recommended, ...tseslint.configs.recommendedTypeChecked],
    languageOptions: {
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
    rules: {
      // Quality bar: no any unless explicitly justified with a disable comment.
      '@typescript-eslint/no-explicit-any': 'error',
      '@typescript-eslint/explicit-function-return-type': 'off',
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
      complexity: ['error', 10],
      'max-depth': ['error', 4],
      'no-console': 'error',
    },
  },
  {
    // Tests, benches, scripts, and process entrypoints may log and skip the cap.
    files: ['test/**/*.ts', 'bench/**/*.ts', 'scripts/**/*.ts', 'src/cli.ts', 'src/index.ts'],
    rules: {
      'no-console': 'off',
      complexity: 'off',
      '@typescript-eslint/no-non-null-assertion': 'off',
    },
  },
  prettier,
);
