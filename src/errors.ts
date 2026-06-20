/**
 * Typed application errors.
 *
 * Every failure that crosses a layer boundary is one of these — no bare throws,
 * no swallowed exceptions. Each carries an HTTP status and a stable `code` so
 * the error middleware can map it to a friendly JSON response.
 */

/** Base class for all expected, mappable application errors. */
export class AppError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    options?: { cause?: unknown },
  ) {
    super(message, options);
    this.name = new.target.name;
  }
}

/** 400 — the request was malformed or missing required fields. */
export class ValidationError extends AppError {
  constructor(message: string, code = 'invalid_request') {
    super(400, code, message);
  }
}

/** 401 — missing or invalid credentials. */
export class UnauthorizedError extends AppError {
  constructor(message = 'unauthorized', code = 'unauthorized') {
    super(401, code, message);
  }
}

/** 403 — authenticated but not permitted. */
export class ForbiddenError extends AppError {
  constructor(message = 'forbidden', code = 'forbidden') {
    super(403, code, message);
  }
}

/** 404 — the resource does not exist. */
export class NotFoundError extends AppError {
  constructor(message = 'not found', code = 'not_found') {
    super(404, code, message);
  }
}

/** 409 — the request conflicts with current state. */
export class ConflictError extends AppError {
  constructor(message: string, code = 'conflict') {
    super(409, code, message);
  }
}

/** 413 — the payload exceeds a configured limit. */
export class PayloadTooLargeError extends AppError {
  constructor(message: string, code = 'payload_too_large') {
    super(413, code, message);
  }
}

/** 429 — rate limit exceeded; carries the retry hint in seconds. */
export class RateLimitedError extends AppError {
  constructor(readonly retryAfterSeconds: number) {
    super(429, 'rate_limited', 'rate limit exceeded');
  }
}

/** 502/500 — a downstream dependency (storage, GitHub) failed. */
export class UpstreamError extends AppError {
  constructor(message: string, code = 'upstream_error', cause?: unknown) {
    super(502, code, message, { cause });
  }
}
