/**
 * Structured logging (pino). Single source of truth so every module shares one
 * configured instance. {@link initLogger} (re)applies the level; {@link logger}
 * returns the shared instance, lazily created at info level if never initialized.
 */
import { pino, type Logger } from 'pino';

let instance: Logger | null = null;

/** Initialize (or re-level) the shared logger. Safe to call more than once. */
export function initLogger(level: string): Logger {
  if (instance) instance.level = level;
  else instance = pino({ level });
  return instance;
}

/** The shared logger (defaults to the env/`info` level until initialized). */
export function logger(): Logger {
  instance ??= pino({ level: process.env.LOG_LEVEL ?? 'info' });
  return instance;
}
