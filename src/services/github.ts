/**
 * GitHub Device Flow client. Thin, typed wrappers over the two device-flow
 * endpoints and `/user`. Network failures surface as {@link UpstreamError}.
 */
import { UpstreamError, ValidationError } from '../errors.js';

const GH_USER_AGENT = 'octo-doc';

/** Device-flow start response surfaced to the client. */
export interface DeviceStart {
  device_code: string;
  user_code: string;
  verification_uri: string;
  expires_in: number;
  interval: number;
}

/** Result of polling for an access token. */
export type PollResult = { pending: true } | { pending: false; accessToken: string };

/** A GitHub user profile (only the fields we persist). */
export interface GhUser {
  login?: string;
  name?: string;
  avatar_url?: string;
}

/** POST a form-encoded body to github.com and parse JSON or form-encoded replies. */
async function ghPostForm(
  path: string,
  form: Record<string, string>,
): Promise<Record<string, string>> {
  let res: Response;
  try {
    res = await fetch(`https://github.com${path}`, {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/x-www-form-urlencoded',
        'User-Agent': GH_USER_AGENT,
      },
      body: new URLSearchParams(form).toString(),
    });
  } catch (err) {
    throw new UpstreamError('github unreachable', 'github_unreachable', err);
  }
  const ct = res.headers.get('content-type') ?? '';
  const raw = await res.text();
  if (ct.includes('application/json')) {
    try {
      return JSON.parse(raw) as Record<string, string>;
    } catch {
      throw new UpstreamError('github returned unparseable JSON', 'gh_parse');
    }
  }
  return Object.fromEntries(new URLSearchParams(raw));
}

/** Start the device flow, returning the user code + verification URI. */
export async function ghStartDeviceFlow(clientId: string): Promise<DeviceStart> {
  const r = await ghPostForm('/login/device/code', { client_id: clientId, scope: 'read:user' });
  if (r.error) throw new ValidationError(r.error_description ?? r.error, r.error);
  return {
    device_code: r.device_code!,
    user_code: r.user_code!,
    verification_uri: r.verification_uri!,
    expires_in: Number(r.expires_in),
    interval: Number(r.interval),
  };
}

/** Poll for an access token; `pending` while the user hasn't authorized yet. */
export async function ghPollAccessToken(clientId: string, deviceCode: string): Promise<PollResult> {
  const r = await ghPostForm('/login/oauth/access_token', {
    client_id: clientId,
    device_code: deviceCode,
    grant_type: 'urn:ietf:params:oauth:grant-type:device_code',
  });
  if (r.error === 'authorization_pending' || r.error === 'slow_down') return { pending: true };
  if (r.error) throw new ValidationError(r.error_description ?? r.error, r.error);
  if (!r.access_token) return { pending: true };
  return { pending: false, accessToken: r.access_token };
}

/** Fetch the authenticated user's profile. */
export async function ghFetchUser(token: string): Promise<GhUser> {
  let res: Response;
  try {
    res = await fetch('https://api.github.com/user', {
      headers: {
        Accept: 'application/vnd.github+json',
        Authorization: `Bearer ${token}`,
        'User-Agent': GH_USER_AGENT,
      },
    });
  } catch (err) {
    throw new UpstreamError('github unreachable', 'github_unreachable', err);
  }
  return (await res.json()) as GhUser;
}
