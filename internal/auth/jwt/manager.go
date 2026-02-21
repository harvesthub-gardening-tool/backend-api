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

// NewJWTManager creates a JWTManager with a fresh ephemeral RSA 2048-bit key pair.
// Keys are not persisted — use NewOrLoadJWTManager for production.
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
// Key files stored at:
//   - {keyPath}/.jwt_private.pem  (PKCS#1, mode 0600)
//   - {keyPath}/.jwt_public.pem   (PKIX, mode 0644)
func NewOrLoadJWTManager(keyPath string) (*JWTManager, error) {
	privatePath := filepath.Join(keyPath, ".jwt_private.pem")
	publicPath := filepath.Join(keyPath, ".jwt_public.pem")

	if fileExists(privatePath) && fileExists(publicPath) {
		m, err := loadJWTManager(privatePath, publicPath)
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
	if err := saveKeys(m, privatePath, publicPath); err != nil {
		return nil, fmt.Errorf("failed to save JWT keys: %w", err)
	}
	fmt.Println("🔑 Generated and saved new JWT keys")
	return m, nil
}

// GenerateToken signs a new JWT with the given identity and expiry duration.
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

// ValidateToken parses and validates a JWT string.
// Checks: valid RS256 signature, algorithm pinning, and expiry.
// Returns Claims on success.
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

// ── Key persistence ───────────────────────────────────────────────────────────

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func loadJWTManager(privatePath, publicPath string) (*JWTManager, error) {
	privData, err := os.ReadFile(privatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %w", err)
	}
	block, rest := pem.Decode(privData)
	if block == nil {
		return nil, errors.New("failed to decode PEM block for private key")
	}
	if len(rest) > 0 {
		return nil, errors.New("extra data after PEM block in private key")
	}
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	pubData, err := os.ReadFile(publicPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read public key: %w", err)
	}
	block, rest = pem.Decode(pubData)
	if block == nil {
		return nil, errors.New("failed to decode PEM block for public key")
	}
	if len(rest) > 0 {
		return nil, errors.New("extra data after PEM block in public key")
	}
	pubInterface, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}
	publicKey, ok := pubInterface.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("public key is not RSA")
	}

	return &JWTManager{privateKey: privateKey, publicKey: publicKey}, nil
}

func saveKeys(m *JWTManager, privatePath, publicPath string) error {
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(m.privateKey),
	})
	if err := os.WriteFile(privatePath, privPEM, 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	pubBytes, err := x509.MarshalPKIXPublicKey(m.publicKey)
	if err != nil {
		return fmt.Errorf("marshal public key: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes})
	if err := os.WriteFile(publicPath, pubPEM, 0644); err != nil {
		return fmt.Errorf("failed to write public key: %w", err)
	}
	return nil
}
