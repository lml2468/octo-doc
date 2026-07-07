package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Mininglamp-OSS/octo-doc/internal/core"
	"github.com/Mininglamp-OSS/octo-doc/internal/platform/apperr"
	"github.com/Mininglamp-OSS/octo-doc/internal/service"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage"
)

// publishBody is the parsed publish input from JSON or multipart.
type publishBody struct {
	Slug          string
	HTML          string
	Version       int
	Title         string
	LocalComments []core.Comment
}

func (s *Server) readPublishBody(w http.ResponseWriter, r *http.Request) (publishBody, error) {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(ct, "multipart/form-data") {
		return s.readMultipart(r)
	}
	return s.readJSONPublish(w, r)
}

func (s *Server) readMultipart(r *http.Request) (publishBody, error) {
	if err := r.ParseMultipartForm(s.cfg.MaxHTMLBytes + 1<<20); err != nil {
		return publishBody{}, apperr.Validation("invalid multipart body", "invalid_multipart")
	}
	var b publishBody
	b.Slug = r.FormValue("slug")
	b.Title = r.FormValue("title")
	if v := r.FormValue("version"); v != "" {
		b.Version, _ = strconv.Atoi(v)
	}
	if file, _, err := r.FormFile("file"); err == nil {
		defer func() { _ = file.Close() }()
		data, rerr := io.ReadAll(file)
		if rerr != nil {
			return publishBody{}, apperr.Validation("could not read file", "file_read_failed")
		}
		b.HTML = string(data)
	} else if h := r.FormValue("html"); h != "" {
		b.HTML = h
	}
	return b, nil
}

func (s *Server) readJSONPublish(w http.ResponseWriter, r *http.Request) (publishBody, error) {
	var raw struct {
		Slug    string `json:"slug"`
		HTML    string `json:"html"`
		Version int    `json:"version"`
		Title   string `json:"title"`
		Meta    *struct {
			Title string `json:"title"`
		} `json:"meta"`
		Comments []core.Comment `json:"comments"`
	}
	if r.Body != nil {
		// Publish bodies carry the document HTML, so cap at the HTML limit plus JSON
		// framing headroom rather than the small default JSON cap. The service layer
		// still enforces MAX_HTML_BYTES on the decoded HTML field itself.
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxHTMLBytes+1<<20)
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return publishBody{}, apperr.PayloadTooLarge("request body too large", "body_too_large")
			}
			// Other decode errors fall through: a missing/invalid body surfaces as the
			// service-layer "html required" 400, preserving prior tolerance.
		}
	}
	// The CLI sends the doc's meta.json under `meta` (the documented contract:
	// {slug, version, html, meta, comments}). Honor meta.title, but let an
	// explicit top-level `title` win if both are present.
	title := raw.Title
	if title == "" && raw.Meta != nil {
		title = raw.Meta.Title
	}
	return publishBody{
		Slug: raw.Slug, HTML: raw.HTML, Version: raw.Version, Title: title, LocalComments: raw.Comments,
	}, nil
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) error {
	body, err := s.readPublishBody(w, r)
	if err != nil {
		return err
	}
	slug, err := requireSlug(body.Slug)
	if err != nil {
		return err
	}
	if body.HTML == "" {
		return apperr.Validation("html (file) required", "html_required")
	}
	res, err := s.docs.Publish(r.Context(), service.PublishInput{
		Slug: slug, HTML: body.HTML, Version: body.Version, Title: body.Title, LocalComments: body.LocalComments,
	})
	if err != nil {
		return err
	}
	writeData(w, 200, res)
	return nil
}

// handleSaveDraft writes the mutable draft slot (PUT /v1/docs/{slug}/draft).
// Write-auth gated. The body is the same shape as publish, minus version.
func (s *Server) handleSaveDraft(w http.ResponseWriter, r *http.Request) error {
	slug, err := requireSlug(chi.URLParam(r, "slug"))
	if err != nil {
		return err
	}
	body, err := s.readPublishBody(w, r)
	if err != nil {
		return err
	}
	if body.HTML == "" {
		return apperr.Validation("html (file) required", "html_required")
	}
	res, err := s.docs.SaveDraft(r.Context(), slug, body.HTML, body.Title)
	if err != nil {
		return err
	}
	writeData(w, 200, res)
	return nil
}

// handlePromote promotes the draft to a new immutable version
// (POST /v1/docs/{slug}/draft/promote). Write-auth gated.
func (s *Server) handlePromote(w http.ResponseWriter, r *http.Request) error {
	slug, err := requireSlug(chi.URLParam(r, "slug"))
	if err != nil {
		return err
	}
	// Optional {title} override.
	var raw struct {
		Title string `json:"title"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&raw)
	}
	res, err := s.docs.Promote(r.Context(), slug, raw.Title)
	if err != nil {
		return err
	}
	writeData(w, 200, res)
	return nil
}

// handleRenderDraft renders the draft slot (GET/HEAD /d/{slug}/draft) with the
// overlay in "draft" mode. Write-auth gated — a draft is author-only until promoted.
func (s *Server) handleRenderDraft(w http.ResponseWriter, r *http.Request) error {
	slug, err := requireSlug(chi.URLParam(r, "slug"))
	if err != nil {
		return err
	}
	data, err := s.docs.GetDraft(r.Context(), slug)
	if err != nil {
		return err
	}
	if data == nil {
		return apperr.NotFound("Not found: " + slug + " draft")
	}
	session, err := s.auth.GetSession(r.Context(), sessionCookie(r))
	if err != nil {
		return err
	}
	// Draft mode: the overlay shows a Publish affordance (promote) instead of
	// Share/Fork. Version 0 signals "not yet a committed version".
	html, err := core.InjectOverlayCfg(data.HTML, s.overlayJS, core.OverlayConfig{
		Slug:           slug,
		Version:        0,
		Identity:       identityFromSession(session),
		IsOwner:        s.auth.IsOwner(session),
		AuthConfigured: s.auth.LoginEnabled(),
		Mode:           "draft",
		Versions:       toVersionRefs(data.Versions, 0),
	})
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		w.WriteHeader(200)
		return nil
	}
	_, _ = io.WriteString(w, html)
	return nil
}

// handleShare mints (or rotates) the per-doc share code and returns a coded read
// URL. Author-only. POST /v1/docs/{slug}/share.
func (s *Server) handleShare(w http.ResponseWriter, r *http.Request) error {
	slug, err := requireSlug(chi.URLParam(r, "slug"))
	if err != nil {
		return err
	}
	code, err := s.auth.GenerateCode(r.Context(), slug)
	if err != nil {
		return err
	}
	// Point at the latest version if one exists, else the doc root.
	url := s.cfg.BaseURL + "/d/" + slug + "/v/1?code=" + code
	if vl, verr := s.docs.ListVersions(r.Context(), slug); verr == nil && vl != nil && len(vl.Versions) > 0 {
		latest := vl.Versions[len(vl.Versions)-1].N
		url = s.cfg.BaseURL + "/d/" + slug + "/v/" + strconv.Itoa(latest) + "?code=" + code
	}
	writeData(w, 200, map[string]any{"slug": slug, "code": code, "url": url})
	return nil
}

// handleRevokeShare clears the per-doc share code (existing links stop working).
// Author-only. DELETE /v1/docs/{slug}/share.
func (s *Server) handleRevokeShare(w http.ResponseWriter, r *http.Request) error {
	slug, err := requireSlug(chi.URLParam(r, "slug"))
	if err != nil {
		return err
	}
	if err := s.auth.RevokeCode(r.Context(), slug); err != nil {
		return err
	}
	writeData(w, 200, map[string]any{"slug": slug, "revoked": true})
	return nil
}

func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request) error {
	slug, err := requireSlug(chi.URLParam(r, "slug"))
	if err != nil {
		return err
	}
	res, err := s.docs.ListVersions(r.Context(), slug)
	if err != nil {
		return err
	}
	if res == nil {
		return apperr.NotFound("")
	}
	writeData(w, 200, toVersionListDTO(res))
	return nil
}

func (s *Server) handleDeleteDoc(w http.ResponseWriter, r *http.Request) error {
	slug, err := requireSlug(chi.URLParam(r, "slug"))
	if err != nil {
		return err
	}
	if err := s.docs.Remove(r.Context(), slug); err != nil {
		return err
	}
	writeData(w, 200, struct{}{})
	return nil
}

func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) error {
	slug, err := requireSlug(chi.URLParam(r, "slug"))
	if err != nil {
		return err
	}
	version, ok := parseVersionParam(chi.URLParam(r, "version"))
	if !ok {
		return apperr.NotFound("")
	}
	data, err := s.docs.Render(r.Context(), slug, version)
	if err != nil {
		return err
	}
	if data == nil {
		return apperr.NotFound("Not found: " + slug + " v" + chi.URLParam(r, "version"))
	}

	session, err := s.auth.GetSession(r.Context(), sessionCookie(r))
	if err != nil {
		return err
	}
	// Is this viewer the author (write token via Bearer or cookie) or a reader
	// (share code)? Both reach here through requireDocReadHTML, but only the author
	// may mint/rotate a share code — so the overlay must hide the Share CTA from a
	// reader (clicking it would 404). We carry the flag OUTSIDE core.OverlayConfig
	// (which is byte-frozen) as a separate window.__ODOC_CAP__ marker.
	cap, err := s.resolveCap(r, slug)
	if err != nil {
		return err
	}
	// A doc rendered by this server is, by definition, published — so the overlay
	// always runs in "published" mode (Share/Fork, never a Publish button; that
	// belongs to the local preview server). AuthConfigured reflects whether a
	// login provider exists (none yet → anonymous commenting); a future Octo
	// unified login flips it on via AuthService.LoginEnabled.
	versions := toVersionRefs(data.Versions, version)
	html, err := core.InjectOverlayCfg(data.HTML, s.overlayJS, core.OverlayConfig{
		Slug:           slug,
		Version:        version,
		Identity:       identityFromSession(session),
		IsOwner:        s.auth.IsOwner(session),
		AuthConfigured: s.auth.LoginEnabled(),
		Mode:           "published",
		Versions:       versions,
	})
	if err != nil {
		return err
	}
	html = injectCapMarker(html, cap == service.CapAuthor)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		w.WriteHeader(200)
		return nil
	}
	_, _ = io.WriteString(w, html)
	return nil
}

// injectCapMarker adds window.__ODOC_CAP__ before the overlay script so the
// overlay can gate author-only UI (the Share/mint-code button) without touching
// the byte-frozen core.OverlayConfig. It is injected right before the overlay's
// own <script> so it is defined when the overlay boots.
func injectCapMarker(html string, isAuthor bool) string {
	marker := `<script>window.__ODOC_CAP__ = {isAuthor: ` + strconv.FormatBool(isAuthor) + `};</script>`
	// The overlay boot is the last "<script>" InjectOverlayCfg wrote; place the
	// marker before the window.__ODOC__ config script so both precede the overlay.
	const anchor = "<script>window.__ODOC__ = "
	if i := strings.Index(html, anchor); i >= 0 {
		return html[:i] + marker + "\n" + html[i:]
	}
	return html + marker
}

func (s *Server) handleForkExport(w http.ResponseWriter, r *http.Request) error {
	kind := chi.URLParam(r, "kind")
	if kind != "export" && kind != "fork" {
		return apperr.NotFound("")
	}
	slug, err := requireSlug(chi.URLParam(r, "slug"))
	if err != nil {
		return err
	}
	version, ok := parseVersionParam(chi.URLParam(r, "version"))
	if !ok {
		return apperr.NotFound("")
	}
	data, err := s.docs.Render(r.Context(), slug, version)
	if err != nil {
		return err
	}
	if data == nil {
		return apperr.NotFound("Not found: " + slug + " v" + chi.URLParam(r, "version"))
	}
	list, err := s.comments.List(r.Context(), slug, version)
	if err != nil {
		return err
	}
	out, err := buildForkExport(forkExportInput{
		Slug: slug, Version: version, HTML: data.HTML, Comments: list, Kind: kind,
		OverlayJS: s.overlayJS, Now: nowISO(),
	})
	if err != nil {
		return err
	}
	dl := r.URL.Query().Get("download")
	force := dl == "1" || (kind == "export" && dl != "0")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if force {
		w.Header().Set("Content-Disposition", `attachment; filename="`+slug+"-v"+strconv.Itoa(version)+`-fork.html"`)
	}
	_, _ = io.WriteString(w, out)
	return nil
}

// toVersionRefs converts storage version refs to overlay version refs, falling
// back to the single current version when none are stored.
func toVersionRefs(stored []storage.VersionRef, current int) []core.VersionRef {
	if len(stored) == 0 {
		return []core.VersionRef{{N: current}}
	}
	out := make([]core.VersionRef, 0, len(stored))
	for _, v := range stored {
		out = append(out, core.VersionRef{N: v.N, Created: v.Created})
	}
	return out
}
