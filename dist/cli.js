#!/usr/bin/env node
import {
  loadConfig
} from "./chunk-4DEK7H4H.js";

// src/cli.ts
var cmd = process.argv[2] ?? "start";
async function main() {
  switch (cmd) {
    case "start":
      await import("./index.js");
      break;
    case "migrate":
      await import("./migrate-PNFCYTAZ.js");
      break;
    case "bootstrap": {
      const config = loadConfig(process.env);
      const base = (config.baseUrl || `http://127.0.0.1:${config.port}`).replace(/\/$/, "");
      const res = await fetch(`${base}/api/admin/bootstrap`);
      const body = await res.json();
      if (body.token) {
        process.stdout.write(body.token + "\n");
      } else {
        process.stderr.write(JSON.stringify(body) + "\n");
        process.exit(1);
      }
      break;
    }
    default:
      process.stderr.write(
        `octo-doc: unknown command "${cmd}"
usage: octo-doc [start|migrate|bootstrap]
`
      );
      process.exit(1);
  }
}
void main();
//# sourceMappingURL=cli.js.map