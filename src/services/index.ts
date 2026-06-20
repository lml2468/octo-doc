/**
 * Service layer barrel. The application's business operations, sitting between
 * the HTTP routes and the storage adapters. Routes import only from here.
 */
export { DocService } from './doc-service.js';
export type { PublishInput, PublishResult, RenderData } from './doc-service.js';
export { CommentService } from './comment-service.js';
export type { VersionScope, MutationResult } from './comment-service.js';
export { AuthService } from './auth-service.js';
export type { Identity } from './auth-service.js';
export { rand, newToken, newSessionId } from './ids.js';
