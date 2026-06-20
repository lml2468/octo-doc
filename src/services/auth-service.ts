/**
 * AuthService — write-token validation, admin bootstrap, viewer sessions, and
 * the GitHub Device Flow. Encapsulates all credential logic so routes stay thin.
 */
import { timingSafeEqual as nodeTimingSafeEqual } from 'node:crypto';
import type { MetadataStore, Session } from '../storage/types.js';
import type { Config } from '../config.js';
import { ConflictError, ForbiddenError, UpstreamError, ValidationError } from '../errors.js';
import { newSessionId, newToken } from './ids.js';
import { ghPollAccessToken, ghStartDeviceFlow, ghFetchUser, type DeviceStart } from './github.js';

/** Session TTL: 30 days. */
const SESSION_TTL_SECONDS = 60 * 60 * 24 * 30;

/** Identity returned to the overlay/API. */
export interface Identity {
  login: string;
  avatar_url?: string | null;
  name?: string;
}

export class AuthService {
  constructor(
    private readonly meta: MetadataStore,
    private readonly config: Config,
  ) {}

  /** Constant-time check that `token` is the static or a provisioned write token. */
  async isValidWriteToken(token: string): Promise<boolean> {
    if (!token) return false;
    if (this.config.writeToken && constantTimeEqual(token, this.config.writeToken)) return true;
    return (await this.meta.getToken(token)) !== null;
  }

  /**
   * Mint the first write token. One-shot: 409s once any token exists or a static
   * token is configured.
   *
   * @throws {ForbiddenError} if bootstrap is disabled
   * @throws {ConflictError} if already bootstrapped
   */
  async bootstrap(): Promise<{ token: string }> {
    if (!this.config.allowBootstrap)
      throw new ForbiddenError('bootstrap disabled', 'bootstrap_disabled');
    if (this.config.writeToken)
      throw new ConflictError('a static WRITE_TOKEN is configured', 'static_token_configured');
    if (await this.meta.anyToken())
      throw new ConflictError('already bootstrapped', 'already_bootstrapped');
    const token = newToken();
    await this.meta.putToken(token, {
      token,
      created: new Date().toISOString(),
      label: 'bootstrap',
    });
    return { token };
  }

  /** Resolve a session from its id, or null. */
  getSession(sid: string | undefined): Promise<Session | null> {
    if (!sid) return Promise.resolve(null);
    return this.meta.getSession(sid);
  }

  /** Whether a session belongs to the configured owner (for the /me catalog). */
  isOwner(session: Session | null): boolean {
    const owner = this.config.owner.toLowerCase();
    return !!owner && !!session?.login && session.login.toLowerCase() === owner;
  }

  /** Start the GitHub Device Flow. @throws {ValidationError} if auth is unconfigured. */
  startDeviceFlow(): Promise<DeviceStart> {
    if (!this.config.githubClientId) {
      return Promise.reject(new ValidationError('auth not configured', 'auth_not_configured'));
    }
    return ghStartDeviceFlow(this.config.githubClientId);
  }

  /**
   * Poll the device flow; on success creates a session and returns the identity
   * plus the new session id. Returns `{ pending: true }` while authorization is
   * outstanding.
   */
  async pollDeviceFlow(
    deviceCode: string,
  ): Promise<{ pending: true } | { pending: false; sid: string; identity: Identity }> {
    this.requireGithub();
    if (!deviceCode) throw new ValidationError('device_code required', 'device_code_required');
    const poll = await ghPollAccessToken(this.config.githubClientId, deviceCode);
    if (poll.pending) return { pending: true };
    const user = await ghFetchUser(poll.accessToken);
    if (!user.login) throw new UpstreamError('GitHub returned no login', 'no_user');
    const sid = newSessionId();
    const session: Session = {
      login: user.login,
      avatar_url: user.avatar_url ?? null,
      name: user.name ?? user.login,
      created: new Date().toISOString(),
    };
    await this.meta.putSession(sid, session, SESSION_TTL_SECONDS);
    const identity: Identity = { login: user.login };
    if (session.avatar_url != null) identity.avatar_url = session.avatar_url;
    if (session.name != null) identity.name = session.name;
    return { pending: false, sid, identity };
  }

  /** Destroy a session. */
  async logout(sid: string | undefined): Promise<void> {
    if (sid) await this.meta.deleteSession(sid);
  }

  /** Session cookie max-age, exposed so routes can set the cookie consistently. */
  get sessionTtlSeconds(): number {
    return SESSION_TTL_SECONDS;
  }

  private requireGithub(): void {
    if (!this.config.githubClientId)
      throw new ValidationError('auth not configured', 'auth_not_configured');
  }
}

/** Constant-time string comparison resistant to timing attacks. */
function constantTimeEqual(a: string, b: string): boolean {
  const ab = Buffer.from(a);
  const bb = Buffer.from(b);
  if (ab.length !== bb.length) return false;
  return nodeTimingSafeEqual(ab, bb);
}
