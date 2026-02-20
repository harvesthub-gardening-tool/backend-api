package authjwt

import (
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewJWTManager(t *testing.T) {
	t.Run("creates manager with valid RSA keys", func(t *testing.T) {
		manager, err := NewJWTManager()
		require.NoError(t, err)
		require.NotNil(t, manager)
		require.NotNil(t, manager.privateKey)
		require.NotNil(t, manager.publicKey)

		assert.Equal(t, 2048, manager.privateKey.N.BitLen())
	})
}

func TestJWTManager_GenerateToken(t *testing.T) {
	manager, err := NewJWTManager()
	require.NoError(t, err)

	t.Run("generates valid user token", func(t *testing.T) {
		token, err := manager.GenerateToken("user-123", "john@example.com", 24*time.Hour)
		require.NoError(t, err)
		assert.NotEmpty(t, token)

		parsed, err := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
			return manager.publicKey, nil
		})
		require.NoError(t, err)
		assert.True(t, parsed.Valid)
	})

	t.Run("generates valid service account token with empty username", func(t *testing.T) {
		token, err := manager.GenerateToken("hub-456", "", 365*24*time.Hour)
		require.NoError(t, err)
		assert.NotEmpty(t, token)

		parsed, err := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
			return manager.publicKey, nil
		})
		require.NoError(t, err)
		assert.True(t, parsed.Valid)
	})

	t.Run("sets correct expiry time", func(t *testing.T) {
		before := time.Now()
		token, err := manager.GenerateToken("user-123", "test@example.com", time.Hour)
		require.NoError(t, err)
		after := time.Now()

		claims, err := manager.ValidateToken(token)
		require.NoError(t, err)

		assert.WithinDuration(t, before.Add(time.Hour), claims.ExpiresAt.Time, 2*time.Second)
		assert.True(t, claims.ExpiresAt.Time.After(after))
	})

	t.Run("returns error for empty user ID", func(t *testing.T) {
		token, err := manager.GenerateToken("", "user@example.com", 24*time.Hour)
		assert.Error(t, err)
		assert.Empty(t, token)
		assert.Contains(t, err.Error(), "userID")
	})

	t.Run("returns error for zero expiry", func(t *testing.T) {
		token, err := manager.GenerateToken("user-123", "user@example.com", 0)
		assert.Error(t, err)
		assert.Empty(t, token)
		assert.Contains(t, err.Error(), "expiry")
	})

	t.Run("returns error for negative expiry", func(t *testing.T) {
		token, err := manager.GenerateToken("user-123", "user@example.com", -time.Hour)
		assert.Error(t, err)
		assert.Empty(t, token)
		assert.Contains(t, err.Error(), "expiry")
	})
}

