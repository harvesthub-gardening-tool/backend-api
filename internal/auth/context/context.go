// Package authctx provides the AuthInfo type and context storage/retrieval helpers
// for the Harvest Hub auth system.
//
// AuthInfo is set by the JWT middleware after successful token validation and
// can be retrieved in any downstream handler via the helper functions.
package authctx

import "context"

// contextKey is an unexported type to prevent collisions with other packages.
type contextKey struct{}

var authKey = contextKey{}

// AuthInfo holds the identity extracted from a validated JWT token.
type AuthInfo struct {
	UserID   string
	Username string
}

// IsServiceAccount returns true when the token belongs to a Hub device (empty username).
func (a *AuthInfo) IsServiceAccount() bool {
	return a.Username == ""
}

// SetAuthInfo stores AuthInfo in the context. Called by the auth middleware.
func SetAuthInfo(ctx context.Context, info *AuthInfo) context.Context {
	return context.WithValue(ctx, authKey, info)
}

// GetUserID returns the authenticated user's ID, or empty string if not set.
func GetUserID(ctx context.Context) string {
	if info, ok := ctx.Value(authKey).(*AuthInfo); ok {
		return info.UserID
	}
	return ""
}

// GetUsername returns the authenticated user's email/username, or empty string.
// An empty string indicates a service account (Hub device).
func GetUsername(ctx context.Context) string {
	if info, ok := ctx.Value(authKey).(*AuthInfo); ok {
		return info.Username
	}
	return ""
}

// GetAuthInfo returns the full AuthInfo from context and a boolean indicating
// whether auth info was present.
func GetAuthInfo(ctx context.Context) (*AuthInfo, bool) {
	info, ok := ctx.Value(authKey).(*AuthInfo)
	return info, ok
}

// IsServiceAccount returns true if the authenticated entity is a Hub device
// (token has empty username), or false if no auth info is present.
func IsServiceAccount(ctx context.Context) bool {
	if info, ok := ctx.Value(authKey).(*AuthInfo); ok {
		return info.Username == ""
	}
	return false
}
