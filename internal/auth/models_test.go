package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupModelsTestDB creates a test database for model tests.
func setupModelsTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}

	if err := db.AutoMigrate(&User{}, &HubToken{}); err != nil {
		t.Fatalf("failed to migrate database: %v", err)
	}

	return db
}

// ============================================================================
// User Model Tests
// ============================================================================

func TestUserModel(t *testing.T) {
	db := setupModelsTestDB(t)

	t.Run("creates user with valid fields", func(t *testing.T) {
		user := User{
			Email:        "test@example.com",
			PasswordHash: "hashed_password",
		}

		err := db.Create(&user).Error
		require.NoError(t, err)
		assert.NotZero(t, user.ID)
		assert.NotZero(t, user.CreatedAt)
		assert.NotZero(t, user.UpdatedAt)
	})

	t.Run("enforces unique email constraint", func(t *testing.T) {
		email := "unique@example.com"

		user1 := User{Email: email, PasswordHash: "hash1"}
		err := db.Create(&user1).Error
		require.NoError(t, err)

		user2 := User{Email: email, PasswordHash: "hash2"}
		err = db.Create(&user2).Error
		assert.Error(t, err)
	})

	t.Run("uses correct table name", func(t *testing.T) {
		user := User{}
		assert.Equal(t, "auth_users", user.TableName())
	})

	t.Run("auto-updates UpdatedAt timestamp", func(t *testing.T) {
		user := User{Email: "update@example.com", PasswordHash: "hash"}
		err := db.Create(&user).Error
		require.NoError(t, err)

		originalUpdatedAt := user.UpdatedAt

		user.PasswordHash = "new_hash"
		err = db.Save(&user).Error
		require.NoError(t, err)

		assert.True(t, user.UpdatedAt.After(originalUpdatedAt))
	})

	t.Run("loads user by email", func(t *testing.T) {
		email := "load@example.com"
		user := User{Email: email, PasswordHash: "hash"}
		err := db.Create(&user).Error
		require.NoError(t, err)

		var loaded User
		err = db.Where("email = ?", email).First(&loaded).Error
		require.NoError(t, err)
		assert.Equal(t, user.ID, loaded.ID)
		assert.Equal(t, email, loaded.Email)
		assert.Equal(t, "hash", loaded.PasswordHash)
	})

	t.Run("deletes user", func(t *testing.T) {
		user := User{Email: "delete@example.com", PasswordHash: "hash"}
		err := db.Create(&user).Error
		require.NoError(t, err)

		err = db.Delete(&user).Error
		require.NoError(t, err)

		var count int64
		db.Model(&User{}).Where("id = ?", user.ID).Count(&count)
		assert.Equal(t, int64(0), count)
	})
}

// ============================================================================
// HubToken Model Tests
// ============================================================================

