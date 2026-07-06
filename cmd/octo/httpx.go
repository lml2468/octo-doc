package main

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// httpPing is a short-timeout client for liveness checks against the local
// preview server and remote octo-doc servers.
var httpPing = &http.Client{Timeout: 2 * time.Second}

// portAnswers reports whether anything answers HTTP on the local preview port.
func portAnswers(p int) bool {
	resp, err := httpPing.Get(localURL(p, "/v1/ping"))
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// pingIsOurs reports whether the local preview port is answered by an octo
// preview server (identity marker "service":"octo"). A foreign process squatting
// the port answers 200 but won't carry the marker.
func pingIsOurs(p int) bool {
	return pingService(localURL(p, "/v1/ping")) == "octo"
}

// pingService fetches a /v1/ping URL and returns its service marker, or "".
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
	// Cheap marker scan avoids a struct just to read one field; both the local
	// ("octo") and remote ("octo-doc") servers embed "service":"…" in the body.
	s := string(b)
	if strings.Contains(s, `"service":"octo-doc"`) {
		return "octo-doc"
	}
	if strings.Contains(s, `"service":"octo"`) {
		return "octo"
	}
	return ""
}

// localURL builds a loopback URL for the preview server.
func localURL(p int, path string) string {
	return "http://localhost:" + strconv.Itoa(p) + path
}
