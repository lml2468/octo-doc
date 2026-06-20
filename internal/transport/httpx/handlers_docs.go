package httpx

import (
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

func (s *Server) readPublishBody(r *http.Request) (publishBody, error) {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(ct, "multipart/form-data") {
		return s.readMultipart(r)
	}
	return s.readJSONPublish(r)
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
		defer file.Close()
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

func (s *Server) readJSONPublish(r *http.Request) (publishBody, error) {
	var raw struct {
		Slug     string         `json:"slug"`
		HTML     string         `json:"html"`
		Version  int            `json:"version"`
		Title    string         `json:"title"`
		Comments []core.Comment `json:"comments"`
	}
	_ = decodeJSON(r, &raw)
	return publishBody{
		Slug: raw.Slug, HTML: raw.HTML, Version: raw.Version, Title: raw.Title, LocalComments: raw.Comments,
	}, nil
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) error {
	body, err := s.readPublishBody(r)
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
	writeJSON(w, 200, mergeOK(res))
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
	writeJSON(w, 200, res)
	return nil
}

func (s *Server) handleDeleteDoc(w http.ResponseWriter, r *http.Request) error {
	slug, err := requireSlug(r.URL.Query().Get("slug"))
	if err != nil {
		return err
	}
	if err := s.docs.Remove(r.Context(), slug); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
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

	session, err := s.auth.GetSession(r.Context(), cookie(r, "tdoc_sid"))
	if err != nil {
		return err
	}
	mode := "local"
	if s.cfg.GitHubClientID != "" {
		mode = "published"
	}
	versions := toVersionRefs(data.Versions, version)
	html, err := core.InjectOverlayCfg(data.HTML, s.overlayJS, core.OverlayConfig{
		Slug:           slug,
		Version:        version,
		Identity:       identityFromSession(session),
		IsOwner:        s.auth.IsOwner(session),
		AuthConfigured: s.cfg.GitHubClientID != "",
		Mode:           mode,
		Versions:       versions,
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
