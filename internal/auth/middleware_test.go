package auth_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	"harvest-hub/api/internal/auth"
)

// mockValidator implements auth.TokenValidator for testing.
type mockValidator struct {
	info *auth.AuthInfo
	err  error
}

func (m *mockValidator) Validate(_ context.Context, _ string) (*auth.AuthInfo, error) {
	return m.info, m.err
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

// passthrough is the "next" handler that records it was called and returns the context.
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
	interceptor := auth.ConnectInterceptor(
		&mockValidator{info: &auth.AuthInfo{UserID: "u1"}},
		auth.InterceptorConfig{},
	)

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
	interceptor := auth.ConnectInterceptor(
		&mockValidator{err: errors.New("bad token")},
		auth.InterceptorConfig{},
	)

	handler := interceptor(passthroughNext(&callRecord{}))
	headers := http.Header{"Authorization": []string{"Bearer bad"}}
	req := fakeRequest("/garden.v1.GardenService/GetSummary", headers)

	_, err := handler(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for invalid token, got nil")
	}
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("expected CodeUnauthenticated, got %v", connect.CodeOf(err))
	}
}

func TestConnectInterceptor_InsertSensorData_Authorization(t *testing.T) {
	tests := []struct {
		name     string
		info     *auth.AuthInfo
		cfg      auth.InterceptorConfig
		wantCode connect.Code
		wantPass bool
	}{
		{
			name:     "service account allowed",
			info:     &auth.AuthInfo{UserID: "hub-1", Username: ""},
			cfg:      auth.InterceptorConfig{},
			wantPass: true,
		},
		{
			name:     "service account with matching hub ID",
			info:     &auth.AuthInfo{UserID: "hub-1", Username: ""},
			cfg:      auth.InterceptorConfig{HubServiceAccountID: "hub-1"},
			wantPass: true,
		},
		{
			name:     "service account with wrong hub ID",
			info:     &auth.AuthInfo{UserID: "hub-2", Username: ""},
			cfg:      auth.InterceptorConfig{HubServiceAccountID: "hub-1"},
			wantCode: connect.CodePermissionDenied,
		},
		{
			name:     "user rejected from insert",
			info:     &auth.AuthInfo{UserID: "user-1", Username: "alice"},
			cfg:      auth.InterceptorConfig{},
			wantCode: connect.CodePermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interceptor := auth.ConnectInterceptor(
				&mockValidator{info: tt.info},
				tt.cfg,
			)

			rec := &callRecord{}
			handler := interceptor(passthroughNext(rec))
			headers := http.Header{"Authorization": []string{"Bearer valid"}}
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
		name string
		info *auth.AuthInfo
	}{
		{
			name: "regular user",
			info: &auth.AuthInfo{UserID: "user-1", Username: "alice"},
		},
		{
			name: "service account",
			info: &auth.AuthInfo{UserID: "hub-1", Username: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interceptor := auth.ConnectInterceptor(
				&mockValidator{info: tt.info},
				auth.InterceptorConfig{},
			)

			rec := &callRecord{}
			handler := interceptor(passthroughNext(rec))
			headers := http.Header{"Authorization": []string{"Bearer valid"}}
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
	info := &auth.AuthInfo{UserID: "user-42", Username: "bob"}
	interceptor := auth.ConnectInterceptor(
		&mockValidator{info: info},
		auth.InterceptorConfig{},
	)

	rec := &callRecord{}
	handler := interceptor(passthroughNext(rec))
	headers := http.Header{"Authorization": []string{"Bearer valid"}}
	req := fakeRequest("/garden.v1.GardenService/GetSummary", headers)

	_, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify context accessors work with the context passed to next handler.
	if got := auth.GetUserID(rec.ctx); got != "user-42" {
		t.Errorf("GetUserID() = %q, want %q", got, "user-42")
	}
	if got := auth.GetUsername(rec.ctx); got != "bob" {
		t.Errorf("GetUsername() = %q, want %q", got, "bob")
	}
	if auth.IsServiceAccount(rec.ctx) {
		t.Error("IsServiceAccount() = true, want false for user with username")
	}
}

func TestContextAccessors_NoAuth(t *testing.T) {
	ctx := context.Background()

	if got := auth.GetUserID(ctx); got != "" {
		t.Errorf("GetUserID() = %q, want empty", got)
	}
	if got := auth.GetUsername(ctx); got != "" {
		t.Errorf("GetUsername() = %q, want empty", got)
	}
	if _, ok := auth.GetAuthInfo(ctx); ok {
		t.Error("GetAuthInfo() ok = true, want false")
	}
	if auth.IsServiceAccount(ctx) {
		t.Error("IsServiceAccount() = true, want false")
	}
}