func TestJWTManager_ValidateToken(t *testing.T) {
	manager, err := NewJWTManager()
	require.NoError(t, err)

	t.Run("validates correct user token", func(t *testing.T) {
		token, err := manager.GenerateToken("user-123", "john@example.com", 24*time.Hour)
		require.NoError(t, err)

		claims, err := manager.ValidateToken(token)
		require.NoError(t, err)
		assert.Equal(t, "user-123", claims.UserID)
		assert.Equal(t, "john@example.com", claims.Username)
		assert.False(t, claims.ExpiresAt.Time.IsZero())
	})

	t.Run("validates correct service account token", func(t *testing.T) {
		token, err := manager.GenerateToken("hub-456", "", 365*24*time.Hour)
		require.NoError(t, err)

		claims, err := manager.ValidateToken(token)
		require.NoError(t, err)
		assert.Equal(t, "hub-456", claims.UserID)
		assert.Equal(t, "", claims.Username)
	})

	t.Run("rejects expired token", func(t *testing.T) {
		token, err := manager.GenerateToken("user-123", "test@example.com", time.Millisecond)
		require.NoError(t, err)
		time.Sleep(10 * time.Millisecond)

		claims, err := manager.ValidateToken(token)
		assert.Error(t, err)
		assert.Nil(t, claims)
		assert.Contains(t, err.Error(), "expired")
	})

	t.Run("rejects token with invalid signature", func(t *testing.T) {
		token, err := manager.GenerateToken("user-123", "test@example.com", 24*time.Hour)
		require.NoError(t, err)

		otherManager, err := NewJWTManager()
		require.NoError(t, err)

		claims, err := otherManager.ValidateToken(token)
		assert.Error(t, err)
		assert.Nil(t, claims)
		assert.Contains(t, err.Error(), "signature")
	})

	t.Run("rejects malformed token", func(t *testing.T) {
		claims, err := manager.ValidateToken("not.a.valid.jwt.token")
		assert.Error(t, err)
		assert.Nil(t, claims)
	})

	t.Run("rejects empty token", func(t *testing.T) {
		claims, err := manager.ValidateToken("")
		assert.Error(t, err)
		assert.Nil(t, claims)
	})

	t.Run("rejects token with wrong algorithm", func(t *testing.T) {
		mapClaims := jwt.MapClaims{
			"sub": "user-123",
			"exp": time.Now().Add(24 * time.Hour).Unix(),
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, mapClaims)
		tokenString, err := token.SignedString([]byte("secret"))
		require.NoError(t, err)

		validatedClaims, err := manager.ValidateToken(tokenString)
		assert.Error(t, err)
		assert.Nil(t, validatedClaims)
		assert.Contains(t, err.Error(), "algorithm")
	})
}

func TestClaims_IsServiceAccount(t *testing.T) {
	manager, err := NewJWTManager()
	require.NoError(t, err)

	t.Run("user token is not a service account", func(t *testing.T) {
		token, err := manager.GenerateToken("user-123", "john@example.com", 24*time.Hour)
		require.NoError(t, err)

		claims, err := manager.ValidateToken(token)
		require.NoError(t, err)
		assert.False(t, claims.IsServiceAccount())
	})

	t.Run("service account token with empty username", func(t *testing.T) {
		token, err := manager.GenerateToken("hub-456", "", 365*24*time.Hour)
		require.NoError(t, err)

		claims, err := manager.ValidateToken(token)
		require.NoError(t, err)
		assert.True(t, claims.IsServiceAccount())
	})
}

func TestJWTManager_KeyGeneration(t *testing.T) {
	t.Run("generates unique keys for each manager instance", func(t *testing.T) {
		manager1, err := NewJWTManager()
		require.NoError(t, err)

		manager2, err := NewJWTManager()
		require.NoError(t, err)

		assert.NotEqual(t, manager1.privateKey, manager2.privateKey)
	})
}

func TestJWTManager_ConcurrentAccess(t *testing.T) {
	manager, err := NewJWTManager()
	require.NoError(t, err)

	t.Run("handles concurrent token generation and validation", func(t *testing.T) {
		tokens := make([]string, 10)
		for i := range tokens {
			token, err := manager.GenerateToken("user-"+string(rune('0'+i)), "u@example.com", time.Hour)
			require.NoError(t, err)
			tokens[i] = token
		}

		done := make(chan bool, 10)
		for i := range tokens {
			go func(idx int) {
				claims, err := manager.ValidateToken(tokens[idx])
				assert.NoError(t, err)
				assert.NotNil(t, claims)
				done <- true
			}(i)
		}
		for range tokens {
			<-done
		}
	})
}

// ── Key persistence tests ─────────────────────────────────────────────────────

