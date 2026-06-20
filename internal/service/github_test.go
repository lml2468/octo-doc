package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeGitHub serves canned device-flow + user responses.
func fakeGitHub(t *testing.T, handler http.HandlerFunc) *githubClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &githubClient{http: srv.Client(), apiBase: srv.URL, webBase: srv.URL}
}

func TestStartDeviceFlow(t *testing.T) {
	g := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/device/code" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"UC-1","verification_uri":"https://gh/device","expires_in":900,"interval":5}`))
	})
	ds, err := g.startDeviceFlow(context.Background(), "client-id")
	if err != nil {
		t.Fatal(err)
	}
	if ds.DeviceCode != "dc" || ds.UserCode != "UC-1" || ds.Interval != 5 {
		t.Fatalf("device start = %+v", ds)
	}
}

func TestPollAccessTokenPendingThenSuccess(t *testing.T) {
	state := 0
	g := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if state == 0 {
			state++
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tok-123"}`))
	})
	ctx := context.Background()
	if r, err := g.pollAccessToken(ctx, "c", "dc"); err != nil || !r.pending {
		t.Fatalf("first poll = %+v, %v; want pending", r, err)
	}
	r, err := g.pollAccessToken(ctx, "c", "dc")
	if err != nil || r.pending || r.accessToken != "tok-123" {
		t.Fatalf("second poll = %+v, %v", r, err)
	}
}

func TestPollAccessTokenError(t *testing.T) {
	g := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":"access_denied","error_description":"user denied"}`))
	})
	if _, err := g.pollAccessToken(context.Background(), "c", "dc"); err == nil {
		t.Fatal("access_denied should surface as an error")
	}
}

func TestFetchUser(t *testing.T) {
	g := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"octocat","name":"The Octocat","avatar_url":"https://gh/a.png"}`))
	})
	u, err := g.fetchUser(context.Background(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	if u.Login != "octocat" || u.Name != "The Octocat" {
		t.Fatalf("user = %+v", u)
	}
}

func TestFormEncodedFallback(t *testing.T) {
	// GitHub sometimes returns x-www-form-urlencoded; the client must parse it.
	g := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		_, _ = w.Write([]byte("access_token=form-tok&token_type=bearer"))
	})
	r, err := g.pollAccessToken(context.Background(), "c", "dc")
	if err != nil || r.accessToken != "form-tok" {
		t.Fatalf("form-encoded poll = %+v, %v", r, err)
	}
}
