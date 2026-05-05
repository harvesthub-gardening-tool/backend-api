package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	authjwt "harvest-hub/api/internal/auth/jwt"
)

// setupTestService creates a test AuthService with an in-memory SQLite database.
func setupTestService(t *testing.T) (*AuthService, *authjwt.JWTManager) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}

	if err := db.AutoMigrate(&User{}, &HubToken{}, &Hub{}); err != nil {
		t.Fatalf("failed to migrate database: %v", err)
	}

	jwtManager, err := authjwt.NewJWTManager()
	if err != nil {
		t.Fatalf("failed to create JWT manager: %v", err)
	}

	service := NewAuthService(db, jwtManager)
	return service, jwtManager
}

// ============================================================================
// RegisterUser Tests
// ============================================================================

func TestAuthService_RegisterUser(t *testing.T) {
	service, _ := setupTestService(t)
	ctx := context.Background()

	t.Run("successfully registers a new user", func(t *testing.T) {
		email := "test@example.com"
		password := "password123"

		userID, err := service.RegisterUser(ctx, email, password)
		require.NoError(t, err)
		assert.NotEmpty(t, userID)

		var user User
		err = service.db.Where("email = ?", email).First(&user).Error
		require.NoError(t, err)
		assert.Equal(t, email, user.Email)
		assert.NotEmpty(t, user.PasswordHash)
		assert.NotEqual(t, password, user.PasswordHash)
	})

	t.Run("returns error for duplicate email", func(t *testing.T) {
		email := "duplicate@example.com"
		password := "password123"

		_, err := service.RegisterUser(ctx, email, password)
		require.NoError(t, err)

		userID, err := service.RegisterUser(ctx, email, password)
		assert.Error(t, err)
		assert.Empty(t, userID)
		assert.ErrorIs(t, err, ErrDuplicateEmail)
	})

	t.Run("returns error for weak password (< 8 chars)", func(t *testing.T) {
		email := "weakpass@example.com"
		password := "short"

		userID, err := service.RegisterUser(ctx, email, password)
		assert.Error(t, err)
		assert.Empty(t, userID)
		assert.ErrorIs(t, err, ErrWeakPassword)
	})

	t.Run("returns error for invalid email format", func(t *testing.T) {
		email := "not-an-email"
		password := "password123"

		userID, err := service.RegisterUser(ctx, email, password)
		assert.Error(t, err)
		assert.Empty(t, userID)
		assert.ErrorIs(t, err, ErrInvalidEmail)
	})

	t.Run("returns error for empty email", func(t *testing.T) {
		userID, err := service.RegisterUser(ctx, "", "password123")
		assert.Error(t, err)
		assert.Empty(t, userID)
		assert.Contains(t, err.Error(), "email")
	})

	t.Run("returns error for empty password", func(t *testing.T) {
		userID, err := service.RegisterUser(ctx, "test2@example.com", "")
		assert.Error(t, err)
		assert.Empty(t, userID)
		assert.Contains(t, err.Error(), "password")
	})

	t.Run("accepts password with exactly 8 characters", func(t *testing.T) {
		userID, err := service.RegisterUser(ctx, "min-pass@example.com", "12345678")
		require.NoError(t, err)
		assert.NotEmpty(t, userID)
	})

	t.Run("accepts valid email variations", func(t *testing.T) {
		testCases := []struct{ email string }{
			{"user+tag@example.com"},
			{"user.name@example.co.uk"},
			{"123@example.com"},
		}

		for _, tc := range testCases {
			t.Run(tc.email, func(t *testing.T) {
				userID, err := service.RegisterUser(ctx, tc.email, "password123")
				require.NoError(t, err)
				assert.NotEmpty(t, userID)
			})
		}
	})
}

// ============================================================================
// LoginUser Tests
// ============================================================================

func TestAuthService_LoginUser(t *testing.T) {
	service, jwtManager := setupTestService(t)
	ctx := context.Background()

	testEmail := "login@example.com"
	testPassword := "password123"
	userID, err := service.RegisterUser(ctx, testEmail, testPassword)
	require.NoError(t, err)

	t.Run("successfully logs in with correct credentials", func(t *testing.T) {
		token, err := service.LoginUser(ctx, testEmail, testPassword)
		require.NoError(t, err)
		assert.NotEmpty(t, token)

		claims, err := jwtManager.ValidateToken(token)
		require.NoError(t, err)
		assert.Equal(t, userID, claims.UserID)
		assert.Equal(t, testEmail, claims.Username)
		assert.False(t, claims.IsServiceAccount())
	})

	t.Run("returns error for incorrect password", func(t *testing.T) {
		token, err := service.LoginUser(ctx, testEmail, "wrongpassword")
		assert.Error(t, err)
		assert.Empty(t, token)
		assert.ErrorIs(t, err, ErrInvalidCredentials)
	})

	t.Run("returns error for non-existent user", func(t *testing.T) {
		token, err := service.LoginUser(ctx, "nonexistent@example.com", "password123")
		assert.Error(t, err)
		assert.Empty(t, token)
		assert.ErrorIs(t, err, ErrInvalidCredentials)
	})

	t.Run("returns error for empty email", func(t *testing.T) {
		token, err := service.LoginUser(ctx, "", testPassword)
		assert.Error(t, err)
		assert.Empty(t, token)
		assert.Contains(t, err.Error(), "email")
	})

	t.Run("returns error for empty password", func(t *testing.T) {
		token, err := service.LoginUser(ctx, testEmail, "")
		assert.Error(t, err)
		assert.Empty(t, token)
		assert.Contains(t, err.Error(), "password")
	})

	t.Run("token has correct expiry (24 hours)", func(t *testing.T) {
		before := time.Now()
		token, err := service.LoginUser(ctx, testEmail, testPassword)
		require.NoError(t, err)
		after := time.Now()

		claims, err := jwtManager.ValidateToken(token)
		require.NoError(t, err)

		expectedExpiry := before.Add(userTokenExpiry)
		actualExpiry := claims.ExpiresAt.Time

		assert.WithinDuration(t, expectedExpiry, actualExpiry, 2*time.Second)
		assert.True(t, actualExpiry.After(after))
		assert.True(t, actualExpiry.Before(after.Add(25*time.Hour)))
	})

	t.Run("multiple successful logins generate different tokens", func(t *testing.T) {
		token1, err := service.LoginUser(ctx, testEmail, testPassword)
		require.NoError(t, err)

		time.Sleep(1 * time.Second)

		token2, err := service.LoginUser(ctx, testEmail, testPassword)
		require.NoError(t, err)

		assert.NotEqual(t, token1, token2)

		claims1, err := jwtManager.ValidateToken(token1)
		require.NoError(t, err)
		claims2, err := jwtManager.ValidateToken(token2)
		require.NoError(t, err)

		assert.Equal(t, claims1.UserID, claims2.UserID)
		assert.Equal(t, claims1.Username, claims2.Username)
	})
}

