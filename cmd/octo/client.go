package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-doc/internal/core"
)

// client is a thin HTTP client for the octo-doc /v1 envelope API. It defines its
// own request/response structs (the server's transport DTOs are internal and
// unexported) and, critically, expects the wire timestamp field "created_at" —
// distinct from the on-disk "created".
type client struct {
	baseURL string
	token   string
	http    *http.Client
}

func newClient(baseURL, token string) *client {
	return &client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 120 * time.Second},
	}
}

// envelope is the /v1 response wrapper: success carries data, failure carries error.
type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *apiError       `json:"error"`
}

// apiError is the /v1 error body.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *apiError) String() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

// publishReq is the POST /v1/docs request body: {slug, version, html, meta, comments}.
type publishReq struct {
	Slug     string       `json:"slug"`
	Version  int          `json:"version"`
	HTML     string       `json:"html"`
	Meta     *publishMeta `json:"meta,omitempty"`
	Comments []comment    `json:"comments,omitempty"`
}

// publishMeta is the doc's meta.json sent under the publish `meta` key. The
// server currently honors only Title (it rebuilds the version list from the
// uploaded blobs and stamps its own timestamps); Versions is sent for
// completeness and forward-compatibility, but per-version prompts are not yet
// persisted server-side, so don't rely on them surviving a round-trip.
type publishMeta struct {
	Title    string       `json:"title,omitempty"`
	Versions []versionRef `json:"versions,omitempty"`
}

// publishResp is the POST /v1/docs success payload.
type publishResp struct {
	Slug           string `json:"slug"`
	Version        int    `json:"version"`
	URL            string `json:"url"`
	Size           int    `json:"size"`
	MergedComments int    `json:"merged_comments"`
}

// wireComment is a comment as returned by GET /v1/comments (created_at, not created).
type wireComment struct {
	ID        string              `json:"id"`
	Version   int                 `json:"version"`
	Anchor    *core.Anchor        `json:"anchor"`
	Text      string              `json:"text"`
	Author    *core.Author        `json:"author"`
	Status    string              `json:"status"`
	CreatedAt string              `json:"created_at"`
	AppliedIn *int                `json:"applied_in,omitempty"`
	Replies   []wireReply         `json:"replies"`
	Reactions map[string][]string `json:"reactions"`
}

// wireReply is a reply as returned on the wire (created_at).
type wireReply struct {
	ID          string              `json:"id"`
	ParentID    string              `json:"parent_id"`
	Text        string              `json:"text"`
	Author      *core.Author        `json:"author"`
	AgentStatus string              `json:"agent_status,omitempty"`
	CreatedAt   string              `json:"created_at"`
	Reactions   map[string][]string `json:"reactions"`
}

// toComment maps a wire comment to the on-disk shape (created_at → created).
func (w wireComment) toComment() comment {
	c := comment{
		ID: w.ID, Version: w.Version, Anchor: w.Anchor, Text: w.Text,
		Author: w.Author, Status: w.Status, Created: w.CreatedAt,
		AppliedIn: w.AppliedIn, Reactions: w.Reactions,
	}
	c.Replies = make([]reply, 0, len(w.Replies))
	for _, r := range w.Replies {
		c.Replies = append(c.Replies, reply{
			ID: r.ID, ParentID: r.ParentID, Text: r.Text, Author: r.Author,
			AgentStatus: r.AgentStatus, Created: r.CreatedAt, Reactions: r.Reactions,
		})
	}
	return c
}

// do performs a request and unwraps the /v1 envelope, returning the raw data.
func (c *client) do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if env.Error != nil {
		return nil, fmt.Errorf("%s", env.Error.String())
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return env.Data, nil
}

// publish uploads one version. The server merges comments non-destructively by id.
func (c *client) publish(ctx context.Context, req publishReq) (*publishResp, error) {
	data, err := c.do(ctx, http.MethodPost, "/v1/docs", req)
	if err != nil {
		return nil, err
	}
	var out publishResp
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// listComments fetches the full cross-version comment history for a slug.
func (c *client) listComments(ctx context.Context, slug string) ([]wireComment, error) {
	data, err := c.do(ctx, http.MethodGet, "/v1/comments?slug="+slug+"&version=all", nil)
	if err != nil {
		return nil, err
	}
	var out []wireComment
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("server did not return a comment array: %w", err)
	}
	return out, nil
}

// unpublish deletes a published doc (all versions + comments).
func (c *client) unpublish(ctx context.Context, slug string) error {
	_, err := c.do(ctx, http.MethodDelete, "/v1/docs/"+slug, nil)
	return err
}

// agentReplyReq is the POST /v1/agent/replies request body.
type agentReplyReq struct {
	Slug      string `json:"slug"`
	ParentID  string `json:"parent_id"`
	Text      string `json:"text"`
	Status    string `json:"status,omitempty"`
	AppliedIn *int   `json:"applied_in,omitempty"`
}

// agentReply posts a reply attributed to the agent on a remote server.
func (c *client) agentReply(ctx context.Context, req agentReplyReq) error {
	_, err := c.do(ctx, http.MethodPost, "/v1/agent/replies", req)
	return err
}

// shareResp is the POST /v1/docs/{slug}/share success payload.
type shareResp struct {
	Slug string `json:"slug"`
	Code string `json:"code"`
	URL  string `json:"url"`
}

// share mints (or rotates) the doc's share code and returns the coded read URL.
func (c *client) share(ctx context.Context, slug string) (*shareResp, error) {
	data, err := c.do(ctx, http.MethodPost, "/v1/docs/"+slug+"/share", nil)
	if err != nil {
		return nil, err
	}
	var out shareResp
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// revokeShare clears the doc's share code (existing links stop working).
func (c *client) revokeShare(ctx context.Context, slug string) error {
	_, err := c.do(ctx, http.MethodDelete, "/v1/docs/"+slug+"/share", nil)
	return err
}

// commentReq is the POST /v1/comments request body. A parent_id makes it a reply;
// an anchor binds a top-level comment to specific text. Reads/comments are public,
// so no token is required.
type commentReq struct {
	Slug     string       `json:"slug"`
	Text     string       `json:"text"`
	Version  int          `json:"version,omitempty"`
	ParentID string       `json:"parent_id,omitempty"`
	Anchor   *core.Anchor `json:"anchor,omitempty"`
}

// createComment posts a human comment (or a reply, if ParentID is set) and returns
// the new id.
func (c *client) createComment(ctx context.Context, req commentReq) (string, error) {
	data, err := c.do(ctx, http.MethodPost, "/v1/comments", req)
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// react toggles an emoji reaction on a comment (public endpoint, keyed server-side
// to the anonymous viewer).
func (c *client) react(ctx context.Context, slug, commentID, emoji string, version int) error {
	body := map[string]any{"slug": slug, "comment_id": commentID, "emoji": emoji}
	if version > 0 {
		body["version"] = version
	}
	_, err := c.do(ctx, http.MethodPost, "/v1/reactions", body)
	return err
}
