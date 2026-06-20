/**
 * Security headers for rendered documents.
 *
 * User HTML is served as an opaque blob (never eval'd). The CSP here protects
 * the surrounding context: `frame-ancestors` (clickjacking / parent-panel XSS)
 * and `base-uri 'self'` (no `<base>` hijack). The doc's own inline JS/CSS need
 * `'unsafe-inline'`/`'unsafe-eval'` — that's the whole point of single-page
 * interactive HTML — so origin isolation (a separate doc subdomain) is the real
 * defense (see DESIGN.md), enforced operationally, not by this header.
 */

/** Build the security headers to attach to every `/d/*` response. */
export function docSecurityHeaders(frameAncestors: string): Record<string, string> {
  const csp = [
    "default-src 'self' data: blob: https:",
    "script-src 'self' 'unsafe-inline' 'unsafe-eval' data: blob: https:",
    "style-src 'self' 'unsafe-inline' https:",
    "img-src 'self' data: blob: https:",
    "font-src 'self' data: https:",
    "connect-src 'self' https:",
    "base-uri 'self'",
    `frame-ancestors ${frameAncestors}`,
  ].join('; ');
  return {
    'Content-Security-Policy': csp,
    'X-Frame-Options': frameAncestors === "'none'" ? 'DENY' : 'SAMEORIGIN',
    'X-Content-Type-Options': 'nosniff',
    'Referrer-Policy': 'no-referrer',
  };
}
