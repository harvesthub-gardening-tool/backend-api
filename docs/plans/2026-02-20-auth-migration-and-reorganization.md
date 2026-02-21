# Auth Migration & Reorganization Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Port the complete JWT auth implementation from the `feature/authboss-migration` worktree into `feature/auth`, reorganize `internal/auth/` into `jwt/` and `context/` subpackages, add `auth/v1/auth.proto` to the protos repo, and clean up the stray MIGRATION_SUMMARY.md.

**Architecture:** The `auth` package is split into three layers: `internal/auth/jwt/` (pure JWT crypto, no deps on rest of auth), `internal/auth/context/` (AuthInfo + context storage/retrieval), and `internal/auth/` (models, AuthService business logic, Connect RPC middleware). The service layer imports `auth` and `authctx`. No circular imports.

**Tech Stack:** Go 1.24, Connect RPC, GORM + TimescaleDB/SQLite (tests), `github.com/golang-jwt/jwt/v5`, `golang.org/x/crypto/bcrypt`, Protobuf + Buf CLI (protos repo)

---

## Task 1: Replace go.mod

**Files:**
- Modify: `go.mod`

**Step 1: Replace go.mod content entirely**

```go
module harvest-hub/api

go 1.24.0

require (
	connectrpc.com/connect v1.19.1
	github.com/golang-jwt/jwt/v5 v5.3.0
	github.com/harvesthub-gardening-tool/protos-go v0.0.0-20260108140851-c213a5dbbd06
	github.com/jackc/pgx/v5 v5.8.0
	github.com/pashagolub/pgxmock/v4 v4.9.0
	github.com/stretchr/testify v1.11.1
	golang.org/x/crypto v0.48.0
	golang.org/x/net v0.49.0
	gorm.io/driver/postgres v1.6.0
	gorm.io/driver/sqlite v1.6.0
	gorm.io/gorm v1.31.1
)
```

**Step 2: Sync dependencies**

```bash
cd /home/ewan/projets/Harvest-Hub/backend-api
go mod tidy
```

Expected: Downloads new deps (GORM, JWT, bcrypt, sqlite), removes Zitadel. `go.sum` is updated.

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: replace Zitadel deps with JWT, GORM, bcrypt"
```

---

## Task 2: Create `internal/auth/jwt/` subpackage

This package is pure JWT crypto — no dependency on the rest of the auth system.

**Files:**
- Create: `internal/auth/jwt/claims.go`
- Create: `internal/auth/jwt/manager.go`
- Create: `internal/auth/jwt/jwt_test.go`

**Step 1: Create `internal/auth/jwt/claims.go`**

```go
package authjwt

import "github.com/golang-jwt/jwt/v5"

// Claims represents the custom JWT claims for all tokens in the system.
// Service accounts (Hub devices) are identified by an empty Username field.
type Claims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// IsServiceAccount returns true if the token belongs to a Hub device (no username).
func (c *Claims) IsServiceAccount() bool {
	return c.Username == ""
}
```

**Step 2: Create `internal/auth/jwt/manager.go`**

Copy from the worktree's `internal/auth/jwt.go`, split into manager + key persistence, rename package to `authjwt`:

```go
package authjwt

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTManager handles JWT token generation and validation using RSA-2048 keys.
type JWTManager struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
}

// NewJWTManager creates a JWTManager with a fresh RSA 2048-bit key pair.
// Keys are ephemeral — use NewOrLoadJWTManager for production.
func NewJWTManager() (*JWTManager, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA private key: %w", err)
	}
	return &JWTManager{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
	}, nil
}

// NewOrLoadJWTManager loads existing PEM keys from disk, or generates and saves
// new ones. Key persistence is critical: without it, all hub tokens (1-year
// expiry) become invalid on every server restart.
//
// Key files: {keyPath}/.jwt_private.pem and {keyPath}/.jwt_public.pem
func NewOrLoadJWTManager(keyPath string) (*JWTManager, error) {
	privatePath := filepath.Join(keyPath, ".jwt_private.pem")
	publicPath := filepath.Join(keyPath, ".jwt_public.pem")

	if fileExists(privatePath) && fileExists(publicPath) {
		m, err := loadFromDisk(privatePath, publicPath)
		if err != nil {
			fmt.Printf("⚠️  Failed to load JWT keys: %v\n", err)
		} else {
			fmt.Println("🔑 Loaded existing JWT keys from disk")
			return m, nil
		}
	}

	m, err := NewJWTManager()
	if err != nil {
		return nil, err
	}
	if err := saveToDisk(m, privatePath, publicPath); err != nil {
		return nil, fmt.Errorf("failed to save JWT keys: %w", err)
	}
	fmt.Println("🔑 Generated and saved new JWT keys")
	return m, nil
}