func TestNewOrLoadJWTManager(t *testing.T) {
	t.Run("generates new keys when directory is empty", func(t *testing.T) {
		tempDir := t.TempDir()

		manager, err := NewOrLoadJWTManager(tempDir)
		require.NoError(t, err)
		require.NotNil(t, manager)

		assert.FileExists(t, tempDir+"/.jwt_private.pem")
		assert.FileExists(t, tempDir+"/.jwt_public.pem")
	})

	t.Run("loads existing keys from disk", func(t *testing.T) {
		tempDir := t.TempDir()

		manager1, err := NewOrLoadJWTManager(tempDir)
		require.NoError(t, err)

		token, err := manager1.GenerateToken("user-123", "test@example.com", 24*time.Hour)
		require.NoError(t, err)

		manager2, err := NewOrLoadJWTManager(tempDir)
		require.NoError(t, err)

		claims, err := manager2.ValidateToken(token)
		require.NoError(t, err)
		assert.Equal(t, "user-123", claims.UserID)
	})

	t.Run("regenerates keys when private key is missing", func(t *testing.T) {
		tempDir := t.TempDir()

		manager1, err := NewOrLoadJWTManager(tempDir)
		require.NoError(t, err)

		token1, err := manager1.GenerateToken("user-123", "test@example.com", 24*time.Hour)
		require.NoError(t, err)

		require.NoError(t, os.Remove(tempDir+"/.jwt_private.pem"))

		manager2, err := NewOrLoadJWTManager(tempDir)
		require.NoError(t, err)

		_, err = manager2.ValidateToken(token1)
		assert.Error(t, err)

		token2, err := manager2.GenerateToken("user-456", "new@example.com", 24*time.Hour)
		require.NoError(t, err)
		claims, err := manager2.ValidateToken(token2)
		require.NoError(t, err)
		assert.Equal(t, "user-456", claims.UserID)
	})

	t.Run("regenerates keys when public key is missing", func(t *testing.T) {
		tempDir := t.TempDir()
		_, err := NewOrLoadJWTManager(tempDir)
		require.NoError(t, err)

		require.NoError(t, os.Remove(tempDir+"/.jwt_public.pem"))

		manager2, err := NewOrLoadJWTManager(tempDir)
		require.NoError(t, err)
		require.NotNil(t, manager2)

		assert.FileExists(t, tempDir+"/.jwt_private.pem")
		assert.FileExists(t, tempDir+"/.jwt_public.pem")
	})

	t.Run("regenerates keys when private key is corrupted", func(t *testing.T) {
		tempDir := t.TempDir()
		_, err := NewOrLoadJWTManager(tempDir)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(tempDir+"/.jwt_private.pem", []byte("corrupted"), 0600))

		manager2, err := NewOrLoadJWTManager(tempDir)
		require.NoError(t, err)

		token, err := manager2.GenerateToken("user-123", "test@example.com", 24*time.Hour)
		require.NoError(t, err)
		claims, err := manager2.ValidateToken(token)
		require.NoError(t, err)
		assert.Equal(t, "user-123", claims.UserID)
	})
}

