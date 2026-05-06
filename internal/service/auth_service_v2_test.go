package service_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	authv2 "github.com/harvesthub-gardening-tool/protos-go/auth/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"harvest-hub/api/internal/auth"
	authctx "harvest-hub/api/internal/auth/context"
	authjwt "harvest-hub/api/internal/auth/jwt"
	"harvest-hub/api/internal/service"
)

func requireConnectCode(t *testing.T, err error, code connect.Code) {
	t.Helper()

	connectErr := new(connect.Error)
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, code, connectErr.Code())
}

func setupAuthServiceV2(t *testing.T) (*service.AuthServiceV2, *auth.AuthService) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&auth.User{}, &auth.HubToken{}, &auth.Hub{}))

	jwtManager, err := authjwt.NewJWTManager()
	require.NoError(t, err)

	authService := auth.NewAuthService(db, jwtManager)
	return service.NewAuthServiceV2(authService), authService
}

func authUserCtx(userID string) context.Context {
	return authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{
		UserID:   userID,
		Username: "user@example.com",
	})
}

func TestAuthServiceV2_ChangeEmail(t *testing.T) {
	handler, authService := setupAuthServiceV2(t)
	userID, err := authService.RegisterUser(context.Background(), "old@example.com", "password123")
	require.NoError(t, err)

	res, err := handler.ChangeEmail(authUserCtx(userID), connect.NewRequest(&authv2.ChangeEmailRequest{
		NewEmail:        "new@example.com",
		CurrentPassword: "password123",
	}))
	require.NoError(t, err)
	assert.NotEmpty(t, res.Msg.Token)

	_, err = authService.LoginUser(context.Background(), "new@example.com", "password123")
	assert.NoError(t, err)
}

func TestAuthServiceV2_ChangeEmailRequiresUserAuth(t *testing.T) {
	handler, authService := setupAuthServiceV2(t)
	userID, err := authService.RegisterUser(context.Background(), "hub-token@example.com", "password123")
	require.NoError(t, err)

	_, err = handler.ChangeEmail(context.Background(), connect.NewRequest(&authv2.ChangeEmailRequest{}))
	connectErr := new(connect.Error)
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeUnauthenticated, connectErr.Code())

	hubCtx := authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{UserID: userID})
	_, err = handler.ChangeEmail(hubCtx, connect.NewRequest(&authv2.ChangeEmailRequest{}))
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeUnauthenticated, connectErr.Code())
}

func TestAuthServiceV2_ChangeEmailMapsMissingFields(t *testing.T) {
	handler, authService := setupAuthServiceV2(t)
	userID, err := authService.RegisterUser(context.Background(), "missing-email@example.com", "password123")
	require.NoError(t, err)

	_, err = handler.ChangeEmail(authUserCtx(userID), connect.NewRequest(&authv2.ChangeEmailRequest{
		CurrentPassword: "password123",
	}))
	connectErr := new(connect.Error)
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())

	_, err = handler.ChangeEmail(authUserCtx(userID), connect.NewRequest(&authv2.ChangeEmailRequest{
		NewEmail: "new-missing@example.com",
	}))
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
}

func TestAuthServiceV2_ChangePassword(t *testing.T) {
	handler, authService := setupAuthServiceV2(t)
	userID, err := authService.RegisterUser(context.Background(), "pass@example.com", "password123")
	require.NoError(t, err)

	_, err = handler.ChangePassword(authUserCtx(userID), connect.NewRequest(&authv2.ChangePasswordRequest{
		CurrentPassword: "password123",
		NewPassword:     "newpass123",
	}))
	require.NoError(t, err)

	_, err = authService.LoginUser(context.Background(), "pass@example.com", "newpass123")
	assert.NoError(t, err)
}

func TestAuthServiceV2_ChangePasswordMapsWeakPassword(t *testing.T) {
	handler, authService := setupAuthServiceV2(t)
	userID, err := authService.RegisterUser(context.Background(), "weak@example.com", "password123")
	require.NoError(t, err)

	_, err = handler.ChangePassword(authUserCtx(userID), connect.NewRequest(&authv2.ChangePasswordRequest{
		CurrentPassword: "password123",
		NewPassword:     "short",
	}))
	connectErr := new(connect.Error)
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
}