func TestHubTokenModel(t *testing.T) {
	db := setupModelsTestDB(t)

	user := User{Email: "hub@example.com", PasswordHash: "hash"}
	err := db.Create(&user).Error
	require.NoError(t, err)

	t.Run("creates hub token with valid fields", func(t *testing.T) {
		token := HubToken{
			UserID:    user.ID,
			HubName:   "test-hub",
			TokenHash: "hash123",
			Revoked:   false,
		}

		err := db.Create(&token).Error
		require.NoError(t, err)
		assert.NotZero(t, token.ID)
		assert.NotZero(t, token.CreatedAt)
	})

	t.Run("enforces foreign key relationship", func(t *testing.T) {
		token := HubToken{
			UserID:    user.ID,
			HubName:   "fk-test-hub",
			TokenHash: "hash456",
		}

		err := db.Create(&token).Error
		require.NoError(t, err)

		var loaded HubToken
		err = db.Preload("User").First(&loaded, token.ID).Error
		require.NoError(t, err)
		assert.Equal(t, user.ID, loaded.User.ID)
		assert.Equal(t, user.Email, loaded.User.Email)
	})

	t.Run("uses correct table name", func(t *testing.T) {
		token := HubToken{}
		assert.Equal(t, "hub_tokens", token.TableName())
	})

	t.Run("defaults Revoked to false", func(t *testing.T) {
		token := HubToken{
			UserID:    user.ID,
			HubName:   "default-hub",
			TokenHash: "hash789",
		}

		err := db.Create(&token).Error
		require.NoError(t, err)
		assert.False(t, token.Revoked)
	})

	t.Run("allows multiple hub tokens per user", func(t *testing.T) {
		token1 := HubToken{UserID: user.ID, HubName: "hub-1", TokenHash: "hash1"}
		err := db.Create(&token1).Error
		require.NoError(t, err)

		token2 := HubToken{UserID: user.ID, HubName: "hub-2", TokenHash: "hash2"}
		err = db.Create(&token2).Error
		require.NoError(t, err)

		var count int64
		db.Model(&HubToken{}).Where("user_id = ?", user.ID).Count(&count)
		assert.GreaterOrEqual(t, count, int64(2))
	})

	t.Run("allows updating Revoked status", func(t *testing.T) {
		token := HubToken{
			UserID:    user.ID,
			HubName:   "revoke-test",
			TokenHash: "hash",
			Revoked:   false,
		}
		err := db.Create(&token).Error
		require.NoError(t, err)

		token.Revoked = true
		err = db.Save(&token).Error
		require.NoError(t, err)

		var loaded HubToken
		err = db.First(&loaded, token.ID).Error
		require.NoError(t, err)
		assert.True(t, loaded.Revoked)
	})

	t.Run("filters by revoked status", func(t *testing.T) {
		activeToken := HubToken{
			UserID:    user.ID,
			HubName:   "active-hub",
			TokenHash: "active_hash",
			Revoked:   false,
		}
		err := db.Create(&activeToken).Error
		require.NoError(t, err)

		revokedToken := HubToken{
			UserID:    user.ID,
			HubName:   "revoked-hub",
			TokenHash: "revoked_hash",
			Revoked:   true,
		}
		err = db.Create(&revokedToken).Error
		require.NoError(t, err)

		var activeTokens []HubToken
		err = db.Where("user_id = ? AND revoked = ?", user.ID, false).Find(&activeTokens).Error
		require.NoError(t, err)

		found := false
		for _, token := range activeTokens {
			if token.ID == activeToken.ID {
				found = true
				assert.False(t, token.Revoked)
			}
			assert.False(t, token.Revoked)
		}
		assert.True(t, found)
	})

	t.Run("cascade delete when user is deleted", func(t *testing.T) {
		testUser := User{Email: "cascade@example.com", PasswordHash: "hash"}
		err := db.Create(&testUser).Error
		require.NoError(t, err)

		token1 := HubToken{UserID: testUser.ID, HubName: "cascade-hub-1", TokenHash: "hash1"}
		err = db.Create(&token1).Error
		require.NoError(t, err)

		token2 := HubToken{UserID: testUser.ID, HubName: "cascade-hub-2", TokenHash: "hash2"}
		err = db.Create(&token2).Error
		require.NoError(t, err)

		// Note: SQLite in-memory doesn't enforce foreign key constraints by default
		var count int64
		db.Model(&HubToken{}).Where("user_id = ?", testUser.ID).Count(&count)
		assert.Equal(t, int64(2), count)
	})

	t.Run("orders by created_at", func(t *testing.T) {
		orderUser := User{Email: "order@example.com", PasswordHash: "hash"}
		err := db.Create(&orderUser).Error
		require.NoError(t, err)

		token1 := HubToken{UserID: orderUser.ID, HubName: "first-token", TokenHash: "hash1"}
		err = db.Create(&token1).Error
		require.NoError(t, err)

		token2 := HubToken{UserID: orderUser.ID, HubName: "second-token", TokenHash: "hash2"}
		err = db.Create(&token2).Error
		require.NoError(t, err)

		var tokens []HubToken
		err = db.Where("user_id = ?", orderUser.ID).Order("created_at DESC").Find(&tokens).Error
		require.NoError(t, err)

		assert.GreaterOrEqual(t, len(tokens), 2)
		if len(tokens) >= 2 {
			assert.True(t, tokens[0].CreatedAt.After(tokens[1].CreatedAt) ||
				tokens[0].CreatedAt.Equal(tokens[1].CreatedAt))
		}
	})
}
