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
        'src/index.ts', // process entrypoint
        'src/cli.ts', // process entrypoint
        'src/**/index.ts', // barrels
        'src/**/*.types.ts',
        'src/**/types.ts',
        'src/types/**', // ambient declarations
        'src/http-context.ts', // type-only context shape
        // Backend-specific adapters: exercised by the contract suite against
        // real Postgres/MinIO service containers in CI, not in the default run.
        'src/storage/postgres.ts',
        'src/storage/s3.ts',
        'src/storage/migrate.ts',
      ],
      // Lines/statements/functions held at 85% (the primary coverage metric,
      // actually ~94%). Branch coverage is 80%: the remaining gap is defensive
      // null-coalescing micro-branches in the byte-equivalent core, where
      // contriving the last few branches adds no behavioral assurance.
      thresholds: { lines: 85, functions: 85, branches: 80, statements: 85 },
    },
  },
});
