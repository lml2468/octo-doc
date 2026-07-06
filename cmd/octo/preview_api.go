package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-doc/internal/core"
)

// maxBodyBytes caps preview request bodies so a hostile/buggy client can't OOM
// the local server. Comments are small; 1 MiB is generous.
const maxBodyBytes = 1 << 20

// agentLogin is the identity attributed to agent replies. It stays "tdoc-agent"
// to match the server + golden fixtures (part of the frozen wire contract).
const agentLogin = "tdoc-agent"

// agentStatusEmoji maps an agent verdict to its comment-list emoji.
var agentStatusEmoji = map[string]string{"applied": "✅", "partial": "🟡", "question": "❓"}

// routes builds the preview server's HTTP handler.
func (ps *previewServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", ps.handleRoot)
	mux.HandleFunc("/v1/ping", ps.handlePing)
	mux.HandleFunc("/v1/comments", ps.handleComments)
	mux.HandleFunc("/v1/agent/replies", ps.handleAgentReply)
	mux.HandleFunc("/v1/reactions", ps.handleReactions)
	mux.HandleFunc("/v1/publish", ps.handlePublish)
	return mux
}

// --- envelope helpers -------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// dataEnv writes a {data} success envelope. Preview mutations always succeed with
// 200 (validation failures go through errEnv), so no status parameter is needed.
func dataEnv(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

func listEnv(w http.ResponseWriter, items any, total int) {
	writeJSON(w, http.StatusOK, map[string]any{
		"data":       items,
		"pagination": map[string]int{"total": total, "page": 1, "page_size": total},
	})
}

var errEnum = map[int]string{
	400: "VALIDATION_ERROR", 401: "AUTH_REQUIRED", 403: "FORBIDDEN", 404: "NOT_FOUND",
	409: "CONFLICT", 413: "PAYLOAD_TOO_LARGE", 415: "UNSUPPORTED_MEDIA_TYPE",
	429: "RATE_LIMITED", 503: "UPSTREAM_UNAVAILABLE",
}

func errEnv(w http.ResponseWriter, status int, message string) {
	code := errEnum[status]
	if code == "" {
		code = "INTERNAL_ERROR"
	}
	if message == "" {
		message = code
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// --- guards -----------------------------------------------------------------

// isLocalMutation guards state-mutating requests. The local server has no auth
// (localhost-only by design), so a drive-by web page must not be able to drive it
// via CSRF: require an application/json content-type (defeats CORS-simple POSTs)
// and reject any non-loopback Origin.
func isLocalMutation(r *http.Request) bool {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if ct != "application/json" {
		return false
	}
	return originIsLocal(r)
}

// originIsLocal returns true if there is no Origin header or it is loopback.
func originIsLocal(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// readBody decodes a JSON request body into v, enforcing the size cap. A malformed
// body decodes to the zero value (matching server.js's tolerant parse).
func readBody(r *http.Request, v any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return err
	}
	if len(b) > maxBodyBytes {
		return errTooLarge
	}
	if len(b) == 0 {
		return nil
	}
	_ = json.Unmarshal(b, v) // tolerant: leave zero value on parse error
	return nil
}

var errTooLarge = fmt.Errorf("payload too large")

// --- handlers ---------------------------------------------------------------

// handlePing answers the identity marker health checks grep for. A foreign
// process answering 200 on this port must not pass as octo.
func (ps *previewServer) handlePing(w http.ResponseWriter, _ *http.Request) {
	dataEnv(w, map[string]any{"ok": true, "service": "octo"})
}

// handleRoot serves the doc index at "/" and rendered docs at /d/{slug}/v/{n}.
func (ps *previewServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, ps.indexPage())
		return
	}
	slug, v, ok := parseDocPath(r.URL.Path)
	if !ok {
		errEnv(w, http.StatusNotFound, "Not found")
		return
	}
	if safeSlug(slug) == "" {
		http.Error(w, "invalid slug", http.StatusBadRequest)
		return
	}
	rendered, err := ps.renderDoc(slug, v)
	if err != nil {
		http.Error(w, fmt.Sprintf("Not found: %s v%d", slug, v), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, rendered)
}

// parseDocPath matches /d/{slug}/v/{n}[/].
func parseDocPath(p string) (string, int, bool) {
	p = strings.TrimSuffix(p, "/")
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(parts) != 4 || parts[0] != "d" || parts[2] != "v" {
		return "", 0, false
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n < 1 {
		return "", 0, false
	}
	return parts[1], n, true
}

// handleComments dispatches the /v1/comments verbs.
func (ps *previewServer) handleComments(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ps.listComments(w, r)
	case http.MethodPost:
		ps.createComment(w, r)
	case http.MethodPatch:
		ps.reanchorComment(w, r)
	case http.MethodDelete:
		ps.deleteComment(w, r)
	default:
		errEnv(w, http.StatusNotFound, "Not found")
	}
}

// listComments returns a slug's comments mapped to the wire shape (created_at).
func (ps *previewServer) listComments(w http.ResponseWriter, r *http.Request) {
	slug := safeSlug(r.URL.Query().Get("slug"))
	if slug == "" {
		errEnv(w, http.StatusBadRequest, "invalid or missing slug")
		return
	}
	list, _ := ps.store.readComments(slug)
	wire := make([]wireComment, 0, len(list))
	for _, c := range list {
		wire = append(wire, c.toWire())
	}
	listEnv(w, wire, len(wire))
}

// createComment appends a top-level comment or, if parent_id is set, a reply.
func (ps *previewServer) createComment(w http.ResponseWriter, r *http.Request) {
	if !isLocalMutation(r) {
		errEnv(w, http.StatusForbidden, "forbidden")
		return
	}
	var body struct {
		Slug     string       `json:"slug"`
		Version  int          `json:"version"`
		Anchor   *core.Anchor `json:"anchor"`
		Text     string       `json:"text"`
		ParentID string       `json:"parent_id"`
	}
	if err := readBody(r, &body); err != nil {
		errEnv(w, http.StatusRequestEntityTooLarge, "payload_too_large")
		return
	}
	slug := safeSlug(body.Slug)
	if slug == "" || body.Text == "" {
		errEnv(w, http.StatusBadRequest, "invalid slug or missing text")
		return
	}
	list, _ := ps.store.readComments(slug)
	now := nowISO()
	if body.ParentID != "" {
		idx := findComment(list, body.ParentID)
		if idx < 0 {
			errEnv(w, http.StatusNotFound, "parent_not_found")
			return
		}
		rep := reply{
			ID: newReplyID(), ParentID: body.ParentID, Text: body.Text,
			Author: nil, Created: now, Reactions: map[string][]string{},
		}
		list[idx].Replies = append(list[idx].Replies, rep)
		if err := ps.store.writeComments(slug, list); err != nil {
			errEnv(w, http.StatusInternalServerError, "write_failed")
			return
		}
		dataEnv(w, rep.toWire())
		return
	}
	version := body.Version
	if version == 0 {
		version = 1
	}
	c := comment{
		ID: newCommentID(), Version: version, Anchor: body.Anchor, Text: body.Text,
		Author: nil, Status: "open", Created: now, Replies: []reply{}, Reactions: map[string][]string{},
	}
	list = append(list, c)
	if err := ps.store.writeComments(slug, list); err != nil {
		errEnv(w, http.StatusInternalServerError, "write_failed")
		return
	}
	dataEnv(w, c.toWire())
}

// reanchorComment re-points a comment at new text, resetting agent state. A
// re-anchor means the comment now targets different content, so any prior verdict
// is stale: status flips back to open and the agent's status emoji is cleared.
func (ps *previewServer) reanchorComment(w http.ResponseWriter, r *http.Request) {
	if !isLocalMutation(r) {
		errEnv(w, http.StatusForbidden, "forbidden")
		return
	}
	var body struct {
		Slug   string       `json:"slug"`
		ID     string       `json:"id"`
		Anchor *core.Anchor `json:"anchor"`
	}
	if err := readBody(r, &body); err != nil {
		errEnv(w, http.StatusRequestEntityTooLarge, "payload_too_large")
		return
	}
	slug := safeSlug(body.Slug)
	if slug == "" || body.ID == "" || body.Anchor == nil {
		errEnv(w, http.StatusBadRequest, "invalid slug or missing id/anchor")
		return
	}
	list, _ := ps.store.readComments(slug)
	idx := findComment(list, body.ID)
	if idx < 0 {
		errEnv(w, http.StatusNotFound, "not_found")
		return
	}
	list[idx].Anchor = body.Anchor
	list[idx].Status = "open"
	list[idx].AppliedIn = nil
	setAgentReaction(&list[idx].Reactions, "")
	if err := ps.store.writeComments(slug, list); err != nil {
		errEnv(w, http.StatusInternalServerError, "write_failed")
		return
	}
	dataEnv(w, list[idx].toWire())
}

// deleteComment removes a top-level comment or a nested reply by id.
func (ps *previewServer) deleteComment(w http.ResponseWriter, r *http.Request) {
	// DELETE carries no body, so the JSON content-type check doesn't apply; reject
	// non-local Origins explicitly for defense in depth.
	if !originIsLocal(r) {
		errEnv(w, http.StatusForbidden, "forbidden")
		return
	}
	slug := safeSlug(r.URL.Query().Get("slug"))
	id := r.URL.Query().Get("id")
	if slug == "" || id == "" {
		errEnv(w, http.StatusBadRequest, "invalid slug or missing id")
		return
	}
	list, _ := ps.store.readComments(slug)
	if idx := findComment(list, id); idx >= 0 {
		list = append(list[:idx], list[idx+1:]...)
		if err := ps.store.writeComments(slug, list); err != nil {
			errEnv(w, http.StatusInternalServerError, "write_failed")
			return
		}
		dataEnv(w, map[string]any{})
		return
	}
	for ci := range list {
		for ri := range list[ci].Replies {
			if list[ci].Replies[ri].ID == id {
				list[ci].Replies = append(list[ci].Replies[:ri], list[ci].Replies[ri+1:]...)
				if err := ps.store.writeComments(slug, list); err != nil {
					errEnv(w, http.StatusInternalServerError, "write_failed")
					return
				}
				dataEnv(w, map[string]any{})
				return
			}
		}
	}
	errEnv(w, http.StatusNotFound, "not_found")
}

// handleAgentReply posts a reply attributed to the agent, updates the parent's
// status, and stamps a status emoji on the parent's reactions row.
func (ps *previewServer) handleAgentReply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errEnv(w, http.StatusNotFound, "Not found")
		return
	}
	if !isLocalMutation(r) {
		errEnv(w, http.StatusForbidden, "forbidden")
		return
	}
	var body struct {
		Slug      string `json:"slug"`
		ParentID  string `json:"parent_id"`
		Text      string `json:"text"`
		Status    string `json:"status"`
		AppliedIn *int   `json:"applied_in"`
	}
	if err := readBody(r, &body); err != nil {
		errEnv(w, http.StatusRequestEntityTooLarge, "payload_too_large")
		return
	}
	slug := safeSlug(body.Slug)
	if slug == "" || body.ParentID == "" || body.Text == "" {
		errEnv(w, http.StatusBadRequest, "invalid slug or missing parent_id/text")
		return
	}
	list, _ := ps.store.readComments(slug)
	idx := findComment(list, body.ParentID)
	if idx < 0 {
		errEnv(w, http.StatusNotFound, "parent_not_found")
		return
	}
	status := ""
	switch body.Status {
	case "applied", "partial", "question":
		status = body.Status
	}
	avatar := (*string)(nil)
	rep := reply{
		ID: newReplyID(), ParentID: body.ParentID, Text: body.Text,
		Author:      &core.Author{Kind: "agent", Login: agentLogin, Name: agentLogin, AvatarURL: avatar},
		AgentStatus: status, Created: nowISO(), Reactions: map[string][]string{},
	}
	list[idx].Replies = append(list[idx].Replies, rep)
	switch status {
	case "applied":
		list[idx].Status = "applied"
		if body.AppliedIn != nil {
			list[idx].AppliedIn = body.AppliedIn
		}
	case "question", "partial":
		list[idx].Status = "open"
	}
	setAgentReaction(&list[idx].Reactions, status)
	if err := ps.store.writeComments(slug, list); err != nil {
		errEnv(w, http.StatusInternalServerError, "write_failed")
		return
	}
	dataEnv(w, rep.toWire())
}

