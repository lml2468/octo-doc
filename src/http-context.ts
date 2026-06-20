/**
 * Hono environment typing. Services and config are attached to the context once
 * at app construction, so route handlers read them off `c.var` in a typed way
 * instead of importing singletons.
 */
import type { Config } from './config.js';
import type { AuthService, CommentService, DocService } from './services/index.js';

/** Variables available on every request context. */
export interface AppVariables {
  config: Config;
  docs: DocService;
  comments: CommentService;
  auth: AuthService;
  /** The validated write token, set by {@link requireWriteAuth}. */
  writeToken?: string;
}

/** The Hono generic env for this app. */
export interface AppEnv {
  Variables: AppVariables;
}