// GenerateToken signs a new JWT with the given identity and duration.
// Pass an empty username for service account (Hub device) tokens.
func (m *JWTManager) GenerateToken(userID, username string, expiry time.Duration) (string, error) {
	if userID == "" {
		return "", errors.New("userID cannot be empty")
	}
	if expiry <= 0 {
		return "", errors.New("expiry must be positive")
	}

	now := time.Now()
	claims := Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(m.privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}
	return signed, nil
}

// ValidateToken parses and validates a JWT string. Checks signature (RS256),
// algorithm, and expiry. Returns Claims on success.
func (m *JWTManager) ValidateToken(tokenString string) (*Claims, error) {
	if tokenString == "" {
		return nil, errors.New("token string cannot be empty")
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing algorithm: %v", token.Header["alg"])
		}
		if token.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, fmt.Errorf("expected RS256, got %s", token.Method.Alg())
		}
		return m.publicKey, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, fmt.Errorf("token has expired: %w", err)
		}
		if errors.Is(err, jwt.ErrSignatureInvalid) {
			return nil, fmt.Errorf("invalid token signature: %w", err)
		}
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}

// ── Key persistence helpers ───────────────────────────────────────────────────

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func loadFromDisk(privatePath, publicPath string) (*JWTManager, error) {
	privData, err := os.ReadFile(privatePath)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	block, rest := pem.Decode(privData)
	if block == nil {
		return nil, errors.New("failed to decode private key PEM")
	}
	if len(rest) > 0 {
		return nil, errors.New("extra data after private key PEM block")
	}
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	pubData, err := os.ReadFile(publicPath)
	if err != nil {
		return nil, fmt.Errorf("read public key: %w", err)
	}
	block, rest = pem.Decode(pubData)
	if block == nil {
		return nil, errors.New("failed to decode public key PEM")
	}
	if len(rest) > 0 {
		return nil, errors.New("extra data after public key PEM block")
	}
	pubInterface, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	publicKey, ok := pubInterface.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("public key is not RSA")
	}

	return &JWTManager{privateKey: privateKey, publicKey: publicKey}, nil
}

func saveToDisk(m *JWTManager, privatePath, publicPath string) error {
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(m.privateKey),
	})
	if err := os.WriteFile(privatePath, privPEM, 0600); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}

	pubBytes, err := x509.MarshalPKIXPublicKey(m.publicKey)
	if err != nil {
		return fmt.Errorf("marshal public key: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes})
	if err := os.WriteFile(publicPath, pubPEM, 0644); err != nil {
		return fmt.Errorf("write public key: %w", err)
	}
	return nil
}
```

**Step 3: Create `internal/auth/jwt/jwt_test.go`**

Adapt from worktree's `internal/auth/jwt_test.go` — change `package auth` → `package authjwt` and update type references:

Copy the worktree file at `/home/ewan/projets/Harvest-Hub/backend-api/.worktrees/authboss-migration/internal/auth/jwt_test.go`, then:
- Change `package auth` → `package authjwt`
- Remove any `auth.` prefixes (tests are now in the same package)
- All `JWTManager`, `Claims`, `NewJWTManager`, etc. are local

**Step 4: Run tests**

```bash
go test ./internal/auth/jwt/... -v
```

Expected: All tests pass.

**Step 5: Commit**

```bash
git add internal/auth/jwt/
git commit -m "feat: add internal/auth/jwt subpackage (JWTManager, Claims, key persistence)"
```

---

## Task 3: Create `internal/auth/context/` subpackage

This package owns `AuthInfo` and all context storage/retrieval helpers. It has zero dependencies on the rest of the auth system.

**Files:**
- Create: `internal/auth/context/context.go`
- Create: `internal/auth/context/context_test.go`

**Step 1: Create `internal/auth/context/context.go`**

```go
package authctx

import "context"

// contextKey is an unexported type used as a context key to avoid collisions.
type contextKey struct{}

var authKey = contextKey{}

// AuthInfo holds the identity extracted from a validated JWT token.
type AuthInfo struct {
	UserID   string
	Username string
}

// IsServiceAccount returns true when the token belongs to a Hub device (no username).
func (a *AuthInfo) IsServiceAccount() bool {
	return a.Username == ""
}

// SetAuthInfo stores AuthInfo in the context. Called by the auth middleware.
func SetAuthInfo(ctx context.Context, info *AuthInfo) context.Context {
	return context.WithValue(ctx, authKey, info)
}

// GetUserID returns the authenticated user's ID, or empty string if not set.
func GetUserID(ctx context.Context) string {
	if info, ok := ctx.Value(authKey).(*AuthInfo); ok {
		return info.UserID
	}
	return ""
}

// GetUsername returns the authenticated user's email/username, or empty string.
// Empty string indicates a service account (Hub device).
func GetUsername(ctx context.Context) string {
	if info, ok := ctx.Value(authKey).(*AuthInfo); ok {
		return info.Username
	}
	return ""
}

// GetAuthInfo returns the full AuthInfo from context.
func GetAuthInfo(ctx context.Context) (*AuthInfo, bool) {
	info, ok := ctx.Value(authKey).(*AuthInfo)
	return info, ok
}

// IsServiceAccount returns true if the authenticated entity is a Hub device.
func IsServiceAccount(ctx context.Context) bool {
	if info, ok := ctx.Value(authKey).(*AuthInfo); ok {
		return info.Username == ""
	}
	return false
}
```

**Step 2: Create `internal/auth/context/context_test.go`**

Adapt the relevant tests from worktree's `context_test.go` — change package to `authctx_test` and update type/function references:

```go
package authctx_test

import (
	"context"
	"testing"

	authctx "harvest-hub/api/internal/auth/context"
)

func TestGetUserID(t *testing.T) {
	t.Run("returns user ID from context", func(t *testing.T) {
		info := &authctx.AuthInfo{UserID: "user-123", Username: "test@example.com"}
		ctx := authctx.SetAuthInfo(context.Background(), info)
		if got := authctx.GetUserID(ctx); got != "user-123" {
			t.Errorf("got %q, want %q", got, "user-123")
		}
	})

	t.Run("returns empty string when not set", func(t *testing.T) {
		if got := authctx.GetUserID(context.Background()); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestGetUsername(t *testing.T) {
	t.Run("returns username from context", func(t *testing.T) {
		info := &authctx.AuthInfo{UserID: "user-123", Username: "test@example.com"}
		ctx := authctx.SetAuthInfo(context.Background(), info)
		if got := authctx.GetUsername(ctx); got != "test@example.com" {
			t.Errorf("got %q, want %q", got, "test@example.com")
		}
	})

	t.Run("returns empty for service account", func(t *testing.T) {
		info := &authctx.AuthInfo{UserID: "hub-456", Username: ""}
		ctx := authctx.SetAuthInfo(context.Background(), info)
		if got := authctx.GetUsername(ctx); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestGetAuthInfo(t *testing.T) {
	t.Run("returns info from context", func(t *testing.T) {
		info := &authctx.AuthInfo{UserID: "user-123", Username: "test@example.com"}
		ctx := authctx.SetAuthInfo(context.Background(), info)
		got, ok := authctx.GetAuthInfo(ctx)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if got.UserID != "user-123" || got.Username != "test@example.com" {
			t.Errorf("unexpected info: %+v", got)
		}
	})

	t.Run("returns false when not set", func(t *testing.T) {
		_, ok := authctx.GetAuthInfo(context.Background())
		if ok {
			t.Error("expected ok=false")
		}
	})
}

func TestIsServiceAccount(t *testing.T) {
	t.Run("true for empty username", func(t *testing.T) {
		ctx := authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{UserID: "hub-1"})
		if !authctx.IsServiceAccount(ctx) {
			t.Error("expected true for service account")
		}
	})

	t.Run("false for user with username", func(t *testing.T) {
		ctx := authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{UserID: "u-1", Username: "alice"})
		if authctx.IsServiceAccount(ctx) {
			t.Error("expected false for regular user")
		}
	})

	t.Run("false when not set", func(t *testing.T) {
		if authctx.IsServiceAccount(context.Background()) {
			t.Error("expected false when context has no auth info")
		}
	})
}
```

**Step 3: Run tests**

```bash
go test ./internal/auth/context/... -v
```

Expected: All tests pass.

**Step 4: Commit**

```bash
git add internal/auth/context/
git commit -m "feat: add internal/auth/context subpackage (AuthInfo, context helpers)"
```

---

## Task 4: Update `internal/auth/` core files

Replace Zitadel-era files with the JWT implementation, updating imports to use the new subpackages.

**Files:**
- Replace: `internal/auth/models.go`
- Create: `internal/auth/service.go` (from worktree's `auth_service.go`)
- Replace: `internal/auth/middleware.go`
- Replace: `internal/auth/middleware_test.go`
- Create: `internal/auth/service_test.go` (from worktree's `auth_service_test.go`)
- Create: `internal/auth/models_test.go` (from worktree)
- Create: `internal/auth/testing.go`
- Create: `internal/auth/doc.go`

**Step 1: Write `internal/auth/models.go`**

Direct copy from worktree — no import changes needed:

```go
package auth

import "time"

// User represents an authenticated user account.
type User struct {
	ID           uint      `gorm:"primarykey"`
	Email        string    `gorm:"uniqueIndex;not null"`
	PasswordHash string    `gorm:"not null"`
	CreatedAt    time.Time `gorm:"autoCreateTime"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime"`
}

func (User) TableName() string { return "auth_users" }

// HubToken represents a long-lived service account token for a Hub device.
// Token values are hashed (SHA-256) before storage — shown only once on creation.
type HubToken struct {
	ID        uint      `gorm:"primarykey"`
	UserID    uint      `gorm:"not null;index"`
	User      User      `gorm:"foreignKey:UserID"`
	HubName   string    `gorm:"not null"`
	TokenHash string    `gorm:"not null"`
	Revoked   bool      `gorm:"default:false"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

func (HubToken) TableName() string { return "hub_tokens" }
```

**Step 2: Write `internal/auth/service.go`**

From worktree's `auth_service.go` — update the `JWTManager` import to use the new `authjwt` subpackage:

- Change `type AuthService struct { jwtManager *JWTManager }` → `jwtManager *authjwt.JWTManager`
- Change `func NewAuthService(db *gorm.DB, jwtManager *JWTManager)` → `jwtManager *authjwt.JWTManager`
- Add import: `authjwt "harvest-hub/api/internal/auth/jwt"`
- Keep all other logic identical to worktree

Key signature change:
```go
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"time"

	authjwt "harvest-hub/api/internal/auth/jwt"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type AuthService struct {
	db         *gorm.DB
	jwtManager *authjwt.JWTManager
}

func NewAuthService(db *gorm.DB, jwtManager *authjwt.JWTManager) *AuthService {
	return &AuthService{db: db, jwtManager: jwtManager}
}
// ... rest identical to worktree's auth_service.go
```

**Step 3: Write `internal/auth/middleware.go`**

Replace the Zitadel version entirely. Uses `authjwt` for token validation and `authctx` for context storage:

```go
package auth

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	authctx "harvest-hub/api/internal/auth/context"
	authjwt "harvest-hub/api/internal/auth/jwt"
)

// NewJWTAuthInterceptor returns a Connect interceptor that validates RS256 JWT tokens
// and enforces per-RPC authorization:
//   - InsertSensorData: service accounts (Hub devices) only
//   - All other endpoints: any valid token
func NewJWTAuthInterceptor(jwtManager *authjwt.JWTManager) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
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

// extractBearerToken parses "Bearer <token>" from an Authorization header.
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
```

**Step 4: Write `internal/auth/middleware_test.go`**

Adapt from worktree — update imports: `auth.JWTManager` → `authjwt.JWTManager`, `auth.GetUserID` → `authctx.GetUserID`:

```go
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
	token, err := h.manager.GenerateToken(userID, username, time.Hour)
	if err != nil {
		t.Fatalf("failed to generate user token: %v", err)
	}
	return token
}

func (h *testJWTHelper) generateServiceAccountToken(t *testing.T, serviceAccountID string) string {
	t.Helper()
	token, err := h.manager.GenerateToken(serviceAccountID, "", time.Hour)
	if err != nil {
		t.Fatalf("failed to generate service account token: %v", err)
	}
	return token
}

func (h *testJWTHelper) generateExpiredToken(t *testing.T, userID, username string) string {
	t.Helper()
	token, err := h.manager.GenerateToken(userID, username, time.Nanosecond)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	return token
}

// fakeRequest is a minimal connect.AnyRequest stub for testing.
type stubRequest struct {
	connect.AnyRequest
	procedure string
	headers   http.Header
}

func (r *stubRequest) Spec() connect.Spec       { return connect.Spec{Procedure: r.procedure} }
func (r *stubRequest) Header() http.Header      { return r.headers }
func (r *stubRequest) Peer() connect.Peer       { return connect.Peer{} }

func fakeRequest(procedure string, headers http.Header) connect.AnyRequest {
	return &stubRequest{procedure: procedure, headers: headers}
}

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
	h := newTestJWTHelper(t)
	interceptor := auth.NewJWTAuthInterceptor(h.manager)
	handler := interceptor(passthroughNext(&callRecord{}))

	_, err := handler(context.Background(), fakeRequest("/garden.v1.GardenService/GetSummary", http.Header{}))
	if err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("expected CodeUnauthenticated, got %v", err)
	}
}

func TestConnectInterceptor_InvalidToken(t *testing.T) {
	h := newTestJWTHelper(t)
	interceptor := auth.NewJWTAuthInterceptor(h.manager)
	handler := interceptor(passthroughNext(&callRecord{}))

	headers := http.Header{"Authorization": []string{"Bearer invalid_token"}}
	_, err := handler(context.Background(), fakeRequest("/garden.v1.GardenService/GetSummary", headers))
	if err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("expected CodeUnauthenticated, got %v", err)
	}
}

func TestConnectInterceptor_ExpiredToken(t *testing.T) {
	h := newTestJWTHelper(t)
	interceptor := auth.NewJWTAuthInterceptor(h.manager)
	handler := interceptor(passthroughNext(&callRecord{}))

	token := h.generateExpiredToken(t, "user-1", "alice")
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	_, err := handler(context.Background(), fakeRequest("/garden.v1.GardenService/GetSummary", headers))
	if err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("expected CodeUnauthenticated, got %v", err)
	}
}

func TestConnectInterceptor_InsertSensorData_Authorization(t *testing.T) {
	tests := []struct {
		name     string
		userID   string
		username string
		wantPass bool
		wantCode connect.Code
	}{
		{"service account allowed", "hub-1", "", true, 0},
		{"user rejected from insert", "user-1", "alice", false, connect.CodePermissionDenied},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestJWTHelper(t)
			interceptor := auth.NewJWTAuthInterceptor(h.manager)

			var token string
			if tt.username == "" {
				token = h.generateServiceAccountToken(t, tt.userID)
			} else {
				token = h.generateUserToken(t, tt.userID, tt.username)
			}

			rec := &callRecord{}
			handler := interceptor(passthroughNext(rec))
			headers := http.Header{"Authorization": []string{fmt.Sprintf("Bearer %s", token)}}
			_, err := handler(context.Background(), fakeRequest("/garden.v1.GardenService/InsertSensorData", headers))

			if tt.wantPass {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !rec.called {
					t.Fatal("expected next handler to be called")
				}
			} else {
				if err == nil || connect.CodeOf(err) != tt.wantCode {
					t.Errorf("expected %v, got %v", tt.wantCode, err)
				}
			}
		})
	}
}

func TestConnectInterceptor_SetsAuthContext(t *testing.T) {
	h := newTestJWTHelper(t)
	interceptor := auth.NewJWTAuthInterceptor(h.manager)
	token := h.generateUserToken(t, "user-42", "bob")

	rec := &callRecord{}
	handler := interceptor(passthroughNext(rec))
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	_, err := handler(context.Background(), fakeRequest("/garden.v1.GardenService/GetSummary", headers))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := authctx.GetUserID(rec.ctx); got != "user-42" {
		t.Errorf("GetUserID = %q, want %q", got, "user-42")
	}
	if got := authctx.GetUsername(rec.ctx); got != "bob" {
		t.Errorf("GetUsername = %q, want %q", got, "bob")
	}
	if authctx.IsServiceAccount(rec.ctx) {
		t.Error("IsServiceAccount = true, want false")
	}
}
```

**Step 5: Write `internal/auth/testing.go`**

Test helper used by service_test.go — replaces the old pgxpool-based one with GORM+SQLite:

```go
package auth

import (
	"context"
	"testing"

	authctx "harvest-hub/api/internal/auth/context"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// NewTestGORMDB creates an in-memory SQLite database with auth tables migrated.
// Use this in tests that need a database (AuthService tests, model tests).
func NewTestGORMDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	if err := db.AutoMigrate(&User{}, &HubToken{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	})
	return db
}

// CreateTestAuthContext creates a context with auth info, used by service layer tests.
func CreateTestAuthContext(userID, username string) context.Context {
	return authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{
		UserID:   userID,
		Username: username,
	})
}
```

**Step 6: Write `internal/auth/doc.go`**

```go
// Package auth provides user authentication and hub device token management
// for the Harvest Hub backend API.
//
// Sub-packages:
//   - auth/jwt  — JWT token generation, validation, and RSA key persistence
//   - auth/context — AuthInfo type and context storage/retrieval helpers
//
// This package contains: GORM models (User, HubToken), AuthService business
// logic (register, login, hub token CRUD), and the Connect RPC middleware
// (NewJWTAuthInterceptor).
package auth
```

**Step 7: Copy `models_test.go` and `service_test.go` from worktree**

From worktree `internal/auth/`:
- Copy `models_test.go` → current branch `internal/auth/models_test.go`
  - Update any `setupTestDB` calls to `auth.NewTestGORMDB(t)` (or just `NewTestGORMDB(t)` if internal package)
- Copy `auth_service_test.go` → current branch `internal/auth/service_test.go`
  - Same substitution for DB setup
  - Remove any import of pgxpool

**Step 8: Run auth package tests**

```bash
go test ./internal/auth/... -v -count=1
```

Expected: All tests pass. Coverage should be near 80%+.

**Step 9: Commit**

```bash
git add internal/auth/
git commit -m "feat: replace Zitadel with JWT auth, add jwt/ and context/ subpackages"
```

---

## Task 5: Update `internal/service/` layer

Add the auth RPC handlers and update `GetUserID` calls to use `authctx`.

**Files:**
- Create: `internal/service/auth_proto.go` (placeholder types)
- Create: `internal/service/auth_service.go` (RPC handlers)
- Create: `internal/service/auth_service_test.go`
- Create: `internal/service/doc.go`

**Step 1: Write `internal/service/auth_proto.go`**

Direct copy from worktree — no import changes needed. Keep the TODO note explaining it should be replaced with buf-generated code after the protos repo is updated.

**Step 2: Write `internal/service/auth_service.go`**

Copy from worktree — one change: `auth.GetUserID(ctx)` → `authctx.GetUserID(ctx)`:

```go
package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"harvest-hub/api/internal/auth"
	authctx "harvest-hub/api/internal/auth/context"
)

type AuthService struct {
	authService *auth.AuthService
}

func NewAuthService(authService *auth.AuthService) *AuthService {
	return &AuthService{authService: authService}
}

// Register, Login — unchanged from worktree (no context extraction needed)

// CreateHubToken, ListHubTokens, RevokeHubToken — change:
//   auth.GetUserID(ctx) → authctx.GetUserID(ctx)
```

**Step 3: Copy `auth_service_test.go` from worktree**

From worktree `internal/service/auth_service_test.go` → current branch same path.

Update imports: any `auth.GetUserID` → `authctx.GetUserID`, add authctx import.

**Step 4: Write `internal/service/doc.go`**

Copy from worktree.

**Step 5: Run service tests**

```bash
go test ./internal/service/... -v -count=1
```

Expected: All tests pass.

**Step 6: Commit**

```bash
git add internal/service/
git commit -m "feat: add auth RPC service handlers (Register, Login, hub token management)"
```

---

## Task 6: Update `server/main.go`

Replace the Zitadel-based entry point with the JWT version.

**Files:**
- Replace: `server/main.go`

**Step 1: Write `server/main.go`**

Copy from worktree — it's complete and correct. Key changes from current branch:
- Two DB connections: `pgxpool` for GardenService, `gorm.Open` for AuthService
- `auth.NewOrLoadJWTManager(".")` for key persistence
- `auth.NewJWTAuthInterceptor(jwtManager)` instead of Zitadel
- Registers both GardenService and AuthService endpoints
- Removes all Zitadel env var requirements

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"connectrpc.com/connect"
	"github.com/harvesthub-gardening-tool/protos-go/garden/v1/gardenv1connect"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"harvest-hub/api/internal/auth"
	authjwt "harvest-hub/api/internal/auth/jwt"
	"harvest-hub/api/internal/service"
)

func main() {
	ctx := context.Background()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@db:5432/garden_db?sslmode=disable"
	}

	// pgxpool for GardenService (TimescaleDB time-series queries)
	pgxDB, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v", err)
	}
	defer pgxDB.Close()

	// GORM for AuthService (user + hub token management)
	gormDB, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect GORM: %v", err)
	}
	if err := gormDB.AutoMigrate(&auth.User{}, &auth.HubToken{}); err != nil {
		log.Fatalf("Failed to migrate auth tables: %v", err)
	}

	// JWT keys persist to disk — critical for 1-year hub tokens surviving restarts
	keyPath := os.Getenv("JWT_KEY_PATH")
	if keyPath == "" {
		keyPath = "."
	}
	jwtManager, err := authjwt.NewOrLoadJWTManager(keyPath)
	if err != nil {
		log.Fatalf("Failed to initialize JWT manager: %v", err)
	}

	authService := auth.NewAuthService(gormDB, jwtManager)
	authInterceptor := auth.NewJWTAuthInterceptor(jwtManager)

	gardenSvc := service.NewGardenService(pgxDB)
	authSvc := service.NewAuthService(authService)

	mux := http.NewServeMux()

	gardenPath, gardenHandler := gardenv1connect.NewGardenServiceHandler(
		gardenSvc,
		connect.WithInterceptors(authInterceptor),
	)
	mux.Handle(gardenPath, gardenHandler)
	registerAuthEndpoints(mux, authSvc, authInterceptor)

	addr := ":8080"
	fmt.Printf("✅ Garden API listening on %s\n", addr)
	fmt.Printf("🔐 Authentication: JWT RS256 | User tokens: 24h | Hub tokens: 1yr\n")

	if err := http.ListenAndServe(addr, h2c.NewHandler(cors(mux), &http2.Server{})); err != nil {
		log.Fatalf("❌ Server failed: %v", err)
	}
}

func registerAuthEndpoints(mux *http.ServeMux, authSvc *service.AuthService, authInterceptor connect.UnaryInterceptorFunc) {
	// Register and Login require NO authentication
	mux.Handle("/auth.v1.AuthService/Register", connect.NewUnaryHandler(
		"/auth.v1.AuthService/Register", authSvc.Register,
	))
	mux.Handle("/auth.v1.AuthService/Login", connect.NewUnaryHandler(
		"/auth.v1.AuthService/Login", authSvc.Login,
	))

	// Hub token endpoints REQUIRE authentication
	mux.Handle("/auth.v1.AuthService/CreateHubToken", connect.NewUnaryHandler(
		"/auth.v1.AuthService/CreateHubToken", authSvc.CreateHubToken,
		connect.WithInterceptors(authInterceptor),
	))
	mux.Handle("/auth.v1.AuthService/ListHubTokens", connect.NewUnaryHandler(
		"/auth.v1.AuthService/ListHubTokens", authSvc.ListHubTokens,
		connect.WithInterceptors(authInterceptor),
	))
	mux.Handle("/auth.v1.AuthService/RevokeHubToken", connect.NewUnaryHandler(
		"/auth.v1.AuthService/RevokeHubToken", authSvc.RevokeHubToken,
		connect.WithInterceptors(authInterceptor),
	))
}

func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		h.ServeHTTP(w, r)
	})
}
```

**Step 2: Build to verify no compile errors**

```bash
go build ./...
```

Expected: Clean build, no errors.

**Step 3: Run all tests**

```bash
go test ./... -count=1
```

Expected: All tests pass.

**Step 4: Commit**

```bash
git add server/main.go
git commit -m "feat: update server entrypoint for JWT auth (remove Zitadel)"
```

---

## Task 7: Add `auth/v1/auth.proto` to the protos repo

**Files:**
- Create: `/home/ewan/projets/Harvest-Hub/protos/auth/v1/auth.proto`

**Step 1: Create the proto directory**

```bash
mkdir -p /home/ewan/projets/Harvest-Hub/protos/auth/v1
```

**Step 2: Write the proto file**

```protobuf
syntax = "proto3";

