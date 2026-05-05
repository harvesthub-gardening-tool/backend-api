package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	authv2 "github.com/harvesthub-gardening-tool/protos-go/auth/v2"
	"harvest-hub/api/internal/auth"
	authctx "harvest-hub/api/internal/auth/context"
)

// AuthServiceV2 implements the QR-code hub provisioning flow defined in auth/v2.
type AuthServiceV2 struct {
	authService *auth.AuthService
}

// NewAuthServiceV2 creates a new v2 AuthService handler.
func NewAuthServiceV2(authService *auth.AuthService) *AuthServiceV2 {
	return &AuthServiceV2{authService: authService}
}

func (s *AuthServiceV2) Register(
	ctx context.Context,
	req *connect.Request[authv2.RegisterRequest],
) (*connect.Response[authv2.RegisterResponse], error) {
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

	token, err := s.authService.LoginUser(ctx, msg.Email, msg.Password)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to generate token: %w", err))
	}

	return connect.NewResponse(&authv2.RegisterResponse{
		UserId: userID,
		Token:  token,
	}), nil
}

func (s *AuthServiceV2) Login(
	ctx context.Context,
	req *connect.Request[authv2.LoginRequest],
) (*connect.Response[authv2.LoginResponse], error) {
	msg := req.Msg

	token, err := s.authService.LoginUser(ctx, msg.Email, msg.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			return nil, connect.NewError(connect.CodeUnauthenticated, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("login failed: %w", err))
	}

	return connect.NewResponse(&authv2.LoginResponse{Token: token}), nil
}

func (s *AuthServiceV2) AssociateHub(
	ctx context.Context,
	req *connect.Request[authv2.AssociateHubRequest],
) (*connect.Response[authv2.AssociateHubResponse], error) {
	userID := authctx.GetUserID(ctx)
	username := authctx.GetUsername(ctx)
	if userID == "" || username == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	msg := req.Msg
	hubID, err := s.authService.AssociateHub(ctx, userID, msg.DeviceId, msg.HubSecret, msg.HubName)
	if err != nil {
		if errors.Is(err, auth.ErrDeviceAlreadyAssociated) {
			return nil, connect.NewError(connect.CodeAlreadyExists, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to associate hub: %w", err))
	}

	return connect.NewResponse(&authv2.AssociateHubResponse{
		HubId:    hubID,
		DeviceId: msg.DeviceId,
		HubName:  msg.HubName,
	}), nil
}

func (s *AuthServiceV2) ClaimHubToken(
	ctx context.Context,
	req *connect.Request[authv2.ClaimHubTokenRequest],
) (*connect.Response[authv2.ClaimHubTokenResponse], error) {
	msg := req.Msg
	token, err := s.authService.ClaimHubToken(ctx, msg.DeviceId, msg.HubSecret)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidDeviceCredentials) {
			return nil, connect.NewError(connect.CodePermissionDenied, err)
		}
		if errors.Is(err, auth.ErrHubAlreadyClaimed) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to claim hub token: %w", err))
	}

	return connect.NewResponse(&authv2.ClaimHubTokenResponse{Token: token}), nil
}

func (s *AuthServiceV2) ListHubs(
	ctx context.Context,
	req *connect.Request[authv2.ListHubsRequest],
) (*connect.Response[authv2.ListHubsResponse], error) {
	userID := authctx.GetUserID(ctx)
	if userID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	hubs, err := s.authService.ListHubs(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list hubs: %w", err))
	}

	infos := make([]*authv2.HubInfo, len(hubs))
	for i, h := range hubs {
		infos[i] = &authv2.HubInfo{
			Id:        h.ID,
			DeviceId:  h.DeviceID,
			HubName:   h.Name,
			CreatedAt: h.CreatedAt.UnixMilli(),
			Claimed:   h.Claimed,
			Revoked:   h.Revoked,
		}
	}

	return connect.NewResponse(&authv2.ListHubsResponse{Hubs: infos}), nil
}

func (s *AuthServiceV2) RevokeHub(
	ctx context.Context,
	req *connect.Request[authv2.RevokeHubRequest],
) (*connect.Response[authv2.RevokeHubResponse], error) {
	userID := authctx.GetUserID(ctx)
	if userID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	msg := req.Msg
	if err := s.authService.RevokeHub(ctx, userID, msg.HubId); err != nil {
		if errors.Is(err, auth.ErrHubNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to revoke hub: %w", err))
	}

	return connect.NewResponse(&authv2.RevokeHubResponse{}), nil
}

func (s *AuthServiceV2) ChangeEmail(
	ctx context.Context,
	req *connect.Request[authv2.ChangeEmailRequest],
) (*connect.Response[authv2.ChangeEmailResponse], error) {
	userID := authctx.GetUserID(ctx)
	username := authctx.GetUsername(ctx)
	if userID == "" || username == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	msg := req.Msg
	token, err := s.authService.ChangeEmail(ctx, userID, msg.NewEmail, msg.CurrentPassword)
	if err != nil {
		if errors.Is(err, auth.ErrDuplicateEmail) {
			return nil, connect.NewError(connect.CodeAlreadyExists, err)
		}
		if errors.Is(err, auth.ErrInvalidEmail) || errors.Is(err, auth.ErrMissingRequiredField) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		if errors.Is(err, auth.ErrInvalidCredentials) {
			return nil, connect.NewError(connect.CodeUnauthenticated, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to change email: %w", err))
	}

	return connect.NewResponse(&authv2.ChangeEmailResponse{Token: token}), nil
}

func (s *AuthServiceV2) ChangePassword(
	ctx context.Context,
	req *connect.Request[authv2.ChangePasswordRequest],
) (*connect.Response[authv2.ChangePasswordResponse], error) {
	userID := authctx.GetUserID(ctx)
	username := authctx.GetUsername(ctx)
	if userID == "" || username == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	msg := req.Msg
	if err := s.authService.ChangePassword(ctx, userID, msg.CurrentPassword, msg.NewPassword); err != nil {
		if errors.Is(err, auth.ErrWeakPassword) || errors.Is(err, auth.ErrMissingRequiredField) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		if errors.Is(err, auth.ErrInvalidCredentials) {
			return nil, connect.NewError(connect.CodeUnauthenticated, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to change password: %w", err))
	}

	return connect.NewResponse(&authv2.ChangePasswordResponse{}), nil
}
