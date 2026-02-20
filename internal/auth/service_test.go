package auth

import (
	"context"
	"fmt"
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

	if err := db.AutoMigrate(&User{}, &HubToken{}); err != nil {
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

// ============================================================================
// CreateHubToken Tests
// ============================================================================

func TestAuthService_CreateHubToken(t *testing.T) {
	service, jwtManager := setupTestService(t)
	ctx := context.Background()

	userID, err := service.RegisterUser(ctx, "hub@example.com", "password123")
	require.NoError(t, err)

	t.Run("successfully creates hub token", func(t *testing.T) {
		hubName := "my-garden-hub"

		token, err := service.CreateHubToken(ctx, userID, hubName)
		require.NoError(t, err)
		assert.NotEmpty(t, token)

		claims, err := jwtManager.ValidateToken(token)
		require.NoError(t, err)
		assert.Equal(t, userID, claims.UserID)
		assert.Empty(t, claims.Username)
		assert.True(t, claims.IsServiceAccount())

		var hubToken HubToken
		err = service.db.Where("hub_name = ?", hubName).First(&hubToken).Error
		require.NoError(t, err)
		assert.Equal(t, hubName, hubToken.HubName)
		assert.NotEmpty(t, hubToken.TokenHash)
		assert.False(t, hubToken.Revoked)
	})

	t.Run("returns error for duplicate hub name", func(t *testing.T) {
		hubName := "duplicate-hub"

		_, err := service.CreateHubToken(ctx, userID, hubName)
		require.NoError(t, err)

		token, err := service.CreateHubToken(ctx, userID, hubName)
		assert.Error(t, err)
		assert.Empty(t, token)
		assert.ErrorIs(t, err, ErrDuplicateHubName)
	})

	t.Run("returns error for empty hub name", func(t *testing.T) {
		token, err := service.CreateHubToken(ctx, userID, "")
		assert.Error(t, err)
		assert.Empty(t, token)
		assert.Contains(t, err.Error(), "hub name")
	})

	t.Run("returns error for empty user ID", func(t *testing.T) {
		token, err := service.CreateHubToken(ctx, "", "test-hub")
		assert.Error(t, err)
		assert.Empty(t, token)
		assert.Contains(t, err.Error(), "userID")
	})

	t.Run("allows same hub name for different users", func(t *testing.T) {
		userID2, err := service.RegisterUser(ctx, "hub2@example.com", "password123")
		require.NoError(t, err)

		hubName := "shared-name"

		token1, err := service.CreateHubToken(ctx, userID, hubName)
		require.NoError(t, err)
		assert.NotEmpty(t, token1)

		token2, err := service.CreateHubToken(ctx, userID2, hubName)
		require.NoError(t, err)
		assert.NotEmpty(t, token2)
	})

	t.Run("hub token has correct expiry (1 year)", func(t *testing.T) {
		before := time.Now()
		token, err := service.CreateHubToken(ctx, userID, "long-lived-hub")
		require.NoError(t, err)
		after := time.Now()

		claims, err := jwtManager.ValidateToken(token)
		require.NoError(t, err)

		expectedExpiry := before.Add(hubTokenExpiry)
		actualExpiry := claims.ExpiresAt.Time

		assert.WithinDuration(t, expectedExpiry, actualExpiry, 2*time.Second)
		assert.True(t, actualExpiry.After(after.Add(364*24*time.Hour)))
	})

	t.Run("token hash is SHA-256 of raw token", func(t *testing.T) {
		hubName := "hash-test-hub"
		_, err := service.CreateHubToken(ctx, userID, hubName)
		require.NoError(t, err)

		var hubToken HubToken
		err = service.db.Where("hub_name = ?", hubName).First(&hubToken).Error
		require.NoError(t, err)

		assert.Len(t, hubToken.TokenHash, 64)
	})
}

// ============================================================================
// ListHubTokens Tests
// ============================================================================

func TestAuthService_ListHubTokens(t *testing.T) {
	service, _ := setupTestService(t)
	ctx := context.Background()

	userID, err := service.RegisterUser(ctx, "list@example.com", "password123")
	require.NoError(t, err)

	t.Run("returns empty list for user with no tokens", func(t *testing.T) {
		tokens, err := service.ListHubTokens(ctx, userID)
		require.NoError(t, err)
		assert.Empty(t, tokens)
	})

	t.Run("returns all active tokens for user", func(t *testing.T) {
		_, err := service.CreateHubToken(ctx, userID, "hub-1")
		require.NoError(t, err)
		_, err = service.CreateHubToken(ctx, userID, "hub-2")
		require.NoError(t, err)
		_, err = service.CreateHubToken(ctx, userID, "hub-3")
		require.NoError(t, err)

		tokens, err := service.ListHubTokens(ctx, userID)
		require.NoError(t, err)
		assert.Len(t, tokens, 3)

		hubNames := make([]string, len(tokens))
		for i, token := range tokens {
			hubNames[i] = token.HubName
			assert.Equal(t, userID, token.UserID)
			assert.False(t, token.Revoked)
			assert.NotEmpty(t, token.ID)
			assert.NotZero(t, token.CreatedAt)
		}

		assert.Contains(t, hubNames, "hub-1")
		assert.Contains(t, hubNames, "hub-2")
		assert.Contains(t, hubNames, "hub-3")
	})

	t.Run("excludes revoked tokens", func(t *testing.T) {
		userID2, err := service.RegisterUser(ctx, "revoked@example.com", "password123")
		require.NoError(t, err)

		_, err = service.CreateHubToken(ctx, userID2, "active-hub")
		require.NoError(t, err)
		_, err = service.CreateHubToken(ctx, userID2, "revoked-hub")
		require.NoError(t, err)

		var hubToken HubToken
		err = service.db.Where("hub_name = ?", "revoked-hub").First(&hubToken).Error
		require.NoError(t, err)

		err = service.RevokeHubToken(ctx, userID2, fmt.Sprint(hubToken.ID))
		require.NoError(t, err)

		tokens, err := service.ListHubTokens(ctx, userID2)
		require.NoError(t, err)
		assert.Len(t, tokens, 1)
		assert.Equal(t, "active-hub", tokens[0].HubName)
	})

	t.Run("returns error for empty user ID", func(t *testing.T) {
		tokens, err := service.ListHubTokens(ctx, "")
		assert.Error(t, err)
		assert.Nil(t, tokens)
		assert.Contains(t, err.Error(), "userID")
	})

	t.Run("returns tokens in descending created_at order", func(t *testing.T) {
		userID3, err := service.RegisterUser(ctx, "ordered@example.com", "password123")
		require.NoError(t, err)

		_, err = service.CreateHubToken(ctx, userID3, "first")
		require.NoError(t, err)
		time.Sleep(10 * time.Millisecond)

		_, err = service.CreateHubToken(ctx, userID3, "second")
		require.NoError(t, err)
		time.Sleep(10 * time.Millisecond)

		_, err = service.CreateHubToken(ctx, userID3, "third")
		require.NoError(t, err)

		tokens, err := service.ListHubTokens(ctx, userID3)
		require.NoError(t, err)
		assert.Len(t, tokens, 3)

		assert.Equal(t, "third", tokens[0].HubName)
		assert.Equal(t, "second", tokens[1].HubName)
		assert.Equal(t, "first", tokens[2].HubName)
	})
}

// ============================================================================
// RevokeHubToken Tests
// ============================================================================

func TestAuthService_RevokeHubToken(t *testing.T) {
	service, _ := setupTestService(t)
	ctx := context.Background()

	userID, err := service.RegisterUser(ctx, "revoke@example.com", "password123")
	require.NoError(t, err)

	t.Run("successfully revokes hub token", func(t *testing.T) {
		_, err := service.CreateHubToken(ctx, userID, "revoke-test")
		require.NoError(t, err)

		var hubToken HubToken
		err = service.db.Where("hub_name = ?", "revoke-test").First(&hubToken).Error
		require.NoError(t, err)

		tokenID := fmt.Sprint(hubToken.ID)

		err = service.RevokeHubToken(ctx, userID, tokenID)
		require.NoError(t, err)

		var updatedToken HubToken
		err = service.db.Where("id = ?", hubToken.ID).First(&updatedToken).Error
		require.NoError(t, err)
		assert.True(t, updatedToken.Revoked)
	})

	t.Run("returns error for non-existent token", func(t *testing.T) {
		err := service.RevokeHubToken(ctx, userID, "99999")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrHubTokenNotFound)
	})

	t.Run("returns error when revoking already revoked token", func(t *testing.T) {
		_, err := service.CreateHubToken(ctx, userID, "double-revoke")
		require.NoError(t, err)

		var hubToken HubToken
		err = service.db.Where("hub_name = ?", "double-revoke").First(&hubToken).Error
		require.NoError(t, err)
		tokenID := fmt.Sprint(hubToken.ID)

		err = service.RevokeHubToken(ctx, userID, tokenID)
		require.NoError(t, err)

		err = service.RevokeHubToken(ctx, userID, tokenID)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrHubTokenNotFound)
	})

	t.Run("returns error for empty user ID", func(t *testing.T) {
		err := service.RevokeHubToken(ctx, "", "123")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "userID")
	})

	t.Run("returns error for empty token ID", func(t *testing.T) {
		err := service.RevokeHubToken(ctx, userID, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "hub token ID")
	})

	t.Run("user cannot revoke another user's token", func(t *testing.T) {
		userID1, err := service.RegisterUser(ctx, "user1@example.com", "password123")
		require.NoError(t, err)
		userID2, err := service.RegisterUser(ctx, "user2@example.com", "password123")
		require.NoError(t, err)

		_, err = service.CreateHubToken(ctx, userID1, "user1-hub")
		require.NoError(t, err)

		var hubToken HubToken
		err = service.db.Where("hub_name = ?", "user1-hub").First(&hubToken).Error
		require.NoError(t, err)
		tokenID := fmt.Sprint(hubToken.ID)

		err = service.RevokeHubToken(ctx, userID2, tokenID)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrHubTokenNotFound)

		var stillActiveToken HubToken
		err = service.db.Where("id = ?", hubToken.ID).First(&stillActiveToken).Error
		require.NoError(t, err)
		assert.False(t, stillActiveToken.Revoked)
	})

	t.Run("allows reusing hub name after revocation", func(t *testing.T) {
		userID3, err := service.RegisterUser(ctx, "reuse@example.com", "password123")
		require.NoError(t, err)

		hubName := "reusable-name"

		_, err = service.CreateHubToken(ctx, userID3, hubName)
		require.NoError(t, err)

		var hubToken HubToken
		err = service.db.Where("hub_name = ? AND user_id = ?", hubName, userID3).First(&hubToken).Error
		require.NoError(t, err)

		err = service.RevokeHubToken(ctx, userID3, fmt.Sprint(hubToken.ID))
		require.NoError(t, err)

		token2, err := service.CreateHubToken(ctx, userID3, hubName)
		require.NoError(t, err)
		assert.NotEmpty(t, token2)
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
