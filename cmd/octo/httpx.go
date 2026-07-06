package main

import (
	"io"
	"net/http"
	"strings"
	"time"
)

// httpPing is a short-timeout client for liveness checks against the remote
// octo-doc server (used by `octo doctor`).
var httpPing = &http.Client{Timeout: 2 * time.Second}

// pingService fetches a /v1/ping URL and returns its service marker, or "". The
// remote octo-doc server embeds "service":"octo-doc" in the body.
func pingService(url string) string {
	resp, err := httpPing.Get(url)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ""
	}
	if strings.Contains(string(b), `"service":"octo-doc"`) {
		return "octo-doc"
	}
	return ""
}

// nowISO returns the current UTC time as an ISO-8601 Z timestamp, matching the
// server's meta/comment timestamp format.
func nowISO() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }
