package service_test

import (
	"context"
	"testing"

	"github.com/lml2468/octo-doc/internal/config"
	"github.com/lml2468/octo-doc/internal/platform/sluglock"
	"github.com/lml2468/octo-doc/internal/service"
	"github.com/lml2468/octo-doc/internal/storage/memory"
)

// The session machinery is the seam a future Octo unified login plugs into.
// These tests exercise it end to end so it is verified-ready, not dead code:
// a provider authenticates, calls CreateSession, and the rest of the system
// resolves the identity generically.

func TestCreateSessionRoundTrip(t *testing.T) {
	store := memory.New()
	auth := service.NewAuthService(store, &config.Config{}, sluglock.NewMemory())
	ctx := context.Background()

	avatar := "https://example.com/a.png"
	sid, err := auth.CreateSession(ctx, "alice", "Alice", &avatar)
	if err != nil || sid == "" {
		t.Fatalf("CreateSession = %q, %v", sid, err)
	}

	got, err := auth.GetSession(ctx, sid)
	if err != nil || got == nil {
		t.Fatalf("GetSession = %v, %v", got, err)
	}
	if got.Login != "alice" || got.Name != "Alice" || got.AvatarURL == nil || *got.AvatarURL != avatar {
		t.Fatalf("session round-trip mismatch: %+v", got)
	}

	if err := auth.Logout(ctx, sid); err != nil {
		t.Fatal(err)
	}
	if got, _ := auth.GetSession(ctx, sid); got != nil {
		t.Fatal("session not cleared after logout")
	}
}

func TestCreateSessionDefaultsNameToLogin(t *testing.T) {
	store := memory.New()
	auth := service.NewAuthService(store, &config.Config{}, sluglock.NewMemory())
	sid, err := auth.CreateSession(context.Background(), "bob", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := auth.GetSession(context.Background(), sid)
	if got == nil || got.Name != "bob" {
		t.Fatalf("name should default to login, got %+v", got)
	}
}

func TestIsOwnerMatchesConfiguredOwner(t *testing.T) {
	store := memory.New()
	auth := service.NewAuthService(store, &config.Config{Owner: "Alice"}, sluglock.NewMemory())
	ctx := context.Background()

	sid, _ := auth.CreateSession(ctx, "alice", "Alice", nil) // case-insensitive
	owner, _ := auth.GetSession(ctx, sid)
	if !auth.IsOwner(owner) {
		t.Error("expected configured owner to be recognized")
	}

	sid2, _ := auth.CreateSession(ctx, "bob", "Bob", nil)
	other, _ := auth.GetSession(ctx, sid2)
	if auth.IsOwner(other) {
		t.Error("non-owner must not be owner")
	}
	if auth.IsOwner(nil) {
		t.Error("anonymous (nil) must not be owner")
	}
}

func TestLoginDisabledByDefault(t *testing.T) {
	auth := service.NewAuthService(memory.New(), &config.Config{}, sluglock.NewMemory())
	if auth.LoginEnabled() {
		t.Error("no login provider is configured; LoginEnabled must be false")
	}
}
