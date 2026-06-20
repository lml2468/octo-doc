package httpx

import (
	"encoding/json"

	"github.com/Mininglamp-OSS/octo-doc/internal/core"
	"github.com/Mininglamp-OSS/octo-doc/internal/service"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage"
)

// mergeOK marshals a publish result with an {ok:true, ...} envelope, matching the
// upstream response shape ({ ok, slug, version, url, size, aids, mergedComments }).
func mergeOK(res *service.PublishResult) map[string]any {
	raw, _ := json.Marshal(res)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["ok"] = true
	return m
}

// identityFromSession builds the overlay identity from a viewer session, or nil.
func identityFromSession(session *storage.Session) *core.OverlayIdentity {
	if session == nil {
		return nil
	}
	id := &core.OverlayIdentity{Login: session.Login}
	if session.AvatarURL != nil {
		id.AvatarURL = session.AvatarURL
	}
	if session.Name != "" {
		id.Name = session.Name
	}
	return id
}

// authorFromSession builds a comment Author from a viewer session, or nil in
// anonymous (local) mode.
func authorFromSession(session *storage.Session) *core.Author {
	if session == nil {
		return nil
	}
	a := &core.Author{Login: session.Login}
	if session.AvatarURL != nil {
		a.AvatarURL = session.AvatarURL
	}
	if session.Name != "" {
		a.Name = session.Name
	}
	return a
}