// handleReactions toggles an emoji reaction (anonymous, keyed by an "anon"
// pseudo-user) on a comment or reply.
func (ps *previewServer) handleReactions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errEnv(w, http.StatusNotFound, "Not found")
		return
	}
	if !isLocalMutation(r) {
		errEnv(w, http.StatusForbidden, "forbidden")
		return
	}
	var body struct {
		Slug      string `json:"slug"`
		CommentID string `json:"comment_id"`
		Emoji     string `json:"emoji"`
	}
	if err := readBody(r, &body); err != nil {
		errEnv(w, http.StatusRequestEntityTooLarge, "payload_too_large")
		return
	}
	slug := safeSlug(body.Slug)
	if slug == "" || body.CommentID == "" || body.Emoji == "" {
		errEnv(w, http.StatusBadRequest, "invalid slug or missing comment_id/emoji")
		return
	}
	if len(body.Emoji) > 8 {
		errEnv(w, http.StatusBadRequest, "invalid_emoji")
		return
	}
	list, _ := ps.store.readComments(slug)
	reactions := findReactions(list, body.CommentID)
	if reactions == nil {
		errEnv(w, http.StatusNotFound, "not_found")
		return
	}
	toggleReaction(*reactions, body.Emoji, "anon")
	if err := ps.store.writeComments(slug, list); err != nil {
		errEnv(w, http.StatusInternalServerError, "write_failed")
		return
	}
	dataEnv(w, map[string]any{"ok": true, "reactions": *reactions})
}

