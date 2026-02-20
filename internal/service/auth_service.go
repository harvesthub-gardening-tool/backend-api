package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"harvest-hub/api/internal/auth"
	authctx "harvest-hub/api/internal/auth/context"
)

// AuthService handles authentication and hub token management endpoints.
type AuthService struct {
	authService *auth.AuthService
}

// NewAuthService creates a new AuthService with the provided auth service.
func NewAuthService(authService *auth.AuthService) *AuthService {
	return &AuthService{
		authService: authService,
	}
}

// Register creates a new user account and returns a JWT token.
// Returns:
//   - user_id: The unique identifier for the created user
//   - token: JWT token with 24-hour expiry for immediate login
func (s *AuthService) Register(
	ctx context.Context,
	req *connect.Request[RegisterRequest],
) (*connect.Response[RegisterResponse], error) {
	msg := req.Msg

	userID, err := s.authService.RegisterUser(ctx, msg.Email, msg.Password)
	if err != nil {
		if errors.Is(err, auth.ErrDuplicateEmail) {
			return nil, connect.NewError(connect.CodeAlreadyExists, err)
		}
		if errors.Is(err, auth.ErrInvalidEmail) || errors.Is(err, auth.ErrWeakPassword) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("registration failed: %w", err))
	}

	// Generate JWT token for immediate login (user token with username)
	token, err := s.authService.LoginUser(ctx, msg.Email, msg.Password)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to generate token: %w", err))
	}

	return connect.NewResponse(&RegisterResponse{
		UserId: userID,
		Token:  token,
	}), nil
}

// Login validates user credentials and returns a JWT token.
// Returns:
//   - token: JWT token with 24-hour expiry
func (s *AuthService) Login(
	ctx context.Context,
	req *connect.Request[LoginRequest],
) (*connect.Response[LoginResponse], error) {
	msg := req.Msg

	token, err := s.authService.LoginUser(ctx, msg.Email, msg.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			return nil, connect.NewError(connect.CodeUnauthenticated, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("login failed: %w", err))
	}

	return connect.NewResponse(&LoginResponse{
		Token: token,
	}), nil
}

// CreateHubToken creates a new hub device token tied to the authenticated user.
// Requires authentication.
// Returns:
//   - token: JWT service account token with 1-year expiry (shown only once)
func (s *AuthService) CreateHubToken(
	ctx context.Context,
	req *connect.Request[CreateHubTokenRequest],
) (*connect.Response[CreateHubTokenResponse], error) {
	userID := authctx.GetUserID(ctx)
	if userID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	msg := req.Msg

	token, err := s.authService.CreateHubToken(ctx, userID, msg.HubName)
	if err != nil {
		if errors.Is(err, auth.ErrDuplicateHubName) {
			return nil, connect.NewError(connect.CodeAlreadyExists, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create hub token: %w", err))
	}

	return connect.NewResponse(&CreateHubTokenResponse{
		Token: token,
	}), nil
}

// ListHubTokens returns all active hub tokens for the authenticated user.
// Requires authentication. Excludes revoked tokens.
// Returns:
//   - tokens: Array of hub token metadata (id, hub_name, created_at, revoked)
func (s *AuthService) ListHubTokens(
	ctx context.Context,
	req *connect.Request[ListHubTokensRequest],
) (*connect.Response[ListHubTokensResponse], error) {
	userID := authctx.GetUserID(ctx)
	if userID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	tokens, err := s.authService.ListHubTokens(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list hub tokens: %w", err))
	}

	tokenInfos := make([]*HubTokenInfo, len(tokens))
	for i, token := range tokens {
		tokenInfos[i] = &HubTokenInfo{
			Id:        token.ID,
			HubName:   token.HubName,
			CreatedAt: token.CreatedAt.UnixMilli(),
			Revoked:   token.Revoked,
		}
	}

	return connect.NewResponse(&ListHubTokensResponse{
		Tokens: tokenInfos,
	}), nil
}

// RevokeHubToken marks a hub token as revoked.
// Requires authentication. Revoked tokens remain in the database but are excluded from listings.
// After revocation, the same hub name can be reused.
func (s *AuthService) RevokeHubToken(
	ctx context.Context,
	req *connect.Request[RevokeHubTokenRequest],
) (*connect.Response[RevokeHubTokenResponse], error) {
	userID := authctx.GetUserID(ctx)
	if userID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	msg := req.Msg

	err := s.authService.RevokeHubToken(ctx, userID, msg.TokenId)
	if err != nil {
		if errors.Is(err, auth.ErrHubTokenNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to revoke hub token: %w", err))
	}

	return connect.NewResponse(&RevokeHubTokenResponse{}), nil
}
