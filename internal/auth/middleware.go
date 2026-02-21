package auth

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	authv1connect "github.com/harvesthub-gardening-tool/protos-go/auth/v1/authv1connect"
	authctx "harvest-hub/api/internal/auth/context"
	authjwt "harvest-hub/api/internal/auth/jwt"
)

// NewJWTAuthInterceptor returns a Connect unary interceptor that validates RS256
// JWT tokens and enforces per-RPC authorization rules:
//   - /auth.v1.AuthService/Register, /auth.v1.AuthService/Login — public, no token required
//   - /garden.v1.GardenService/InsertSensorData — service accounts (Hub devices) only
//   - All other endpoints — any valid token (user or service account)
func NewJWTAuthInterceptor(jwtManager *authjwt.JWTManager) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// Public endpoints — no token required
			switch req.Spec().Procedure {
			case authv1connect.AuthServiceRegisterProcedure,
				authv1connect.AuthServiceLoginProcedure:
				return next(ctx, req)
			}

			authHeader := req.Header().Get("Authorization")
			if authHeader == "" {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing authorization header"))
			}

			token, err := extractBearerToken(authHeader)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, err)
			}

			claims, err := jwtManager.ValidateToken(token)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, err)
			}

			info := &authctx.AuthInfo{
				UserID:   claims.UserID,
				Username: claims.Username,
			}

			// Only service accounts (Hub devices) may insert sensor data.
			if req.Spec().Procedure == "/garden.v1.GardenService/InsertSensorData" {
				if info.Username != "" {
					return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only hub can insert data"))
				}
			}

			ctx = authctx.SetAuthInfo(ctx, info)
			return next(ctx, req)
		}
	}
}

// extractBearerToken parses a "Bearer <token>" Authorization header.
func extractBearerToken(authHeader string) (string, error) {
	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return "", errors.New("authorization header must start with 'Bearer '")
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, prefix))
	if token == "" {
		return "", errors.New("bearer token is empty")
	}
	return token, nil
}
