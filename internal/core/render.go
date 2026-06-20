package core

import (
	"encoding/json"
	"strings"
)

// HTML helpers and overlay injection, ported from render.ts. The browser overlay
// is injected before </body>; the bytes reaching the browser are identical to the
// upstream worker's build-time inlining.

// OverlayIdentity is the minimal identity the overlay renders in its toolbar.
type OverlayIdentity struct {
	Login     string  `json:"login"`
	AvatarURL *string `json:"avatar_url,omitempty"`
	Name      string  `json:"name,omitempty"`
}

// OverlayConfig is the boot config injected as window.__TDOC__ for the overlay.
type OverlayConfig struct {
	Slug           string           `json:"slug"`
	Version        int              `json:"version"`
	Identity       *OverlayIdentity `json:"identity"`
	Mode           string           `json:"mode"`
	AuthConfigured bool             `json:"authConfigured"`
	IsOwner        bool             `json:"isOwner,omitempty"`
	Versions       []VersionRef     `json:"versions,omitempty"`
	OriginalSlug   string           `json:"originalSlug,omitempty"`
}

// VersionRef is one entry in the overlay's version picker.
type VersionRef struct {
	N       int     `json:"n"`
	Created *string `json:"created,omitempty"`
}

// SafeJSONForScript escapes </script> and HTML-comment openers so JSON can't
// break out of a <script> element. It uses an encoder with HTML escaping
// DISABLED so the byte output matches JavaScript's JSON.stringify (Go's default
// escapes <, >, & to \u00XX, which JS does not).
func SafeJSONForScript(v any) (string, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	s := strings.TrimRight(buf.String(), "\n") // Encoder appends a newline
	s = strings.ReplaceAll(s, "</script>", `<\/script>`)
	s = strings.ReplaceAll(s, "<!--", `<\!--`)
	return s, nil
}

var htmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&#39;",
)

// EscapeHTML escapes a string for interpolation into markup.
func EscapeHTML(s string) string {
	return htmlEscaper.Replace(s)
}

// ForHTMLComment neutralizes -- so an untrusted string can't terminate an HTML comment.
func ForHTMLComment(s string) string {
	return strings.ReplaceAll(s, "--", `-\-`)
}

// InjectOverlayCfg injects the overlay boot script + config before </body>.
func InjectOverlayCfg(rawHTML, overlayJS string, cfg OverlayConfig) (string, error) {
	cfgJSON, err := SafeJSONForScript(cfg)
	if err != nil {
		return "", err
	}
	inject := "<script>window.__TDOC__ = " + cfgJSON + ";</script>\n<script>" + overlayJS + "</script>"
	if strings.Contains(rawHTML, "</body>") {
		return strings.Replace(rawHTML, "</body>", inject+"\n</body>", 1), nil
	}
	return rawHTML + inject, nil
}
