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
