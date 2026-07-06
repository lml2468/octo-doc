package main

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// requireServer resolves config and returns a client, requiring a base URL (and,
// when needWrite is set, a token). It also persists the resolved config so the
// next run needs no env, matching the bash tools' behavior.
func requireServer(cfg config, needWrite bool) (*client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New(`no octo-doc server configured.

Set the server to publish to:
    export OCTO_BASE_URL="https://your-host"   # or http://localhost:8080
    export OCTO_TOKEN="<write token>"          # from: octo-doc bootstrap

To mint a token from a fresh server (only when it has no static WRITE_TOKEN):
    curl -sS -X POST "$OCTO_BASE_URL/v1/admin/bootstrap" | jq -r .data.token`)
	}
	if needWrite && cfg.Token == "" {
		return nil, fmt.Errorf("no write token. Set OCTO_TOKEN or add it to ~/.octo/config.json\n"+
			"       Get one with: curl -sS -X POST %q/v1/admin/bootstrap | jq -r .data.token", cfg.BaseURL)
	}
	// Persist for next time (best-effort; a failure here is non-fatal).
	if needWrite {
		_ = saveConfig(cfg.BaseURL, cfg.Token)
	}
	return newClient(cfg.BaseURL, cfg.Token), nil
}

// cmdPublish uploads a local doc's versions to the configured server. It uploads
// every version (oldest first, latest last) so the full history is preserved and
// the printed URL always points at the freshest version. Older-version failures
// are warnings; a latest-version failure is fatal.
func cmdPublish(args []string) error {
	if len(args) < 1 || args[0] == "" {
		return errors.New("usage: octo publish <slug>")
	}
	slug := args[0]
	cfg := loadConfig()
	st := newStore(cfg.Dir)
	if !st.exists(slug) {
		return fmt.Errorf("no local doc at %s. Create it with `octo new`", st.slugDir(slug))
	}
	cl, err := requireServer(cfg, true)
	if err != nil {
		return err
	}
	meta, err := st.readMeta(slug)
	if err != nil {
		return err
	}
	comments, err := st.readComments(slug)
	if err != nil {
		return err
	}

	versions := meta.sortedVersions()
	latest := meta.latestVersion()
	ctx := context.Background()

	fmt.Fprintf(os.Stderr, "[octo] publishing %s to %s\n", slug, cfg.BaseURL)
	pm := &publishMeta{Title: meta.Title, Versions: meta.Versions}

	var olderFailed []int
	var lastResp *publishResp
	for _, vr := range versions {
		resp, upErr := st.uploadVersion(ctx, cl, slug, vr.N, pm, comments)
		if vr.N == latest {
			if upErr != nil {
				return fmt.Errorf("latest v%d failed: %w — aborting", latest, upErr)
			}
			lastResp = resp
			continue
		}
		if upErr != nil {
			olderFailed = append(olderFailed, vr.N)
		}
	}
	if len(olderFailed) > 0 {
		fmt.Fprintf(os.Stderr, "[octo] WARNING: older version(s) failed: %v — re-run to retry.\n", olderFailed)
	}
	url := fmt.Sprintf("%s/d/%s/v/%d", cfg.BaseURL, slug, latest)
	if lastResp != nil && lastResp.URL != "" {
		url = lastResp.URL
	}
	fmt.Printf("\nPublished: %s\n", url)
	return nil
}

// uploadVersion sends one version's HTML (+ meta + comments) to POST /v1/docs.
// Comments are re-sent every time; the server merges them idempotently by id.
func (s *store) uploadVersion(ctx context.Context, cl *client, slug string, v int, pm *publishMeta, comments []comment) (*publishResp, error) {
	html, err := os.ReadFile(s.htmlPath(slug, v))
	if err != nil {
		return nil, fmt.Errorf("v%d html missing: %w", v, err)
	}
	req := publishReq{Slug: slug, Version: v, HTML: string(html), Meta: pm}
	if len(comments) > 0 {
		req.Comments = comments
	}
	resp, err := cl.publish(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.MergedComments > 0 {
		fmt.Fprintf(os.Stderr, "[octo] v%d uploaded (%d bytes, +%d comment(s) merged)\n", v, resp.Size, resp.MergedComments)
	} else {
		fmt.Fprintf(os.Stderr, "[octo] v%d uploaded (%d bytes)\n", v, resp.Size)
	}
	return resp, nil
}