func TestLoadJWTManager(t *testing.T) {
	t.Run("loads valid PEM keys", func(t *testing.T) {
		tempDir := t.TempDir()
		privatePath := tempDir + "/.jwt_private.pem"
		publicPath := tempDir + "/.jwt_public.pem"

		manager1, err := NewJWTManager()
		require.NoError(t, err)
		require.NoError(t, saveKeys(manager1, privatePath, publicPath))

		manager2, err := loadJWTManager(privatePath, publicPath)
		require.NoError(t, err)

		token, err := manager2.GenerateToken("user-123", "test@example.com", 24*time.Hour)
		require.NoError(t, err)
		claims, err := manager2.ValidateToken(token)
		require.NoError(t, err)
		assert.Equal(t, "user-123", claims.UserID)
	})

	t.Run("returns error for invalid private key PEM", func(t *testing.T) {
		tempDir := t.TempDir()
		privatePath := tempDir + "/.jwt_private.pem"
		publicPath := tempDir + "/.jwt_public.pem"

		require.NoError(t, os.WriteFile(privatePath, []byte("invalid pem"), 0600))

		manager, err := NewJWTManager()
		require.NoError(t, err)
		require.NoError(t, saveKeys(manager, tempDir+"/tmp_priv.pem", publicPath))

		_, err = loadJWTManager(privatePath, publicPath)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decode PEM block")
	})

	t.Run("returns error for non-existent private key file", func(t *testing.T) {
		tempDir := t.TempDir()
		_, err := loadJWTManager(tempDir+"/nonexistent.pem", tempDir+"/public.pem")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read private key")
	})

	t.Run("returns error for non-existent public key file", func(t *testing.T) {
		tempDir := t.TempDir()
		privatePath := tempDir + "/.jwt_private.pem"

		manager, err := NewJWTManager()
		require.NoError(t, err)
		require.NoError(t, saveKeys(manager, privatePath, tempDir+"/tmp.pem"))

		_, err = loadJWTManager(privatePath, tempDir+"/nonexistent.pem")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read public key")
	})

	t.Run("returns error for PEM with extra data (private key)", func(t *testing.T) {
		tempDir := t.TempDir()
		privatePath := tempDir + "/.jwt_private.pem"
		publicPath := tempDir + "/.jwt_public.pem"

		manager, err := NewJWTManager()
		require.NoError(t, err)
		require.NoError(t, saveKeys(manager, privatePath, publicPath))

		data, err := os.ReadFile(privatePath)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(privatePath, append(data, []byte("extra data")...), 0600))

		_, err = loadJWTManager(privatePath, publicPath)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "extra data after PEM block")
	})

	t.Run("returns error for PEM with extra data (public key)", func(t *testing.T) {
		tempDir := t.TempDir()
		privatePath := tempDir + "/.jwt_private.pem"
		publicPath := tempDir + "/.jwt_public.pem"

		manager, err := NewJWTManager()
		require.NoError(t, err)
		require.NoError(t, saveKeys(manager, privatePath, publicPath))

		data, err := os.ReadFile(publicPath)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(publicPath, append(data, []byte("extra data")...), 0644))

		_, err = loadJWTManager(privatePath, publicPath)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "extra data after PEM block")
	})
}

func TestSaveKeys(t *testing.T) {
	t.Run("creates key files with correct permissions", func(t *testing.T) {
		tempDir := t.TempDir()
		privatePath := tempDir + "/.jwt_private.pem"
		publicPath := tempDir + "/.jwt_public.pem"

		manager, err := NewJWTManager()
		require.NoError(t, err)
		require.NoError(t, saveKeys(manager, privatePath, publicPath))

		privInfo, err := os.Stat(privatePath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), privInfo.Mode().Perm())

		pubInfo, err := os.Stat(publicPath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0644), pubInfo.Mode().Perm())
	})

	t.Run("returns error for invalid private key path", func(t *testing.T) {
		manager, err := NewJWTManager()
		require.NoError(t, err)

		err = saveKeys(manager, "/nonexistent/dir/.jwt_private.pem", "/tmp/.jwt_public.pem")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to write private key")
	})

	t.Run("returns error for invalid public key path", func(t *testing.T) {
		tempDir := t.TempDir()
		manager, err := NewJWTManager()
		require.NoError(t, err)

		err = saveKeys(manager, tempDir+"/.jwt_private.pem", "/nonexistent/dir/.jwt_public.pem")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to write public key")
	})
}

func TestFileExists(t *testing.T) {
	t.Run("returns true for existing file", func(t *testing.T) {
		f := t.TempDir() + "/test.txt"
		require.NoError(t, os.WriteFile(f, []byte("test"), 0644))
		assert.True(t, fileExists(f))
	})

	t.Run("returns false for non-existent file", func(t *testing.T) {
		assert.False(t, fileExists("/nonexistent/file.txt"))
	})

	t.Run("returns false for directory", func(t *testing.T) {
		assert.False(t, fileExists(t.TempDir()))
	})
}
