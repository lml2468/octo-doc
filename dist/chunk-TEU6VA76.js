// src/logger.ts
import { pino } from "pino";
var instance = null;
function initLogger(level) {
  if (instance) instance.level = level;
  else instance = pino({ level });
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
//# sourceMappingURL=chunk-TEU6VA76.js.map