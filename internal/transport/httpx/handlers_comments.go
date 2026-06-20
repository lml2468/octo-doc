package httpx

import (
	"encoding/json"
	"net/http"

	"github.com/Mininglamp-OSS/octo-doc/internal/core"
	"github.com/Mininglamp-OSS/octo-doc/internal/platform/apperr"
	"github.com/Mininglamp-OSS/octo-doc/internal/service"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage"
)

// mutationLike mirrors service.MutationResult so both create/reply branches can
// be assigned to one variable.
type mutationLike service.MutationResult

// viewer resolves the viewer session, requiring one when GitHub auth is configured.
func (s *Server) viewer(r *http.Request) (*storage.Session, error) {
	session, err := s.auth.GetSession(r.Context(), cookie(r, "tdoc_sid"))
	if err != nil {
		return nil, err
	}
	if s.cfg.GitHubClientID != "" && session == nil {
		return nil, apperr.Unauthorized("sign_in_required", "sign_in_required")
	}
	return session, nil
}

// parseVersionQuery parses the version query param: a number, "all", or latest.
func parseVersionQuery(raw string) int {
	if raw == "" {
		return core.VersionLatest
	}
	if raw == "all" {
		return core.VersionLatest
	}
	if n, ok := parseVersionParam(raw); ok {
		return n
	}
	return core.VersionLatest
}

func (s *Server) handleListComments(w http.ResponseWriter, r *http.Request) error {
	slug, err := requireSlug(r.URL.Query().Get("slug"))
	if err != nil {
		return err
	}
	list, err := s.comments.List(r.Context(), slug, parseVersionQuery(r.URL.Query().Get("version")))
	if err != nil {
		return err
	}
	writeJSON(w, 200, list)
	return nil
}

func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) error {
	session, err := s.viewer(r)
	if err != nil {
		return err
	}
	var body struct {
		Slug     string       `json:"slug"`
		Text     string       `json:"text"`
		Version  json.Number  `json:"version"`
		ParentID *string      `json:"parent_id"`
		Anchor   *core.Anchor `json:"anchor"`
	}
	_ = decodeJSON(r, &body)
	slug, err := requireSlug(body.Slug)
	if err != nil {
		return err
	}
	if body.Text == "" {
		return apperr.Validation("slug and text required", "text_required")
	}
	version := numOr1(body.Version)
	var res mutationLike
	if body.ParentID != nil && *body.ParentID != "" {
		mr, merr := s.comments.Reply(r.Context(), slug, *body.ParentID, authorFromSession(session), body.Text, version)
		if merr != nil {
			return merr
		}
		res = mutationLike(mr)
	} else {
		mr, merr := s.comments.Create(r.Context(), slug, authorFromSession(session), body.Text, body.Anchor, version)
		if merr != nil {
			return merr
		}
		res = mutationLike(mr)
	}
	writeJSON(w, res.Status, res.Body)
	return nil
}

func (s *Server) handlePatchComment(w http.ResponseWriter, r *http.Request) error {
	session, err := s.viewer(r)
	if err != nil {
		return err
	}
	var body struct {
		Slug    string       `json:"slug"`
		ID      string       `json:"id"`
		Anchor  *core.Anchor `json:"anchor"`
		Version json.Number  `json:"version"`
	}
	_ = decodeJSON(r, &body)
	slug, err := requireSlug(body.Slug)
	if err != nil {
		return err
	}
	if body.ID == "" || body.Anchor == nil {
		return apperr.Validation("slug, id, anchor required", "anchor_required")
	}
	if err := s.authorizeMutation(r, slug, body.ID, session); err != nil {
		return err
	}
	actor := actorLogin(session)
	mr, err := s.comments.Reanchor(r.Context(), slug, body.ID, body.Anchor, numOr1(body.Version), actor)
	if err != nil {
		return err
	}
	writeJSON(w, mr.Status, mr.Body)
	return nil
}

func (s *Server) handleDeleteComment(w http.ResponseWriter, r *http.Request) error {
	slug, err := requireSlug(r.URL.Query().Get("slug"))
	if err != nil {
		return err
	}
	if r.URL.Query().Get("all") == "1" {
		return s.wipeComments(w, r, slug)
	}
	session, err := s.viewer(r)
	if err != nil {
		return err
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		return apperr.Validation("slug and id required", "id_required")
	}
	if err := s.authorizeMutation(r, slug, id, session); err != nil {
		return err
	}
	version := parseVersionQuery(r.URL.Query().Get("version"))
	if version == core.VersionLatest {
		version = 999_999
	}
	mr, err := s.comments.Remove(r.Context(), slug, id, version, actorLogin(session))
	if err != nil {
		return err
	}
	writeJSON(w, mr.Status, mr.Body)
	return nil
}

func (s *Server) handleReact(w http.ResponseWriter, r *http.Request) error {
	session, err := s.viewer(r)
	if err != nil {
		return err
	}
	var body struct {
		Slug      string      `json:"slug"`
		CommentID string      `json:"comment_id"`
		Emoji     string      `json:"emoji"`
		Version   json.Number `json:"version"`
	}
	_ = decodeJSON(r, &body)
	slug, err := requireSlug(body.Slug)
	if err != nil {
		return err
	}
	if body.CommentID == "" || body.Emoji == "" {
		return apperr.Validation("slug, comment_id, emoji required", "reaction_fields_required")
	}
	if len(body.Emoji) == 0 || len(body.Emoji) > 32 {
		return apperr.Validation("invalid emoji", "invalid_emoji")
	}
	by := "anon"
	if session != nil {
		by = session.Login
	}
	mr, err := s.comments.React(r.Context(), slug, body.CommentID, body.Emoji, by, numOr1(body.Version))
	if err != nil {
		return err
	}
	writeJSON(w, mr.Status, mr.Body)
	return nil
}

func (s *Server) wipeComments(w http.ResponseWriter, r *http.Request, slug string) error {
	token := bearerToken(r)
	ok, err := s.auth.IsValidWriteToken(r.Context(), token)
	if err != nil {
		return err
	}
	if token == "" || !ok {
		return apperr.Unauthorized("", "")
	}
	mr, err := s.comments.Wipe(r.Context(), slug)
	if err != nil {
		return err
	}
	writeJSON(w, mr.Status, mr.Body)
	return nil
}

// authorizeMutation enforces that the viewer is the author or owner (only when
// GitHub auth is configured; local mode is unauthenticated).
func (s *Server) authorizeMutation(r *http.Request, slug, id string, session *storage.Session) error {
	if s.cfg.GitHubClientID == "" {
		return nil
	}
	list, err := s.comments.Read(r.Context(), slug)
	if err != nil {
		return err
	}
	author := findAuthorRecord(list, id)
	if author == nil {
		return apperr.Validation("not found", "not_found")
	}
	if s.auth.IsOwner(session) {
		return nil
	}
	if author.Login != "" && session != nil && author.Login == session.Login {
		return nil
	}
	return apperr.Forbidden("not the author", "not_author")
}

// findAuthorRecord returns the author of a comment or reply id, or nil.
func findAuthorRecord(list []core.Comment, id string) *core.Author {
	comment, reply := core.FindHost(list, id)
	if comment == nil {
		return nil
	}
	if reply != nil {
		return reply.Author
	}
	return comment.Author
}

func actorLogin(session *storage.Session) string {
	if session != nil {
		return session.Login
	}
	return "local"
}

func numOr1(n json.Number) int {
	if n == "" {
		return 1
	}
	v, err := n.Int64()
	if err != nil || v == 0 {
		return 1
	}
	return int(v)
}
