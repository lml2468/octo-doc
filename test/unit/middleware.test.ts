import { describe, it, expect } from 'vitest';
import { docSecurityHeaders } from '../../src/middleware/security.js';
import { clientIp } from '../../src/middleware/rate-limit.js';
import {
  escapeHtml,
  forHtmlComment,
  safeJsonForScript,
  toOverlayIdentity,
} from '../../src/core/render.js';

describe('docSecurityHeaders', () => {
  it('uses DENY when frame-ancestors is none', () => {
    const h = docSecurityHeaders("'none'");
    expect(h['X-Frame-Options']).toBe('DENY');
    expect(h['Content-Security-Policy']).toMatch(/frame-ancestors 'none'/);
  });

  it('uses SAMEORIGIN when embedding is allowed', () => {
    const h = docSecurityHeaders("'self' https://panel.example.com");
    expect(h['X-Frame-Options']).toBe('SAMEORIGIN');
    expect(h['Content-Security-Policy']).toMatch(/panel\.example\.com/);
  });
});

describe('clientIp', () => {
  it('prefers X-Forwarded-For, then X-Real-IP, then unknown', () => {
    expect(clientIp(new Headers({ 'x-forwarded-for': '1.2.3.4, 5.6.7.8' }))).toBe('1.2.3.4');
    expect(clientIp(new Headers({ 'x-real-ip': '9.9.9.9' }))).toBe('9.9.9.9');
    expect(clientIp(new Headers())).toBe('unknown');
  });
});

describe('render helpers', () => {
  it('escapeHtml escapes all five entities and handles null', () => {
    expect(escapeHtml(`<a href="x" id='y'>&</a>`)).toBe(
      '&lt;a href=&quot;x&quot; id=&#39;y&#39;&gt;&amp;&lt;/a&gt;',
    );
    expect(escapeHtml(null)).toBe('');
    expect(escapeHtml(undefined)).toBe('');
  });

  it('forHtmlComment breaks -- runs and handles null', () => {
    expect(forHtmlComment('a -- b')).toBe('a -\\- b');
    expect(forHtmlComment(null)).toBe('');
  });

  it('safeJsonForScript escapes script and comment openers', () => {
    const out = safeJsonForScript({ x: '</script><!--' });
    expect(out).not.toMatch(/<\/script>/);
    expect(out).not.toMatch(/<!--/);
  });

  it('toOverlayIdentity omits null/undefined optional fields', () => {
    expect(toOverlayIdentity(null)).toBeNull();
    expect(toOverlayIdentity({ login: 'a' })).toStrictEqual({ login: 'a' });
    expect(toOverlayIdentity({ login: 'a', avatar_url: null, name: 'A' })).toStrictEqual({
      login: 'a',
      name: 'A',
    });
    expect(toOverlayIdentity({ login: 'a', avatar_url: 'http://x' })).toStrictEqual({
      login: 'a',
      avatar_url: 'http://x',
    });
  });
});