func TestAuthServiceV2_ChangePasswordMapsMissingFields(t *testing.T) {
	handler, authService := setupAuthServiceV2(t)
	userID, err := authService.RegisterUser(context.Background(), "missing-pass@example.com", "password123")
	require.NoError(t, err)

	tests := []*authv2.ChangePasswordRequest{
		{NewPassword: "newpass123"},
		{CurrentPassword: "password123"},
	}
	for _, req := range tests {
		_, err = handler.ChangePassword(authUserCtx(userID), connect.NewRequest(req))
		connectErr := new(connect.Error)
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
	}
}

func TestAuthServiceV2_Register(t *testing.T) {
	handler, authService := setupAuthServiceV2(t)

	t.Run("registers user and returns token", func(t *testing.T) {
		res, err := handler.Register(context.Background(), connect.NewRequest(&authv2.RegisterRequest{
			Email:    "register-success@example.com",
			Password: "password123",
		}))
		require.NoError(t, err)
		assert.NotEmpty(t, res.Msg.UserId)
		assert.NotEmpty(t, res.Msg.Token)

		_, err = authService.LoginUser(context.Background(), "register-success@example.com", "password123")
		assert.NoError(t, err)
	})

	t.Run("maps duplicate email to already exists", func(t *testing.T) {
		_, err := authService.RegisterUser(context.Background(), "register-dup@example.com", "password123")
		require.NoError(t, err)

		_, err = handler.Register(context.Background(), connect.NewRequest(&authv2.RegisterRequest{
			Email:    "register-dup@example.com",
			Password: "password123",
		}))
		requireConnectCode(t, err, connect.CodeAlreadyExists)
	})

	t.Run("maps invalid inputs to invalid argument", func(t *testing.T) {
		cases := []*authv2.RegisterRequest{
			{Email: "invalid-email", Password: "password123"},
			{Email: "weak-password@example.com", Password: "short"},
		}

		for _, req := range cases {
			_, err := handler.Register(context.Background(), connect.NewRequest(req))
			requireConnectCode(t, err, connect.CodeInvalidArgument)
		}
	})

	t.Run("maps unexpected backend errors to internal", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := handler.Register(canceledCtx, connect.NewRequest(&authv2.RegisterRequest{
			Email:    "register-canceled@example.com",
			Password: "password123",
		}))
		requireConnectCode(t, err, connect.CodeInternal)
	})
}

