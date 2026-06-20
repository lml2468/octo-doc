/**
 * Throughput/latency benchmark for the doc-render hot path (autocannon). Spawns
 * a server, publishes one doc, then hammers `GET /d/<slug>/v/1` — the path the
 * non-functional targets cover (p50 ≤ 50ms, p99 ≤ 200ms on 1 vCPU).
 *
 * Usage: tsx bench/run.ts [durationSeconds] [connections]
 */
import { spawn } from 'node:child_process';
import { mkdtempSync, rmSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import net from 'node:net';
import autocannon from 'autocannon';

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, '..');
const duration = Number(process.argv[2] ?? 10);
const connections = Number(process.argv[3] ?? 50);

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const s = net.createServer();
    s.listen(0, '127.0.0.1', () => {
      const addr = s.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      s.close(() => resolve(port));
    });
    s.on('error', reject);
  });
}

async function waitReady(base: string, ms = 8000): Promise<void> {
  const deadline = Date.now() + ms;
  for (;;) {
    try {
      if ((await fetch(`${base}/healthz`)).ok) return;
    } catch {
      // not up yet
    }
    if (Date.now() > deadline) throw new Error('server not ready');
    await new Promise((r) => setTimeout(r, 100));
  }
}

async function main(): Promise<void> {
  const dir = mkdtempSync(join(tmpdir(), 'octo-bench-'));
  const port = await freePort();
  const base = `http://127.0.0.1:${port}`;
  const srv = spawn(process.execPath, ['--import', 'tsx', join(root, 'src/index.ts')], {
    env: {
      ...process.env,
      DATA_DIR: dir,
      STORAGE: 'sqlite+fs',
      PORT: String(port),
      LOG_LEVEL: 'silent',
      COOKIE_SECURE: 'false',
    },
    stdio: 'ignore',
  });
  const cleanup = (): void => {
    try {
      srv.kill('SIGKILL');
    } catch {
      /* already gone */
    }
    rmSync(dir, { recursive: true, force: true });
  };
  process.on('exit', cleanup);

  await waitReady(base);
  const token = ((await (await fetch(`${base}/api/admin/bootstrap`)).json()) as { token: string })
    .token;
  const html = readFileSync(join(root, 'fixtures/hello.html'), 'utf8');
  const form = new FormData();
  form.set('slug', 'bench');
  form.set('file', new Blob([html], { type: 'text/html' }), 'hello.html');
  await fetch(`${base}/api/docs`, {
    method: 'POST',
    body: form,
    headers: { authorization: `Bearer ${token}` },
  });

  console.log(`Benchmarking GET /d/bench/v/1 — ${connections} conns x ${duration}s\n`);
  const result = await autocannon({ url: `${base}/d/bench/v/1`, connections, duration });
  console.log(autocannon.printResult(result));
  console.log(`\nLatency  p50=${result.latency.p50}ms  p99=${result.latency.p99}ms`);
  console.log(`Req/sec  avg=${Math.round(result.requests.average)}`);
  cleanup();
  process.exit(0);
}

void main();
