package httpx

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Mininglamp-OSS/octo-doc/internal/core"
	"github.com/Mininglamp-OSS/octo-doc/internal/service"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage"
)

func (s *Server) handleLanding(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(landingHTML(s.cfg.RepoURL)))
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) error {
	session, err := s.auth.GetSession(r.Context(), sessionCookie(r))
	if err != nil {
		return err
	}
	if !s.auth.IsOwner(session) {
		http.Redirect(w, r, s.cfg.RepoURL, http.StatusFound)
		return nil
	}
	docs, err := s.docs.ListAllForOwner(r.Context())
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(catalogHTML(session, docs)))
	return nil
}

func landingHTML(repo string) string {
	repoLabel := strings.TrimPrefix(strings.TrimPrefix(repo, "https://"), "http://")
	return `<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>octo-doc</title>
<style>
  body { font: 15px system-ui, -apple-system, sans-serif; min-height: 100vh; margin: 0;
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    color: #111; background: #fff; gap: 10px; }
  h1 { font-size: 30px; margin: 0; color: #1652f0; }
  p { color: #666; margin: 0; }
  a { color: #1652f0; text-decoration: none; }
  a:hover { text-decoration: underline; }
  .sub { margin-top: 14px; font-size: 13px; color: #888; }
</style></head><body>
  <h1>octo-doc</h1>
  <p>Prompt-native, commentable documents. Self-hosted.</p>
  <p class="sub">Open a document from its shared link &middot;
    <a href="` + core.EscapeHTML(repo) + `">` + core.EscapeHTML(repoLabel) + `</a></p>
</body></html>`
}

func catalogHTML(session *storage.Session, docs []service.OwnerDoc) string {
	var rows strings.Builder
	for _, d := range docs {
		rows.WriteString(`<tr><td><a href="/d/` + core.EscapeHTML(d.Slug) + `/v/` + strconv.Itoa(d.Latest) + `">` +
			core.EscapeHTML(d.Title) + `</a></td><td>` + core.EscapeHTML(d.Slug) + `</td><td>v` + strconv.Itoa(d.Latest) + `</td></tr>`)
	}
	who := ""
	if session != nil && session.Login != "" {
		who = ` &middot; signed in as <b>` + core.EscapeHTML(session.Login) + `</b>`
	}
	body := `<p class="empty">No published docs yet.</p>`
	if len(docs) > 0 {
		body = `<table><thead><tr><th>Title</th><th>Slug</th><th>Version</th></tr></thead><tbody>` + rows.String() + `</tbody></table>`
	}
	return `<!doctype html><html><head><meta charset="utf-8"><title>octo-doc</title>
<style>
  body { font: 15px system-ui, -apple-system, sans-serif; max-width: 760px; margin: 60px auto; padding: 0 20px; color: #111; }
  h1 { font-size: 28px; margin: 0 0 4px; color: #1652f0; }
  table { width: 100%; border-collapse: collapse; }
  th, td { text-align: left; padding: 10px 12px; border-bottom: 1px solid #eee; }
  th { font-size: 12px; text-transform: uppercase; color: #888; letter-spacing: 0.04em; }
  a { color: #1652f0; text-decoration: none; }
  a:hover { text-decoration: underline; }
  .empty { color: #888; padding: 40px 0; text-align: center; }
  .who { color: #888; font-size: 13px; margin: 0 0 32px; }
  .who b { color: #444; font-weight: 600; }
</style></head><body>
<h1>My docs</h1>
<p class="who">Documents hosted on this server` + who + `.</p>
` + body + `
</body></html>`
}
