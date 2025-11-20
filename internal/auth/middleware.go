package auth

import (
"context"
"errors"
"os"

"connectrpc.com/connect"
"github.com/zitadel/zitadel-go/v3/pkg/authorization"
"github.com/zitadel/zitadel-go/v3/pkg/authorization/oauth"
)

// ConnectInterceptor adapts Zitadel's authorization for Connect RPC
func ConnectInterceptor(authz *authorization.Authorizer[*oauth.IntrospectionContext]) connect.UnaryInterceptorFunc {
return func(next connect.UnaryFunc) connect.UnaryFunc {
return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
// Extract Authorization header
authHeader := req.Header().Get("Authorization")
if authHeader == "" {
return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing authorization header"))
}

// Use Zitadel's authorization to validate token
authCtx, err := authz.CheckAuthorization(ctx, authHeader)
if err != nil {
return nil, connect.NewError(connect.CodeUnauthenticated, err)
}

// Authorization logic: only Hub service account can write
if req.Spec().Procedure == "/garden.v1.GardenService/InsertSensorData" {
// Service accounts have no username
if authCtx.Username != "" {
return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only hub can insert data"))
}

// Optional: verify specific hub ID
expectedHubID := os.Getenv("HUB_SERVICE_ACCOUNT_ID")
if expectedHubID != "" && authCtx.UserID() != expectedHubID {
return nil, connect.NewError(connect.CodePermissionDenied, errors.New("unauthorized hub"))
}
}

// Pass auth context to handlers
// Auth context is already in authCtx
return next(ctx, req)
}
}
}