func TestAuthService_ChangeEmail(t *testing.T) {
	service, jwtManager := setupTestService(t)
	ctx := context.Background()

	userID, err := service.RegisterUser(ctx, "email-change@example.com", "password123")
	require.NoError(t, err)
	_, err = service.RegisterUser(ctx, "taken@example.com", "password123")
	require.NoError(t, err)

	t.Run("updates email and returns refreshed token", func(t *testing.T) {
		token, err := service.ChangeEmail(ctx, userID, "updated-email@example.com", "password123")
		require.NoError(t, err)
		assert.NotEmpty(t, token)

		var user User
		require.NoError(t, service.db.First(&user, userID).Error)
		assert.Equal(t, "updated-email@example.com", user.Email)

		claims, err := jwtManager.ValidateToken(token)
		require.NoError(t, err)
		assert.Equal(t, userID, claims.UserID)
		assert.Equal(t, "updated-email@example.com", claims.Username)
	})

	t.Run("rejects invalid email", func(t *testing.T) {
		token, err := service.ChangeEmail(ctx, userID, "not-an-email", "password123")
		assert.ErrorIs(t, err, ErrInvalidEmail)
		assert.Empty(t, token)
	})

	t.Run("rejects duplicate email", func(t *testing.T) {
		token, err := service.ChangeEmail(ctx, userID, "taken@example.com", "password123")
		assert.ErrorIs(t, err, ErrDuplicateEmail)
		assert.Empty(t, token)
	})

	t.Run("rejects incorrect current password", func(t *testing.T) {
		token, err := service.ChangeEmail(ctx, userID, "another-email@example.com", "wrongpassword")
		assert.ErrorIs(t, err, ErrInvalidCredentials)
		assert.Empty(t, token)
	})
}

func TestAuthService_ChangePassword(t *testing.T) {
	service, _ := setupTestService(t)
	ctx := context.Background()

	userID, err := service.RegisterUser(ctx, "password-change@example.com", "password123")
	require.NoError(t, err)

	t.Run("updates password hash", func(t *testing.T) {
		require.NoError(t, service.ChangePassword(ctx, userID, "password123", "newpass123"))

		_, err := service.LoginUser(ctx, "password-change@example.com", "password123")
		assert.ErrorIs(t, err, ErrInvalidCredentials)

		token, err := service.LoginUser(ctx, "password-change@example.com", "newpass123")
		require.NoError(t, err)
		assert.NotEmpty(t, token)
	})

	t.Run("rejects weak new password", func(t *testing.T) {
		err := service.ChangePassword(ctx, userID, "newpass123", "short")
		assert.ErrorIs(t, err, ErrWeakPassword)
	})

	t.Run("rejects incorrect current password", func(t *testing.T) {
		err := service.ChangePassword(ctx, userID, "wrongpassword", "another123")
		assert.ErrorIs(t, err, ErrInvalidCredentials)
	})
}

// ============================================================================
// Helper Functions Tests
// ============================================================================

func TestIsUniqueViolation(t *testing.T) {
	t.Run("returns true for GORM duplicate key error", func(t *testing.T) {
		result := isUniqueViolation(gorm.ErrDuplicatedKey)
		assert.True(t, result)
	})

	t.Run("returns false for nil error", func(t *testing.T) {
		result := isUniqueViolation(nil)
		assert.False(t, result)
	})

	t.Run("returns false for unrelated error", func(t *testing.T) {
		result := isUniqueViolation(assert.AnError)
		assert.False(t, result)
	})
}

func TestContainsString(t *testing.T) {
	t.Run("finds substring in string", func(t *testing.T) {
		result := containsString("duplicate key violation", "duplicate key")
		assert.True(t, result)
	})

	t.Run("returns false when substring not found", func(t *testing.T) {
		result := containsString("some error", "duplicate")
		assert.False(t, result)
	})

	t.Run("handles exact match", func(t *testing.T) {
		result := containsString("test", "test")
		assert.True(t, result)
	})

	t.Run("handles empty substring", func(t *testing.T) {
		result := containsString("test", "")
		assert.True(t, result)
	})
}