package auth.v1;

option go_package = "github.com/harvesthub-gardening-tool/protos-go/auth/v1;authv1";

// AuthService provides user registration, login, and Hub device token management.
// Hub tokens are long-lived service account tokens for IoT devices (ESP32-S3).
service AuthService {
  // Register creates a new user account and returns an immediate login token.
  rpc Register(RegisterRequest) returns (RegisterResponse);

  // Login validates credentials and returns a JWT token (24h expiry).
  rpc Login(LoginRequest) returns (LoginResponse);

  // CreateHubToken creates a long-lived token (1yr) for a named Hub device.
  // The token is shown only once — store it securely on the device.
  // Requires user authentication.
  rpc CreateHubToken(CreateHubTokenRequest) returns (CreateHubTokenResponse);

  // ListHubTokens returns metadata for all active (non-revoked) Hub tokens
  // belonging to the authenticated user. Token values are not returned.
  // Requires user authentication.
  rpc ListHubTokens(ListHubTokensRequest) returns (ListHubTokensResponse);

  // RevokeHubToken marks a Hub token as revoked. The device using that token
  // will no longer be able to insert sensor data.
  // Requires user authentication.
  rpc RevokeHubToken(RevokeHubTokenRequest) returns (RevokeHubTokenResponse);
}

message RegisterRequest {
  string email = 1;
  string password = 2;
}

