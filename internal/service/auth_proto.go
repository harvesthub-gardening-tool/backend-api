package service

// Auth RPC message types (placeholders)
// TODO: Replace with generated proto code from protos repo
//
// These types follow Connect RPC patterns and match the expected API contract.
// Once proto definitions are added to the protos repo and published to protos-go,
// these placeholder types should be replaced with the generated code.

// RegisterRequest is the request to register a new user account.
type RegisterRequest struct {
	Email    string
	Password string
}

// RegisterResponse is the response after successful registration.
type RegisterResponse struct {
	UserId string
	Token  string
}

// LoginRequest is the request to log in an existing user.
type LoginRequest struct {
	Email    string
	Password string
}

// LoginResponse is the response after successful login.
type LoginResponse struct {
	Token string
}

// CreateHubTokenRequest is the request to create a new hub device token.
type CreateHubTokenRequest struct {
	HubName string
}

// CreateHubTokenResponse is the response after creating a hub token.
type CreateHubTokenResponse struct {
	Token string
}

// ListHubTokensRequest is the request to list all hub tokens for the authenticated user.
type ListHubTokensRequest struct{}

// HubTokenInfo represents metadata about a hub token (excludes the token value).
type HubTokenInfo struct {
	Id        string
	HubName   string
	CreatedAt int64 // Unix milliseconds
	Revoked   bool
}

// ListHubTokensResponse is the response containing all hub tokens.
type ListHubTokensResponse struct {
	Tokens []*HubTokenInfo
}

// RevokeHubTokenRequest is the request to revoke a hub token.
type RevokeHubTokenRequest struct {
	TokenId string
}

// RevokeHubTokenResponse is the response after revoking a hub token.
type RevokeHubTokenResponse struct{}
