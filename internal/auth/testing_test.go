package auth

import (
	"testing"

	authctx "harvest-hub/api/internal/auth/context"
)

func TestNewTestGORMDB(t *testing.T) {
	db := NewTestGORMDB(t)

	if !db.Migrator().HasTable(&User{}) {
		t.Fatal("expected auth_users table to exist")
	}
	if !db.Migrator().HasTable(&HubToken{}) {
		t.Fatal("expected hub_tokens table to exist")
	}
}

func TestCreateTestAuthContext(t *testing.T) {
	ctx := CreateTestAuthContext("42", "alice@example.com")

	if got := authctx.GetUserID(ctx); got != "42" {
		t.Fatalf("GetUserID() = %q, want %q", got, "42")
	}
	if got := authctx.GetUsername(ctx); got != "alice@example.com" {
		t.Fatalf("GetUsername() = %q, want %q", got, "alice@example.com")
	}
	if authctx.IsServiceAccount(ctx) {
		t.Fatal("expected test auth context to represent a user")
	}
}
