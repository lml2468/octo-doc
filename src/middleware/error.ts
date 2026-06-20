/**
 * Error-handling middleware. Maps a thrown {@link AppError} to its HTTP status +
 * a friendly JSON body; anything else becomes a 500 with a generic message
 * (details logged, never leaked). No route swallows exceptions — they throw
 * typed errors and this is the single place they become responses.
 */
import type { ErrorHandler } from 'hono';
import { AppError, RateLimitedError } from '../errors.js';
import { logger } from '../logger.js';
import type { AppEnv } from '../http-context.js';

/** The app's central error handler (registered via `app.onError`). */
export const errorHandler: ErrorHandler<AppEnv> = (err, c) => {
  if (err instanceof RateLimitedError) {
    c.header('Retry-After', String(err.retryAfterSeconds));
    return c.json(
      { error: err.code, message: err.message, retry_after: err.retryAfterSeconds },
      429,
    );
  }
  if (err instanceof AppError) {
    // 4xx are client errors (info); 5xx are ours (error, with cause).
    if (err.status >= 500) logger().error({ err, code: err.code, cause: err.cause }, err.message);
    else logger().info({ code: err.code }, err.message);
    return c.json({ error: err.code, message: err.message }, err.status as 400);
  }
  logger().error({ err }, 'unhandled error');
  return c.json({ error: 'internal_error', message: 'an unexpected error occurred' }, 500);
};
