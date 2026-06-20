// Throughput/latency benchmark for the doc-render hot path using autocannon.
// Spawns a server, publishes one doc, then hammers GET /d/<slug>/v/1 — the
// path the non-functional criteria target (p50 ≤ 50ms, p99 ≤ 200ms on 1 vCPU).
//
// Usage: node bench/run.js [durationSeconds] [connections]
import { spawn } from 'node:child_process';
import { mkdtempSync, rmSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import net from 'node:net';
import autocannon from 'autocannon';

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = join(__dirname, '..');
const DURATION = Number(process.argv[2] || 10);
const CONNECTIONS = Number(process.argv[3] || 50);

function freePort() {
  return new Promise((res, rej) => {
    const s = net.createServer();
    s.listen(0, '127.0.0.1', () => { const p = s.address().port; s.close(() => res(p)); });
    s.on('error', rej);
  });
}
async function waitReady(base, ms = 8000) {
  const dl = Date.now() + ms;
  for (;;) {
    try { if ((await fetch(`${base}/healthz`)).ok) return; } catch { /* retry */ }
    if (Date.now() > dl) throw new Error('not ready');
    await new Promise(r => setTimeout(r, 100));
  }
}

const dir = mkdtempSync(join(tmpdir(), 'octo-bench-'));
const port = await freePort();
const base = `http://127.0.0.1:${port}`;
const srv = spawn(process.execPath, [join(ROOT, 'src/index.js')], {
  env: { ...process.env, DATA_DIR: dir, STORAGE: 'sqlite+fs', PORT: String(port), LOG_LEVEL: 'silent', COOKIE_SECURE: 'false' },
  stdio: 'ignore',
});
const cleanup = () => { try { srv.kill('SIGKILL'); } catch {} try { rmSync(dir, { recursive: true, force: true }); } catch {} };
process.on('exit', cleanup);

await waitReady(base);
const token = (await (await fetch(`${base}/api/admin/bootstrap`)).json()).token;
const html = readFileSync(join(ROOT, 'fixtures/hello.html'), 'utf8');
const form = new FormData();
form.set('slug', 'bench');
form.set('file', new Blob([html], { type: 'text/html' }), 'hello.html');
await fetch(`${base}/api/docs`, { method: 'POST', body: form, headers: { authorization: `Bearer ${token}` } });

console.log(`Benchmarking GET /d/bench/v/1 — ${CONNECTIONS} conns × ${DURATION}s\n`);
const result = await autocannon({ url: `${base}/d/bench/v/1`, connections: CONNECTIONS, duration: DURATION });
console.log(autocannon.printResult(result));
console.log(`\nLatency  p50=${result.latency.p50}ms  p99=${result.latency.p99}ms`);
console.log(`Req/sec  avg=${Math.round(result.requests.average)}`);
const memMb = Math.round((process.memoryUsage().rss) / 1048576);
console.log(`(bench client RSS ${memMb}MB — server measured separately)`);
cleanup();
process.exit(0);
