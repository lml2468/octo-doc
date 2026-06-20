import { defineConfig } from 'tsup';

/**
 * Build config. Two entrypoints — the server (`index`) and the CLI (`cli`) —
 * bundled to ESM for Node 22. The browser overlay (`src/overlay.js`) is copied
 * verbatim into dist by the `onSuccess` hook: it is shipped to clients as-is and
 * must not be transpiled or bundled.
 */
export default defineConfig({
  entry: ['src/index.ts', 'src/cli.ts'],
  format: ['esm'],
  target: 'node22',
  platform: 'node',
  outDir: 'dist',
  clean: true,
  sourcemap: true,
  external: ['pg', '@aws-sdk/client-s3', 'better-sqlite3'],
  onSuccess: 'cp src/overlay.js dist/overlay.js',
});
