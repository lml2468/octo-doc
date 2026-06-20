package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type opsIn struct {
	List []Comment       `json:"list"`
	Op   json.RawMessage `json:"op"`
}

// opJSON mirrors the golden op shape (camelCase keys from the TS CommentOp union).
type opJSON struct {
	Kind        string                  `json:"kind"`
	At          string                  `json:"at"`
	ID          string                  `json:"id"`
	Author      *Author                 `json:"author"`
	Text        string                  `json:"text"`
	Anchor      *Anchor                 `json:"anchor"`
	ParentID    string                  `json:"parent_id"`
	ReplyID     string                  `json:"reply_id"`
	ResetStatus bool                    `json:"reset_status"`
	Actor       *struct{ Login string } `json:"actor"`
	CommentID   string                  `json:"comment_id"`
	Emoji       string                  `json:"emoji"`
	By          string                  `json:"by"`
	Version     int                     `json:"version"`
}

func toCommentOp(j opJSON) CommentOp {
	actor := ""
	if j.Actor != nil {
		actor = j.Actor.Login
	}
	return CommentOp{
		Kind: j.Kind, At: j.At, ID: j.ID, Author: j.Author, Text: j.Text, Anchor: j.Anchor,
		ParentID: j.ParentID, ReplyID: j.ReplyID, ResetStatus: j.ResetStatus, Actor: actor,
		CommentID: j.CommentID, Emoji: j.Emoji, By: j.By, Version: j.Version,
	}
}

func TestOpsGolden(t *testing.T) {
	dir := filepath.Join(goldenRoot(t), "ops")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		base, ok := stripSuffix(e.Name(), ".in.json")
		if !ok {
			continue
		}
		t.Run(base, func(t *testing.T) {
			var in opsIn
			if err := json.Unmarshal(readGolden(t, "ops", base+".in.json"), &in); err != nil {
				t.Fatal(err)
			}
			var oj opJSON
			if err := json.Unmarshal(in.Op, &oj); err != nil {
				t.Fatal(err)
			}
			list, res := ApplyCommentOp(in.List, toCommentOp(oj))
			folded := SnapshotList(list, oj.Version)
			out := map[string]any{
				"status": res.Status,
				"body":   res.Body,
				"wipe":   res.Wipe,
				"folded": folded,
			}
			assertJSONEqual(t, out, readGolden(t, "ops", base+".out.json"), base)
		})
	}
}

type reconcileIn struct {
	List    []Comment         `json:"list"`
	AIDs    []StampedArtifact `json:"aids"`
	Version int               `json:"version"`
}

func TestReconcileGolden(t *testing.T) {
	dir := filepath.Join(goldenRoot(t), "reconcile")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		base, ok := stripSuffix(e.Name(), ".in.json")
		if !ok {
			continue
		}
		t.Run(base, func(t *testing.T) {
			var in reconcileIn
			if err := json.Unmarshal(readGolden(t, "reconcile", base+".in.json"), &in); err != nil {
				t.Fatal(err)
			}
			ReconcileAnchors(in.List, in.AIDs, in.Version)
			folded := SnapshotList(in.List, in.Version)
			out := map[string]any{"folded": folded}
			assertJSONEqual(t, out, readGolden(t, "reconcile", base+".out.json"), base)
		})
	}
}
