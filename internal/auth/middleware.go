package auth

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/zitadel/zitadel-go/v3/pkg/authorization"
	"github.com/zitadel/zitadel-go/v3/pkg/authorization/oauth"
	"github.com/zitadel/zitadel-go/v3/pkg/zitadel"
)

type contextKey string

const authContextKey contextKey = "auth_context"

// AuthInfo holds the identity extracted from a validated token.
type AuthInfo struct {
	UserID   string
	Username string
}

// TokenValidator validates an authorization header and returns identity info.
type TokenValidator interface {
	Validate(ctx context.Context, authHeader string) (*AuthInfo, error)
}

// InterceptorConfig holds authorization rules for the interceptor.
type InterceptorConfig struct {
	HubServiceAccountID string
}

// zitadelValidator adapts Zitadel's authorizer to our TokenValidator interface.
type zitadelValidator struct {
	authz *authorization.Authorizer[*oauth.IntrospectionContext]
}

func (z *zitadelValidator) Validate(ctx context.Context, authHeader string) (*AuthInfo, error) {
	authCtx, err := z.authz.CheckAuthorization(ctx, authHeader)
	if err != nil {
		return nil, err
	}
	return &AuthInfo{
		UserID:   authCtx.UserID(),
		Username: authCtx.Username,
	}, nil
}

// NewAuthInterceptor creates a Connect interceptor with Zitadel JWT validation.
func NewAuthInterceptor(ctx context.Context, zitadelDomain, clientID, zitadelEnv string, cfg InterceptorConfig) (connect.UnaryInterceptorFunc, error) {
	var zitadelOpts []zitadel.Option
	if zitadelEnv == "LOCAL" {
		// WithInsecure with empty port for HTTP (port already in domain)
		zitadelOpts = append(zitadelOpts, zitadel.WithInsecure(""))
	}

	authz, err := authorization.New(
		ctx,
		zitadel.New(zitadelDomain, zitadelOpts...),
		oauth.WithJWT(clientID, http.DefaultClient),
	)
	if err != nil {
		return nil, err
	}

	return ConnectInterceptor(&zitadelValidator{authz: authz}, cfg), nil
}

// ConnectInterceptor creates a Connect unary interceptor that validates tokens
// and enforces per-RPC authorization rules.
func ConnectInterceptor(validator TokenValidator, cfg InterceptorConfig) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			authHeader := req.Header().Get("Authorization")
			if authHeader == "" {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing authorization header"))
			}

			info, err := validator.Validate(ctx, authHeader)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, err)
			}

			// Authorization: only Hub service account can insert data.
			if req.Spec().Procedure == "/garden.v1.GardenService/InsertSensorData" {
				if info.Username != "" {
					return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only hub can insert data"))
				}
				if cfg.HubServiceAccountID != "" && info.UserID != cfg.HubServiceAccountID {
					return nil, connect.NewError(connect.CodePermissionDenied, errors.New("unauthorized hub"))
				}
			}

			ctx = context.WithValue(ctx, authContextKey, info)
			return next(ctx, req)
		}
	}
}

// GetUserID extracts user ID from context.
func GetUserID(ctx context.Context) string {
	if info, ok := ctx.Value(authContextKey).(*AuthInfo); ok {
		return info.UserID
	}
	return ""
}

// GetUsername extracts username from context.
func GetUsername(ctx context.Context) string {
	if info, ok := ctx.Value(authContextKey).(*AuthInfo); ok {
		return info.Username
	}
	return ""
}

// GetAuthInfo extracts full auth info from context.
func GetAuthInfo(ctx context.Context) (*AuthInfo, bool) {
	info, ok := ctx.Value(authContextKey).(*AuthInfo)
	return info, ok
}

// IsServiceAccount checks if the authenticated entity is a service account.
func IsServiceAccount(ctx context.Context) bool {
	if info, ok := ctx.Value(authContextKey).(*AuthInfo); ok {
		return info.Username == ""
	}
	return false
}
