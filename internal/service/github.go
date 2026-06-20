package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Mininglamp-OSS/octo-doc/internal/platform/apperr"
)

const ghUserAgent = "octo-doc"

// DeviceStart is the device-flow start response surfaced to the client.
type DeviceStart struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// GhUser is the subset of a GitHub profile we persist.
type GhUser struct {
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

// githubClient wraps the GitHub device-flow + /user endpoints.
type githubClient struct {
	http    *http.Client
	apiBase string // https://api.github.com
	webBase string // https://github.com
}

func newGitHubClient(hc *http.Client) *githubClient {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &githubClient{http: hc, apiBase: "https://api.github.com", webBase: "https://github.com"}
}

func (g *githubClient) postForm(ctx context.Context, path string, form url.Values) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.webBase+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, apperr.Upstream("github request build failed", "github_unreachable", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", ghUserAgent)
	res, err := g.http.Do(req)
	if err != nil {
		return nil, apperr.Upstream("github unreachable", "github_unreachable", err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	ct := res.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			// values may be non-strings; fall back to a generic decode
			var generic map[string]any
			if err2 := json.Unmarshal(raw, &generic); err2 != nil {
				return nil, apperr.Upstream("github returned unparseable JSON", "gh_parse", err)
			}
			m = map[string]string{}
			for k, v := range generic {
				m[k] = toStr(v)
			}
		}
		return m, nil
	}
	parsed, err := url.ParseQuery(string(raw))
	if err != nil {
		return nil, apperr.Upstream("github returned unparseable body", "gh_parse", err)
	}
	m := map[string]string{}
	for k := range parsed {
		m[k] = parsed.Get(k)
	}
	return m, nil
}

func toStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return ""
	}
}

func (g *githubClient) startDeviceFlow(ctx context.Context, clientID string) (*DeviceStart, error) {
	r, err := g.postForm(ctx, "/login/device/code", url.Values{
		"client_id": {clientID}, "scope": {"read:user"},
	})
	if err != nil {
		return nil, err
	}
	if e := r["error"]; e != "" {
		return nil, apperr.Validation(orStr(r["error_description"], e), e)
	}
	return &DeviceStart{
		DeviceCode:      r["device_code"],
		UserCode:        r["user_code"],
		VerificationURI: r["verification_uri"],
		ExpiresIn:       atoiSafe(r["expires_in"]),
		Interval:        atoiSafe(r["interval"]),
	}, nil
}

// pollResult: pending==true means keep polling.
type pollResult struct {
	pending     bool
	accessToken string
}

func (g *githubClient) pollAccessToken(ctx context.Context, clientID, deviceCode string) (pollResult, error) {
	r, err := g.postForm(ctx, "/login/oauth/access_token", url.Values{
		"client_id":   {clientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	})
	if err != nil {
		return pollResult{}, err
	}
	switch r["error"] {
	case "authorization_pending", "slow_down":
		return pollResult{pending: true}, nil
	case "":
		// no error
	default:
		return pollResult{}, apperr.Validation(orStr(r["error_description"], r["error"]), r["error"])
	}
	if r["access_token"] == "" {
		return pollResult{pending: true}, nil
	}
	return pollResult{pending: false, accessToken: r["access_token"]}, nil
}

func (g *githubClient) fetchUser(ctx context.Context, token string) (*GhUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.apiBase+"/user", nil)
	if err != nil {
		return nil, apperr.Upstream("github request build failed", "github_unreachable", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", ghUserAgent)
	res, err := g.http.Do(req)
	if err != nil {
		return nil, apperr.Upstream("github unreachable", "github_unreachable", err)
	}
	defer func() { _ = res.Body.Close() }()
	var u GhUser
	if err := json.NewDecoder(res.Body).Decode(&u); err != nil {
		return nil, apperr.Upstream("github returned unparseable user", "gh_parse", err)
	}
	return &u, nil
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
