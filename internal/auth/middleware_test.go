package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"

	"harvest-hub/api/internal/auth"
	authctx "harvest-hub/api/internal/auth/context"
	authjwt "harvest-hub/api/internal/auth/jwt"
)

// testJWTHelper wraps JWTManager to provide test utilities.
type testJWTHelper struct {
	manager *authjwt.JWTManager
}

func newTestJWTHelper(t *testing.T) *testJWTHelper {
	t.Helper()
	manager, err := authjwt.NewJWTManager()
	if err != nil {
		t.Fatalf("failed to create JWT manager: %v", err)
	}
	return &testJWTHelper{manager: manager}
}

func (h *testJWTHelper) generateUserToken(t *testing.T, userID, username string) string {
	t.Helper()
	token, err := h.manager.GenerateToken(userID, username, 1*time.Hour)
	if err != nil {
		t.Fatalf("failed to generate user token: %v", err)
	}
	return token
}

func (h *testJWTHelper) generateServiceAccountToken(t *testing.T, serviceAccountID string) string {
	t.Helper()
	token, err := h.manager.GenerateToken(serviceAccountID, "", 1*time.Hour)
	if err != nil {
		t.Fatalf("failed to generate service account token: %v", err)
	}
	return token
}

func (h *testJWTHelper) generateExpiredToken(t *testing.T, userID, username string) string {
	t.Helper()
	token, err := h.manager.GenerateToken(userID, username, 1*time.Nanosecond)
	if err != nil {
		t.Fatalf("failed to generate expired token: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	return token
}

// fakeRequest builds a minimal connect.AnyRequest with the given procedure and headers.
func fakeRequest(procedure string, headers http.Header) connect.AnyRequest {
	return &stubRequest{procedure: procedure, headers: headers}
}

type stubRequest struct {
	connect.AnyRequest
	procedure string
	headers   http.Header
}

func (r *stubRequest) Spec() connect.Spec {
	return connect.Spec{Procedure: r.procedure}
}

func (r *stubRequest) Header() http.Header {
	return r.headers
}

func (r *stubRequest) Peer() connect.Peer {
	return connect.Peer{}
}

// callRecord records whether the next handler was called and captures context.
type callRecord struct {
	called bool
	ctx    context.Context
}

func passthroughNext(rec *callRecord) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		rec.called = true
		rec.ctx = ctx
		return nil, nil
	}
}

func TestConnectInterceptor_MissingAuthHeader(t *testing.T) {
	helper := newTestJWTHelper(t)
	interceptor := auth.NewJWTAuthInterceptor(helper.manager)

	handler := interceptor(passthroughNext(&callRecord{}))
	req := fakeRequest("/garden.v1.GardenService/GetSummary", http.Header{})

	_, err := handler(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for missing auth header, got nil")
	}
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("expected CodeUnauthenticated, got %v", connect.CodeOf(err))
	}
}

func TestConnectInterceptor_InvalidToken(t *testing.T) {
	helper := newTestJWTHelper(t)
	interceptor := auth.NewJWTAuthInterceptor(helper.manager)

	handler := interceptor(passthroughNext(&callRecord{}))
	headers := http.Header{"Authorization": []string{"Bearer invalid_token_string"}}
	req := fakeRequest("/garden.v1.GardenService/GetSummary", headers)

	_, err := handler(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for invalid token, got nil")
	}
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("expected CodeUnauthenticated, got %v", connect.CodeOf(err))
	}
}

func TestConnectInterceptor_ExpiredToken(t *testing.T) {
	helper := newTestJWTHelper(t)
	interceptor := auth.NewJWTAuthInterceptor(helper.manager)

	expiredToken := helper.generateExpiredToken(t, "user-1", "alice")
	handler := interceptor(passthroughNext(&callRecord{}))
	headers := http.Header{"Authorization": []string{fmt.Sprintf("Bearer %s", expiredToken)}}
	req := fakeRequest("/garden.v1.GardenService/GetSummary", headers)

	_, err := handler(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("expected CodeUnauthenticated, got %v", connect.CodeOf(err))
	}
}