// handlePublish runs a publish in-process for the overlay's local-mode Publish
// button, which POSTs {slug} to /v1/publish and expects {data:{url}}. It takes
// the identical code path as `octo publish`, so a browser publish and a CLI
// publish behave the same. Requires a configured server + write token.
func (ps *previewServer) handlePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errEnv(w, http.StatusNotFound, "Not found")
		return
	}
	if !isLocalMutation(r) {
		errEnv(w, http.StatusForbidden, "forbidden")
		return
	}
	var body struct {
		Slug string `json:"slug"`
	}
	if err := readBody(r, &body); err != nil {
		errEnv(w, http.StatusRequestEntityTooLarge, "payload_too_large")
		return
	}
	slug := safeSlug(body.Slug)
	if slug == "" {
		errEnv(w, http.StatusBadRequest, "invalid slug")
		return
	}
	url, err := publishDoc(r.Context(), ps.cfg, slug, io.Discard)
	if err != nil {
		// No server configured / no token / upload failure — surface as a 503 the
		// overlay renders in its status line.
		errEnv(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	dataEnv(w, map[string]any{"url": url})
}

// indexPage renders the doc listing at "/".
func (ps *previewServer) indexPage() string {
	slugs, _ := ps.store.listSlugs()
	var rows strings.Builder
	for _, slug := range slugs {
		meta, err := ps.store.readMeta(slug)
		if err != nil {
			meta = &docMeta{Title: slug}
		}
		latest := meta.latestVersion()
		list, _ := ps.store.readComments(slug)
		open := 0
		for _, c := range list {
			if c.Status == "open" {
				open++
			}
		}
		title := meta.Title
		if title == "" {
			title = slug
		}
		openCell := "—"
		if open > 0 {
			openCell = fmt.Sprintf("<b>%d open</b>", open)
		}
		fmt.Fprintf(&rows, `<tr><td><a href="/d/%s/v/%d">%s</a></td><td>%s</td><td>v%d</td><td>%s</td></tr>`,
			url.PathEscape(slug), latest, html.EscapeString(title), html.EscapeString(slug), latest, openCell)
	}
	body := `<p class="empty">No docs yet. Try <code>octo new</code>.</p>`
	if len(slugs) > 0 {
		body = `<table><thead><tr><th>Title</th><th>Slug</th><th>Version</th><th>Comments</th></tr></thead><tbody>` +
			rows.String() + `</tbody></table>`
	}
	return `<!doctype html><html><head><meta charset="utf-8"><title>octo</title>
<style>
  body { font: 15px system-ui, -apple-system, sans-serif; max-width: 760px; margin: 60px auto; padding: 0 20px; color: #111; }
  h1 { font-size: 28px; margin: 0 0 4px; color: #1652f0; }
  .sub { color: #666; margin: 0 0 32px; }
  table { width: 100%; border-collapse: collapse; }
  th, td { text-align: left; padding: 10px 12px; border-bottom: 1px solid #eee; }
  th { font-size: 12px; text-transform: uppercase; color: #888; letter-spacing: 0.04em; }
  a { color: #1652f0; text-decoration: none; }
  a:hover { text-decoration: underline; }
  .empty { color: #888; padding: 40px 0; text-align: center; }
</style></head><body>
<h1>octo</h1><p class="sub">Prompt-native documents.</p>
` + body + `
</body></html>`
}

// --- time + reaction helpers ------------------------------------------------

// nowISO returns the current UTC time as an ISO-8601 Z timestamp.
func nowISO() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// newCommentID / newReplyID mint ids in the server's format: a prefix, the
// timestamp digits, and a random suffix. The random suffix is what prevents
// collisions when two comments are created in the same millisecond — without it
// findComment/findReactions/delete would all act on the first match and the
// second record would be unaddressable.
func newCommentID() string { return "c_" + idStamp() }
func newReplyID() string   { return "r_" + idStamp() }

// idStamp is the shared time+random tail of an id: compact timestamp + "_" + 8 hex.
func idStamp() string {
	digits := make([]byte, 0, 20)
	for _, c := range nowISO() {
		if c >= '0' && c <= '9' {
			digits = append(digits, byte(c))
		}
	}
	var rnd [4]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		panic("octo: crypto/rand failed: " + err.Error())
	}
	return string(digits) + "_" + hex.EncodeToString(rnd[:])
}