message RegisterResponse {
  string user_id = 1;
  string token = 2; // JWT, 24h expiry
}

message LoginRequest {
  string email = 1;
  string password = 2;
}

message LoginResponse {
  string token = 1; // JWT, 24h expiry
}

message CreateHubTokenRequest {
  string hub_name = 1; // Unique name for this Hub device (e.g. "garden-hub-1")
}

message CreateHubTokenResponse {
  string token = 1; // JWT, 1yr expiry — display once, store on device
}

message ListHubTokensRequest {}

message HubTokenInfo {
  string id = 1;
  string hub_name = 2;
  int64 created_at = 3;  // Unix milliseconds
  bool revoked = 4;
}

message ListHubTokensResponse {
  repeated HubTokenInfo tokens = 1;
}

message RevokeHubTokenRequest {
  string token_id = 1;
}

message RevokeHubTokenResponse {}
```

**Step 3: Commit to the protos repo**

```bash
cd /home/ewan/projets/Harvest-Hub/protos
git add auth/v1/auth.proto
git commit -m "feat: add auth/v1/auth.proto (Register, Login, hub token management)"
```

---

## Task 8: Update CLAUDE.md

**Files:**
- Modify: `/home/ewan/projets/Harvest-Hub/backend-api/CLAUDE.md`

**Step 1: Update the Architecture section**

Replace all Zitadel references with JWT auth. Key changes:
- Architecture line: `Zitadel authentication` → `JWT-based authentication`
- Authentication Model section: replace Zitadel description with JWT description (two token types: user 24h, hub 1yr)
- Docker Services table: remove Zitadel + login rows, keep api/db/swagger-ui
- Environment Variables table: remove `ZITADEL_DOMAIN`, `ZITADEL_CLIENT_ID`, `HUB_SERVICE_ACCOUNT_ID`; add `JWT_KEY_PATH`
- Build & Run section: update `docker-compose up` description (no more Zitadel services)

**Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md to reflect JWT auth (remove Zitadel)"
```