func TestConnectInterceptor_MalformedBearerHeader(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{
			name:   "missing Bearer prefix",
			header: "NotBearer token",
		},
		{
			name:   "empty token after Bearer",
			header: "Bearer ",
		},
		{
			name:   "Bearer with only whitespace",
			header: "Bearer   ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := newTestJWTHelper(t)
			interceptor := auth.NewJWTAuthInterceptor(helper.manager)

			handler := interceptor(passthroughNext(&callRecord{}))
			headers := http.Header{"Authorization": []string{tt.header}}
			req := fakeRequest("/garden.v1.GardenService/GetSummary", headers)

			_, err := handler(context.Background(), req)
			if err == nil {
				t.Fatal("expected error for malformed bearer header, got nil")
			}
			if connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Errorf("expected CodeUnauthenticated, got %v", connect.CodeOf(err))
			}
		})
	}
}

func TestConnectInterceptor_InsertSensorData_Authorization(t *testing.T) {
	tests := []struct {
		name     string
		userID   string
		username string
		wantCode connect.Code
		wantPass bool
	}{
		{
			name:     "service account allowed",
			userID:   "hub-1",
			username: "",
			wantPass: true,
		},
		{
			name:     "user rejected from insert",
			userID:   "user-1",
			username: "alice",
			wantCode: connect.CodePermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := newTestJWTHelper(t)
			interceptor := auth.NewJWTAuthInterceptor(helper.manager)

			var token string
			if tt.username == "" {
				token = helper.generateServiceAccountToken(t, tt.userID)
			} else {
				token = helper.generateUserToken(t, tt.userID, tt.username)
			}

			rec := &callRecord{}
			handler := interceptor(passthroughNext(rec))
			headers := http.Header{"Authorization": []string{fmt.Sprintf("Bearer %s", token)}}
			req := fakeRequest("/garden.v1.GardenService/InsertSensorData", headers)

			_, err := handler(context.Background(), req)

			if tt.wantPass {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if !rec.called {
					t.Fatal("expected next handler to be called")
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if connect.CodeOf(err) != tt.wantCode {
					t.Errorf("expected %v, got %v", tt.wantCode, connect.CodeOf(err))
				}
				if rec.called {
					t.Fatal("next handler should not have been called")
				}
			}
		})
	}
}

func TestConnectInterceptor_GetSummary_AnyAuthenticatedUser(t *testing.T) {
	tests := []struct {
		name     string
		userID   string
		username string
	}{
		{
			name:     "regular user",
			userID:   "user-1",
			username: "alice",
		},
		{
			name:     "service account",
			userID:   "hub-1",
			username: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := newTestJWTHelper(t)
			interceptor := auth.NewJWTAuthInterceptor(helper.manager)

			var token string
			if tt.username == "" {
				token = helper.generateServiceAccountToken(t, tt.userID)
			} else {
				token = helper.generateUserToken(t, tt.userID, tt.username)
			}

			rec := &callRecord{}
			handler := interceptor(passthroughNext(rec))
			headers := http.Header{"Authorization": []string{fmt.Sprintf("Bearer %s", token)}}
			req := fakeRequest("/garden.v1.GardenService/GetSummary", headers)

			_, err := handler(context.Background(), req)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if !rec.called {
				t.Fatal("expected next handler to be called")
			}
		})
	}
}

func TestConnectInterceptor_SetsAuthContext(t *testing.T) {
	helper := newTestJWTHelper(t)
	interceptor := auth.NewJWTAuthInterceptor(helper.manager)

	token := helper.generateUserToken(t, "user-42", "bob")

	rec := &callRecord{}
	handler := interceptor(passthroughNext(rec))
	headers := http.Header{"Authorization": []string{fmt.Sprintf("Bearer %s", token)}}
	req := fakeRequest("/garden.v1.GardenService/GetSummary", headers)

	_, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := authctx.GetUserID(rec.ctx); got != "user-42" {
		t.Errorf("GetUserID() = %q, want %q", got, "user-42")
	}
	if got := authctx.GetUsername(rec.ctx); got != "bob" {
		t.Errorf("GetUsername() = %q, want %q", got, "bob")
	}
	if authctx.IsServiceAccount(rec.ctx) {
		t.Error("IsServiceAccount() = true, want false for user with username")
	}
}

func TestContextAccessors_NoAuth(t *testing.T) {
	ctx := context.Background()

	if got := authctx.GetUserID(ctx); got != "" {
		t.Errorf("GetUserID() = %q, want empty", got)
	}
	if got := authctx.GetUsername(ctx); got != "" {
		t.Errorf("GetUsername() = %q, want empty", got)
	}
	if _, ok := authctx.GetAuthInfo(ctx); ok {
		t.Error("GetAuthInfo() ok = true, want false")
	}
	if authctx.IsServiceAccount(ctx) {
		t.Error("IsServiceAccount() = true, want false")
	}
}