// findComment returns the index of a top-level comment by id, or -1.
func findComment(list []comment, id string) int {
	for i := range list {
		if list[i].ID == id {
			return i
		}
	}
	return -1
}

// findReactions returns a pointer to the reactions map of a comment or reply by
// id (allocating the map if nil), or nil if the id isn't found.
func findReactions(list []comment, id string) *map[string][]string {
	for i := range list {
		if list[i].ID == id {
			if list[i].Reactions == nil {
				list[i].Reactions = map[string][]string{}
			}
			return &list[i].Reactions
		}
		for j := range list[i].Replies {
			if list[i].Replies[j].ID == id {
				if list[i].Replies[j].Reactions == nil {
					list[i].Replies[j].Reactions = map[string][]string{}
				}
				return &list[i].Replies[j].Reactions
			}
		}
	}
	return nil
}

// toggleReaction adds or removes user from the emoji's reactor list, deleting the
// emoji key when it empties.
func toggleReaction(reactions map[string][]string, emoji, user string) {
	users := reactions[emoji]
	for i, u := range users {
		if u == user {
			users = append(users[:i], users[i+1:]...)
			if len(users) == 0 {
				delete(reactions, emoji)
			} else {
				reactions[emoji] = users
			}
			return
		}
	}
	reactions[emoji] = append(users, user)
}

// setAgentReaction replaces the agent's reaction with the emoji for status,
// clearing any prior agent reaction first so stale state can't outlive a newer
// outcome. An empty status just clears.
func setAgentReaction(reactions *map[string][]string, status string) {
	if *reactions == nil {
		*reactions = map[string][]string{}
	}
	m := *reactions
	for emoji, users := range m {
		for i, u := range users {
			if u == agentLogin {
				users = append(users[:i], users[i+1:]...)
				break
			}
		}
		if len(users) == 0 {
			delete(m, emoji)
		} else {
			m[emoji] = users
		}
	}
	next := agentStatusEmoji[status]
	if next == "" {
		return
	}
	if slices.Contains(m[next], agentLogin) {
		return
	}
	m[next] = append(m[next], agentLogin)
}