---

## Task 9: Delete stray MIGRATION_SUMMARY.md

**Files:**
- Delete: `/home/ewan/projets/Harvest-Hub/backend-api/.worktrees/authboss-migration/MIGRATION_SUMMARY.md`

**Step 1: Delete the file**

```bash
rm /home/ewan/projets/Harvest-Hub/backend-api/.worktrees/authboss-migration/MIGRATION_SUMMARY.md
```

**Step 2: Commit in the worktree**

```bash
cd /home/ewan/projets/Harvest-Hub/backend-api/.worktrees/authboss-migration
git add -A
git commit -m "chore: remove stray MIGRATION_SUMMARY.md"
```

Note: This commit lands on `feature/authboss-migration`. Since that branch is no longer needed, this just cleans it up before eventual deletion.

---

## Task 10: Final verification + cleanup commit

**Step 1: Run full test suite**

```bash
cd /home/ewan/projets/Harvest-Hub/backend-api
go test ./... -count=1 -cover
```

Expected: All tests pass. Coverage should be 80%+ across auth packages.

**Step 2: Check coverage per package**

```bash
go test ./internal/auth/... -coverprofile=coverage.out && go tool cover -func=coverage.out
```

**Step 3: Build the binary**

```bash
go build -o bin/api ./server
```

