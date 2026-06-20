import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: ['test/**/*.test.ts'],
    // E2E + chaos tests spawn real servers/processes; give them room and run in
    // forks so port allocation and signals behave like production.
    testTimeout: 30_000,
    hookTimeout: 30_000,
    pool: 'forks',
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html', 'lcov'],
      include: ['src/**/*.ts'],
      exclude: [
        'src/overlay.js',
        'src/index.ts',
        'src/cli.ts',
        'src/**/index.ts',
        'src/**/*.types.ts',
        'src/**/types.ts',
      ],
      thresholds: { lines: 85, functions: 85, branches: 85, statements: 85 },
    },
  },
});
