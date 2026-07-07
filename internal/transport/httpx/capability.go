package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Mininglamp-OSS/octo-doc/internal/platform/apperr"
	"github.com/Mininglamp-OSS/octo-doc/internal/service"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage"
)

// Access control: every document is private by default. A credential grants a
// capability for a specific doc:
//   - author = a valid write token (Bearer). Full access.
//   - reader = a valid per-doc share code (Bearer, cookie, or ?code=). Read
//     published versions + comment/react. Never drafts/publish/promote/delete.
//   - none   → 404 (never confirm the doc exists).
//
// Browsers carry the code as ?code= on the first hit, which is exchanged for an
// HttpOnly cookie and redirected to a clean URL so the secret never lingers in
// history/logs/Referer. Agents/CLI carry it as Authorization: Bearer, so the same
// credential model works headless with no cookie.

// slugFromPath / slugFromQuery extract the slug for the read-JSON gate.
func slugFromPath(r *http.Request) string  { return chi.URLParam(r, "slug") }
func slugFromQuery(r *http.Request) string { return r.URL.Query().Get("slug") }

// capCookieName is the per-doc capability cookie. Scoping the name to the slug
// means one share link never leaks access to another doc. (The cookie's Path is
// "/" so it reaches /v1 routes too — see setCapCookie; only the name is scoped.)
func capCookieName(slug string) string { return "octo_cap_" + storage.HashSlug(slug) }

// credCandidates returns every credential a request presents for a doc, in no
// particular order: an Authorization Bearer (author write token or code-as-bearer,
// used by the CLI), the per-doc capability cookie, and the ?code= query param (a
// browser's first hit). A request can carry more than one — e.g. a browser holding
// a stale cookie that is then handed a freshly rotated ?code= link — so callers
// must resolve them all and take the strongest, never letting a weak/stale cookie
// mask a valid ?code= or Bearer.
func (s *Server) credCandidates(r *http.Request, slug string) []string {
	var creds []string
	if t := bearerToken(r); t != "" {
		creds = append(creds, t)
	}
	if c, err := r.Cookie(capCookieName(slug)); err == nil && c.Value != "" {
		creds = append(creds, c.Value)
	}
	if q := r.URL.Query().Get("code"); q != "" {
		creds = append(creds, q)
	}
	return creds
}

// resolveCap returns the highest capability any of the request's credentials
// grants for the slug. Resolving all candidates (rather than the first non-empty
// one) means a fresh valid ?code= or Bearer always wins over a stale cookie — so
// rotating a code cuts off the old link while a recipient's new link still works,
// and an author's ?code=<write-token> is honored even if the browser holds a
// weaker reader cookie for the same doc.
func (s *Server) resolveCap(r *http.Request, slug string) (service.Capability, error) {
	_, cap, err := s.bestCred(r, slug)
	return cap, err
}

// bestCred returns the credential granting the strongest capability for the slug,
// plus that capability. Used by the ?code=→cookie exchange so the cookie it sets
// carries the credential the viewer was actually authorized by (not a stale one).
func (s *Server) bestCred(r *http.Request, slug string) (string, service.Capability, error) {
	bestCred, best := "", service.CapNone
	for _, cred := range s.credCandidates(r, slug) {
		cap, err := s.auth.CapabilityFor(r.Context(), slug, cred)
		if err != nil {
			return "", service.CapNone, err
		}
		if cap > best {
			best, bestCred = cap, cred
		}
	}
	return bestCred, best, nil
}

// capCtxKey stashes the resolved capability for handlers that branch on it.
// requireDocReadHTML gates an HTML /d/ route: it resolves the capability for the
// path {slug}, 404s on none, and — when the credential arrived as ?code= and is
// valid — sets the HttpOnly capability cookie and 302-redirects to the same URL
// without the query param (so the code leaves the address bar). Otherwise it
// continues to the handler.
func (s *Server) requireDocReadHTML(next http.HandlerFunc) http.HandlerFunc {
	return s.docHTMLGate(service.CapReader, next)
}