Expected: Clean build.

**Step 4: Final commit on feature/auth**

```bash
cd /home/ewan/projets/Harvest-Hub/backend-api
git add .
git status  # verify nothing unexpected
git commit -m "chore: add docs/plans and coverage artifacts to gitignore if needed"
```

---

## Summary of final file structure

```
internal/
├── auth/
│   ├── jwt/                    # package authjwt — JWT crypto
│   │   ├── claims.go           # Claims struct
│   │   ├── manager.go          # JWTManager, token ops, key persistence
│   │   └── jwt_test.go
│   ├── context/                # package authctx — context helpers
│   │   ├── context.go          # AuthInfo, SetAuthInfo, GetUserID, etc.
│   │   └── context_test.go
│   ├── models.go               # GORM: User, HubToken
│   ├── models_test.go
│   ├── service.go              # AuthService business logic
│   ├── service_test.go
│   ├── middleware.go           # Connect RPC interceptor
│   ├── middleware_test.go
│   ├── testing.go              # NewTestGORMDB, CreateTestAuthContext
│   └── doc.go
└── service/
    ├── auth_proto.go           # Placeholder types (TODO: replace with buf-generated)
    ├── auth_service.go         # Auth RPC handlers
    ├── auth_service_test.go
    ├── garden.go
    ├── garden_test.go
    └── doc.go

protos/
└── auth/
    └── v1/
        └── auth.proto          # Auth service definition
```
