package authctx_test

import (
	"context"
	"fmt"
	"testing"

	authctx "harvest-hub/api/internal/auth/context"
)

func TestGetUserID(t *testing.T) {
	t.Run("returns user ID from context", func(t *testing.T) {
		info := &authctx.AuthInfo{UserID: "user-123", Username: "test@example.com"}
		ctx := authctx.SetAuthInfo(context.Background(), info)
		if got := authctx.GetUserID(ctx); got != "user-123" {
			t.Errorf("GetUserID() = %q, want %q", got, "user-123")
		}
	})

	t.Run("returns empty string when not set", func(t *testing.T) {
		if got := authctx.GetUserID(context.Background()); got != "" {
			t.Errorf("GetUserID() = %q, want empty", got)
		}
	})
}

func TestGetUsername(t *testing.T) {
	t.Run("returns username from context", func(t *testing.T) {
		info := &authctx.AuthInfo{UserID: "user-123", Username: "test@example.com"}
		ctx := authctx.SetAuthInfo(context.Background(), info)
		if got := authctx.GetUsername(ctx); got != "test@example.com" {
			t.Errorf("GetUsername() = %q, want %q", got, "test@example.com")
		}
	})

	t.Run("returns empty for service account", func(t *testing.T) {
		info := &authctx.AuthInfo{UserID: "hub-456", Username: ""}
		ctx := authctx.SetAuthInfo(context.Background(), info)
		if got := authctx.GetUsername(ctx); got != "" {
			t.Errorf("GetUsername() = %q, want empty", got)
		}
	})

	t.Run("returns empty when not set", func(t *testing.T) {
		if got := authctx.GetUsername(context.Background()); got != "" {
			t.Errorf("GetUsername() = %q, want empty", got)
		}
	})
}

func TestGetAuthInfo(t *testing.T) {
	t.Run("returns info from context", func(t *testing.T) {
		info := &authctx.AuthInfo{UserID: "user-123", Username: "test@example.com"}
		ctx := authctx.SetAuthInfo(context.Background(), info)
		got, ok := authctx.GetAuthInfo(ctx)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if got.UserID != "user-123" || got.Username != "test@example.com" {
			t.Errorf("unexpected info: %+v", got)
		}
	})

	t.Run("returns false when not set", func(t *testing.T) {
		_, ok := authctx.GetAuthInfo(context.Background())
		if ok {
			t.Error("expected ok=false")
		}
	})

	t.Run("returns false when auth info is explicitly nil", func(t *testing.T) {
		ctx := authctx.SetAuthInfo(context.Background(), nil)
		got, ok := authctx.GetAuthInfo(ctx)
		if ok {
			t.Error("expected ok=false")
		}
		if got != nil {
			t.Errorf("expected nil info, got %+v", got)
		}
	})
}

func TestIsServiceAccount(t *testing.T) {
	t.Run("true for empty username", func(t *testing.T) {
		ctx := authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{UserID: "hub-1"})
		if !authctx.IsServiceAccount(ctx) {
			t.Error("expected true for service account")
		}
	})

	t.Run("false for user with username", func(t *testing.T) {
		ctx := authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{UserID: "u-1", Username: "alice"})
		if authctx.IsServiceAccount(ctx) {
			t.Error("expected false for regular user")
		}
	})

	t.Run("false when not set", func(t *testing.T) {
		if authctx.IsServiceAccount(context.Background()) {
			t.Error("expected false when context has no auth info")
		}
	})
}

func TestAuthInfo_IsServiceAccount(t *testing.T) {
	t.Run("true for empty username", func(t *testing.T) {
		info := &authctx.AuthInfo{UserID: "hub-1", Username: ""}
		if !info.IsServiceAccount() {
			t.Error("expected true")
		}
	})

	t.Run("false for non-empty username", func(t *testing.T) {
		info := &authctx.AuthInfo{UserID: "u-1", Username: "alice"}
		if info.IsServiceAccount() {
			t.Error("expected false")
		}
	})
}

func TestGetHubID(t *testing.T) {
	t.Run("returns hub ID from context", func(t *testing.T) {
		ctx := authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{
			UserID:   "user-123",
			Username: "",
			HubID:    "hub-42",
		})

		if got := authctx.GetHubID(ctx); got != "hub-42" {
			t.Errorf("GetHubID() = %q, want %q", got, "hub-42")
		}
	})

	t.Run("returns empty when not set", func(t *testing.T) {
		if got := authctx.GetHubID(context.Background()); got != "" {
			t.Errorf("GetHubID() = %q, want empty", got)
		}
	})
}

func TestAccessors_NilAuthInfoDoesNotPanic(t *testing.T) {
	ctx := authctx.SetAuthInfo(context.Background(), nil)

	assertNoPanic(t, func() {
		if got := authctx.GetUserID(ctx); got != "" {
			t.Errorf("GetUserID() = %q, want empty", got)
		}
	})

	assertNoPanic(t, func() {
		if got := authctx.GetUsername(ctx); got != "" {
			t.Errorf("GetUsername() = %q, want empty", got)
		}
	})

	assertNoPanic(t, func() {
		if got := authctx.GetHubID(ctx); got != "" {
			t.Errorf("GetHubID() = %q, want empty", got)
		}
	})

	assertNoPanic(t, func() {
		if authctx.IsServiceAccount(ctx) {
			t.Error("IsServiceAccount() = true, want false")
		}
	})
}

func assertNoPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", fmt.Sprint(r))
		}
	}()
	fn()
}
