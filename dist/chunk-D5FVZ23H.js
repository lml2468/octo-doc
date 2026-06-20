// src/logger.ts
import { pino } from "pino";
var instance = null;
function initLogger(level) {
  instance ??= pino({ level });
  return instance;
}
function logger() {
  instance ??= pino({ level: process.env.LOG_LEVEL ?? "info" });
  return instance;
}

export {
  initLogger,
  logger
};
//# sourceMappingURL=chunk-D5FVZ23H.js.map