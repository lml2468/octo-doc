package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-doc/internal/core"
)

// TestClientCommentAndReact confirms the new comment/react client methods hit the
// right endpoints with the right bodies and unwrap the envelope — the CLI paths
// the demo seed now depends on instead of raw curl.
func TestClientCommentAndReact(t *testing.T) {
	var gotComment, gotReact map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/v1/comments":
			_ = json.Unmarshal(body, &gotComment)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"id":"c_test_1234"}}`)
		case "/v1/reactions":
			_ = json.Unmarshal(body, &gotReact)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"ok":true}}`)
		default:
			http.Error(w, "unexpected path "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()

	cl := newClient(srv.URL, "")
	ctx := context.Background()

	// Top-level anchored comment.
	id, err := cl.createComment(ctx, commentReq{
		Slug: "doc", Text: "hi", Version: 2,
		Anchor: &core.Anchor{Kind: "text", Text: "some phrase"},
	})
	if err != nil {
		t.Fatalf("createComment: %v", err)
	}
	if id != "c_test_1234" {
		t.Errorf("id = %q, want c_test_1234", id)
	}
	if gotComment["slug"] != "doc" || gotComment["text"] != "hi" {
		t.Errorf("comment body = %+v", gotComment)
	}
	if a, ok := gotComment["anchor"].(map[string]any); !ok || a["kind"] != "text" || a["text"] != "some phrase" {
		t.Errorf("anchor not sent correctly: %+v", gotComment["anchor"])
	}

	// Reaction.
	if err := cl.react(ctx, "doc", "c_test_1234", "👍", 2); err != nil {
		t.Fatalf("react: %v", err)
	}
	if gotReact["comment_id"] != "c_test_1234" || gotReact["emoji"] != "👍" {
		t.Errorf("react body = %+v", gotReact)
	}
}
