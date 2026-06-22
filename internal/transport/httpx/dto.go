package httpx

import (
	"github.com/Mininglamp-OSS/octo-doc/internal/core"
	"github.com/Mininglamp-OSS/octo-doc/internal/service"
)

// Transport DTOs for comment snapshots.
//
// core.CommentSnapshot uses the field name "created" and is byte-equivalence
// locked (its JSON shape is asserted by golden fixtures ported from upstream
// tdoc — see CLAUDE.md / docs/PORTING.md). The OCTO API contract (R3) requires
// timestamp fields to carry the "_at" suffix. We satisfy R3 at the wire boundary
// by mapping the core snapshot into these DTOs, leaving core untouched.

// replyDTO is the wire shape of a reply (created → created_at per R3).
type replyDTO struct {
	ID          string            `json:"id"`
	ParentID    string            `json:"parent_id"`
	Author      *core.Author      `json:"author"`
	Text        string            `json:"text"`
	AgentStatus *core.AgentStatus `json:"agent_status"`
	CreatedAt   string            `json:"created_at"`
	Reactions   core.Reactions    `json:"reactions"`
	Deleted     bool              `json:"deleted"`
}

// commentDTO is the wire shape of a comment (created → created_at per R3).
type commentDTO struct {
	ID        string         `json:"id"`
	Author    *core.Author   `json:"author"`
	CreatedAt string         `json:"created_at"`
	CreatedIn int            `json:"created_in"`
	Version   int            `json:"version"`
	Anchor    *core.Anchor   `json:"anchor"`
	Text      string         `json:"text"`
	Status    string         `json:"status"`
	AppliedIn *int           `json:"applied_in,omitempty"`
	Replies   []replyDTO     `json:"replies"`
	Reactions core.Reactions `json:"reactions"`
	Deleted   bool           `json:"deleted"`
}

// toCommentDTO maps a core snapshot to its wire DTO.
func toCommentDTO(c core.CommentSnapshot) commentDTO {
	replies := make([]replyDTO, 0, len(c.Replies))
	for _, r := range c.Replies {
		replies = append(replies, replyDTO{
			ID: r.ID, ParentID: r.ParentID, Author: r.Author, Text: r.Text,
			AgentStatus: r.AgentStatus, CreatedAt: r.Created, Reactions: r.Reactions, Deleted: r.Deleted,
		})
	}
	return commentDTO{
		ID: c.ID, Author: c.Author, CreatedAt: c.Created, CreatedIn: c.CreatedIn,
		Version: c.Version, Anchor: c.Anchor, Text: c.Text, Status: c.Status,
		AppliedIn: c.AppliedIn, Replies: replies, Reactions: c.Reactions, Deleted: c.Deleted,
	}
}

// toCommentDTOs maps a snapshot list to wire DTOs (never nil).
func toCommentDTOs(list []core.CommentSnapshot) []commentDTO {
	out := make([]commentDTO, 0, len(list))
	for _, c := range list {
		out = append(out, toCommentDTO(c))
	}
	return out
}

// versionRefDTO is the wire shape of a version ref (created → created_at per R3).
// storage.VersionRef can't carry the "_at" suffix directly: its json tags are
// persisted to metadata storage (see storage/docmeta.go). We remap at the wire
// boundary instead.
type versionRefDTO struct {
	N         int     `json:"n"`
	CreatedAt *string `json:"created_at,omitempty"`
}

// versionListDTO is the wire shape of the versions response.
type versionListDTO struct {
	Slug     string          `json:"slug"`
	Title    string          `json:"title"`
	Versions []versionRefDTO `json:"versions"`
}

// toVersionListDTO maps a service VersionList to its wire DTO.
func toVersionListDTO(vl *service.VersionList) versionListDTO {
	refs := make([]versionRefDTO, 0, len(vl.Versions))
	for _, v := range vl.Versions {
		refs = append(refs, versionRefDTO{N: v.N, CreatedAt: v.Created})
	}
	return versionListDTO{Slug: vl.Slug, Title: vl.Title, Versions: refs}
}

// mutationDTO normalizes a service mutation Body to the wire contract. The body
// is heterogeneous (core-defined, golden-locked): a *core.CommentSnapshot for
// create/reanchor, or a map for reply/react/delete. We remap only the snapshot
// (created → created_at per R3) and the reply map's "created" key; other maps
// already use compliant keys.
func mutationDTO(body any) any {
	switch v := body.(type) {
	case *core.CommentSnapshot:
		if v == nil {
			return body
		}
		return toCommentDTO(*v)
	case core.CommentSnapshot:
		return toCommentDTO(v)
	case map[string]any:
		if _, ok := v["created"]; ok {
			m := make(map[string]any, len(v))
			for k, val := range v {
				if k == "created" {
					m["created_at"] = val
				} else {
					m[k] = val
				}
			}
			return m
		}
		return v
	default:
		return body
	}
}