func TestAuthServiceV2_Login(t *testing.T) {
	handler, authService := setupAuthServiceV2(t)
	_, err := authService.RegisterUser(context.Background(), "login-user@example.com", "password123")
	require.NoError(t, err)

	t.Run("logs in and returns token", func(t *testing.T) {
		res, err := handler.Login(context.Background(), connect.NewRequest(&authv2.LoginRequest{
			Email:    "login-user@example.com",
			Password: "password123",
		}))
		require.NoError(t, err)
		assert.NotEmpty(t, res.Msg.Token)
	})

	t.Run("maps invalid credentials to unauthenticated", func(t *testing.T) {
		_, err := handler.Login(context.Background(), connect.NewRequest(&authv2.LoginRequest{
			Email:    "login-user@example.com",
			Password: "wrong-password",
		}))
		requireConnectCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("maps unexpected backend errors to internal", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := handler.Login(canceledCtx, connect.NewRequest(&authv2.LoginRequest{
			Email:    "login-user@example.com",
			Password: "password123",
		}))
		requireConnectCode(t, err, connect.CodeInternal)
	})
}

func TestAuthServiceV2_AssociateHub(t *testing.T) {
	handler, authService := setupAuthServiceV2(t)
	userID, err := authService.RegisterUser(context.Background(), "associate-v2@example.com", "password123")
	require.NoError(t, err)

	t.Run("requires authenticated user", func(t *testing.T) {
		_, err := handler.AssociateHub(context.Background(), connect.NewRequest(&authv2.AssociateHubRequest{}))
		requireConnectCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("associates hub for authenticated user", func(t *testing.T) {
		res, err := handler.AssociateHub(authUserCtx(userID), connect.NewRequest(&authv2.AssociateHubRequest{
			DeviceId:  "assoc-v2-device-1",
			HubSecret: "assoc-v2-secret-1",
			HubName:   "Associate V2 Hub",
		}))
		require.NoError(t, err)
		assert.NotEmpty(t, res.Msg.HubId)
		assert.Equal(t, "assoc-v2-device-1", res.Msg.DeviceId)
		assert.Equal(t, "Associate V2 Hub", res.Msg.HubName)
	})

	t.Run("maps duplicate device to already exists", func(t *testing.T) {
		_, err := handler.AssociateHub(authUserCtx(userID), connect.NewRequest(&authv2.AssociateHubRequest{
			DeviceId:  "assoc-v2-device-dup",
			HubSecret: "assoc-v2-secret-dup",
			HubName:   "Hub First",
		}))
		require.NoError(t, err)

		_, err = handler.AssociateHub(authUserCtx(userID), connect.NewRequest(&authv2.AssociateHubRequest{
			DeviceId:  "assoc-v2-device-dup",
			HubSecret: "assoc-v2-secret-dup-2",
			HubName:   "Hub Second",
		}))
		requireConnectCode(t, err, connect.CodeAlreadyExists)
	})

	t.Run("maps unexpected backend errors to internal", func(t *testing.T) {
		invalidUserCtx := authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{
			UserID:   "not-a-number",
			Username: "user@example.com",
		})

		_, err := handler.AssociateHub(invalidUserCtx, connect.NewRequest(&authv2.AssociateHubRequest{
			DeviceId:  "assoc-v2-device-invalid-user",
			HubSecret: "assoc-v2-secret-invalid-user",
			HubName:   "Invalid User Hub",
		}))
		requireConnectCode(t, err, connect.CodeInternal)
	})
}

func TestAuthServiceV2_ClaimHubToken(t *testing.T) {
	handler, authService := setupAuthServiceV2(t)
	userID, err := authService.RegisterUser(context.Background(), "claim-v2@example.com", "password123")
	require.NoError(t, err)

	t.Run("claims hub token", func(t *testing.T) {
		_, err := authService.AssociateHub(context.Background(), userID, "claim-v2-device-1", "claim-v2-secret-1", "Claim V2 Hub")
		require.NoError(t, err)

		res, err := handler.ClaimHubToken(context.Background(), connect.NewRequest(&authv2.ClaimHubTokenRequest{
			DeviceId:  "claim-v2-device-1",
			HubSecret: "claim-v2-secret-1",
		}))
		require.NoError(t, err)
		assert.NotEmpty(t, res.Msg.Token)
	})

	t.Run("maps invalid credentials to permission denied", func(t *testing.T) {
		_, err := handler.ClaimHubToken(context.Background(), connect.NewRequest(&authv2.ClaimHubTokenRequest{
			DeviceId:  "unknown-claim-v2-device",
			HubSecret: "any",
		}))
		requireConnectCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("maps already claimed to failed precondition", func(t *testing.T) {
		_, err := authService.AssociateHub(context.Background(), userID, "claim-v2-device-2", "claim-v2-secret-2", "Claimed Once")
		require.NoError(t, err)
		_, err = authService.ClaimHubToken(context.Background(), "claim-v2-device-2", "claim-v2-secret-2")
		require.NoError(t, err)

		_, err = handler.ClaimHubToken(context.Background(), connect.NewRequest(&authv2.ClaimHubTokenRequest{
			DeviceId:  "claim-v2-device-2",
			HubSecret: "claim-v2-secret-2",
		}))
		requireConnectCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("maps unexpected backend errors to internal", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := handler.ClaimHubToken(canceledCtx, connect.NewRequest(&authv2.ClaimHubTokenRequest{
			DeviceId:  "claim-v2-device-1",
			HubSecret: "claim-v2-secret-1",
		}))
		requireConnectCode(t, err, connect.CodeInternal)
	})
}

func TestAuthServiceV2_ListHubs(t *testing.T) {
	handler, authService := setupAuthServiceV2(t)
	userID, err := authService.RegisterUser(context.Background(), "listhubs-v2@example.com", "password123")
	require.NoError(t, err)

	t.Run("requires authentication", func(t *testing.T) {
		_, err := handler.ListHubs(context.Background(), connect.NewRequest(&authv2.ListHubsRequest{}))
		requireConnectCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("lists hubs for the authenticated user", func(t *testing.T) {
		_, err := authService.AssociateHub(context.Background(), userID, "listhubs-v2-device-1", "listhubs-v2-secret-1", "Unclaimed")
		require.NoError(t, err)
		_, err = authService.AssociateHub(context.Background(), userID, "listhubs-v2-device-2", "listhubs-v2-secret-2", "Claimed")
		require.NoError(t, err)
		_, err = authService.ClaimHubToken(context.Background(), "listhubs-v2-device-2", "listhubs-v2-secret-2")
		require.NoError(t, err)

		res, err := handler.ListHubs(authUserCtx(userID), connect.NewRequest(&authv2.ListHubsRequest{}))
		require.NoError(t, err)
		require.Len(t, res.Msg.Hubs, 2)

		statusByDevice := map[string]*authv2.HubInfo{}
		for _, hub := range res.Msg.Hubs {
			statusByDevice[hub.DeviceId] = hub
		}

		require.Contains(t, statusByDevice, "listhubs-v2-device-1")
		require.Contains(t, statusByDevice, "listhubs-v2-device-2")
		assert.False(t, statusByDevice["listhubs-v2-device-1"].Claimed)
		assert.True(t, statusByDevice["listhubs-v2-device-2"].Claimed)
	})

	t.Run("maps backend errors to internal", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(authUserCtx(userID))
		cancel()

		_, err := handler.ListHubs(canceledCtx, connect.NewRequest(&authv2.ListHubsRequest{}))
		requireConnectCode(t, err, connect.CodeInternal)
	})
}

func TestAuthServiceV2_RevokeHub(t *testing.T) {
	handler, authService := setupAuthServiceV2(t)
	userID, err := authService.RegisterUser(context.Background(), "revoke-v2@example.com", "password123")
	require.NoError(t, err)

	t.Run("requires authentication", func(t *testing.T) {
		_, err := handler.RevokeHub(context.Background(), connect.NewRequest(&authv2.RevokeHubRequest{HubId: "1"}))
		requireConnectCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("revokes an owned hub", func(t *testing.T) {
		hubID, err := authService.AssociateHub(context.Background(), userID, "revoke-v2-device-1", "revoke-v2-secret-1", "Revokable Hub")
		require.NoError(t, err)
		_, err = authService.ClaimHubToken(context.Background(), "revoke-v2-device-1", "revoke-v2-secret-1")
		require.NoError(t, err)

		_, err = handler.RevokeHub(authUserCtx(userID), connect.NewRequest(&authv2.RevokeHubRequest{HubId: hubID}))
		require.NoError(t, err)

		_, err = authService.ClaimHubToken(context.Background(), "revoke-v2-device-1", "revoke-v2-secret-1")
		assert.NoError(t, err)
	})

	t.Run("maps unknown hub to not found", func(t *testing.T) {
		_, err := handler.RevokeHub(authUserCtx(userID), connect.NewRequest(&authv2.RevokeHubRequest{HubId: "999999"}))
		requireConnectCode(t, err, connect.CodeNotFound)
	})

	t.Run("maps backend errors to internal", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(authUserCtx(userID))
		cancel()

		_, err := handler.RevokeHub(canceledCtx, connect.NewRequest(&authv2.RevokeHubRequest{HubId: "1"}))
		requireConnectCode(t, err, connect.CodeInternal)
	})
}
