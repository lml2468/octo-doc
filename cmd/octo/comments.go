package main

import "github.com/Mininglamp-OSS/octo-doc/internal/core"

// comment is the on-disk shape of a comment in comments.json: a flat record with
// nested replies. It uses "created" (the storage schema); the wire boundary
// (publish/pull, preview API) maps to/from "created_at". Fields mirror what the
// server persists and what the publish endpoint accepts, so the array round-trips
// losslessly.
type comment struct {
	ID        string              `json:"id"`
	Version   int                 `json:"version,omitempty"`
	Anchor    *core.Anchor        `json:"anchor"`
	Text      string              `json:"text"`
	Author    *core.Author        `json:"author"`
	Status    string              `json:"status,omitempty"`
	Created   string              `json:"created"`
	AppliedIn *int                `json:"applied_in,omitempty"`
	Replies   []reply             `json:"replies"`
	Reactions map[string][]string `json:"reactions"`
}

// reply is the on-disk shape of a reply nested under a comment.
type reply struct {
	ID          string              `json:"id"`
	ParentID    string              `json:"parent_id"`
	Text        string              `json:"text"`
	Author      *core.Author        `json:"author"`
	AgentStatus string              `json:"agent_status,omitempty"`
	Created     string              `json:"created"`
	Reactions   map[string][]string `json:"reactions"`
}

// toWire maps an on-disk comment to the wire shape (created → created_at),
// mirroring the server's transport DTO so the preview API and the published
// server look identical to the shared overlay.
func (c comment) toWire() wireComment {
	w := wireComment{
		ID: c.ID, Version: c.Version, Anchor: c.Anchor, Text: c.Text,
		Author: c.Author, Status: c.Status, CreatedAt: c.Created,
		AppliedIn: c.AppliedIn, Reactions: c.Reactions,
	}
	w.Replies = make([]wireReply, 0, len(c.Replies))
	for _, r := range c.Replies {
		w.Replies = append(w.Replies, r.toWire())
	}
	return w
}

// toWire maps an on-disk reply to the wire shape (created → created_at).
func (r reply) toWire() wireReply {
	return wireReply{
		ID: r.ID, ParentID: r.ParentID, Text: r.Text, Author: r.Author,
		AgentStatus: r.AgentStatus, CreatedAt: r.Created, Reactions: r.Reactions,
	}
}