// requireDocAuthorHTML is the author-only HTML gate (draft view). It uses the same
// ?code= → cookie → 302 exchange, so the write token can arrive as ?code= in a
// browser (opened by `octo new --open`) and then ride as a cookie — the only way
// a browser can present the author credential.
func (s *Server) requireDocAuthorHTML(next http.HandlerFunc) http.HandlerFunc {
	return s.docHTMLGate(service.CapAuthor, next)
}

// docHTMLGate resolves the capability for the path {slug}, requires at least min,
// performs the ?code=→cookie→302 exchange, else 404s (existence hidden).
func (s *Server) docHTMLGate(min service.Capability, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug, err := requireSlug(chi.URLParam(r, "slug"))
		if err != nil {
			writeErr(w, s.logger, err)
			return
		}
		cred, cap, err := s.bestCred(r, slug)
		if err != nil {
			writeErr(w, s.logger, err)
			return
		}
		if cap < min {
			// Hide existence — same 404 the old PRIVATE gate returned.
			writeErr(w, s.logger, apperr.NotFound("Not found"))
			return
		}
		// Exchange a ?code= credential for a cookie and drop it from the URL, so
		// the secret (reader code OR write token) leaves the address bar. Only do
		// this when the winning credential came in via ?code= (a fresh link),
		// storing that credential so a later stale cookie can't shadow it.
		if q := r.URL.Query().Get("code"); q != "" && q == cred && bearerToken(r) == "" {
			setCapCookie(w, slug, cred, s.cfg.CookieSecure)
			clean := *r.URL
			q := clean.Query()
			q.Del("code")
			clean.RawQuery = q.Encode()
			http.Redirect(w, r, clean.RequestURI(), http.StatusFound)
			return
		}
		next(w, r)
	}
}

// requireDocReadJSON gates a JSON read route whose slug is a path or query param
// (versions, list-comments). No cookie/redirect — JSON clients (overlay via
// cookie, CLI via Bearer) present the credential directly.
func (s *Server) requireDocReadJSON(slugFrom func(*http.Request) string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug, err := requireSlug(slugFrom(r))
		if err != nil {
			writeErr(w, s.logger, err)
			return
		}
		cap, err := s.resolveCap(r, slug)
		if err != nil {
			writeErr(w, s.logger, err)
			return
		}
		if cap == service.CapNone {
			writeErr(w, s.logger, apperr.NotFound("Not found"))
			return
		}
		next(w, r)
	}
}

// requireDocCap resolves the capability for a body-slug mutation route (the slug
// is only known after the handler parses the body). Handlers call this once they
// have the slug; it returns a 404-worthy error on none. Returns nil when the
// caller has at least reader access.
func (s *Server) requireDocCap(r *http.Request, slug string) error {
	cap, err := s.resolveCap(r, slug)
	if err != nil {
		return err
	}
	if cap == service.CapNone {
		return apperr.NotFound("Not found")
	}
	return nil
}

// requireDocAuthor is chi middleware for author-only mutations whose slug is in
// the path (share, draft save/promote, delete). Unlike requireWriteAuth it accepts
// the author credential via Bearer OR the per-doc cookie, so the overlay's
// Publish/Share buttons work in a browser (cookie) as well as the CLI (Bearer).
func (s *Server) requireDocAuthor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug, err := requireSlug(chi.URLParam(r, "slug"))
		if err != nil {
			writeErr(w, s.logger, err)
			return
		}
		cap, err := s.resolveCap(r, slug)
		if err != nil {
			writeErr(w, s.logger, err)
			return
		}
		if cap != service.CapAuthor {
			// A reader (or nobody) must not learn that author-only ops exist here.
			writeErr(w, s.logger, apperr.NotFound("Not found"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
