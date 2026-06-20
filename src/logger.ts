/**
 * Structured logging (pino). Single source of truth for the logger so every
 * module shares one configured instance instead of constructing its own.
 */
import { pino, type Logger } from 'pino';

let instance: Logger | null = null;

/** Initialize the shared logger at boot. Idempotent. */
export function initLogger(level: string): Logger {
  instance ??= pino({ level });
  return instance;
}

/** The shared logger (defaults to info level until {@link initLogger} runs). */
export function logger(): Logger {
  instance ??= pino({ level: process.env.LOG_LEVEL ?? 'info' });
  return instance;
}
