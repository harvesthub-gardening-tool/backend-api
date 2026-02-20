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

var (
	// ErrDuplicateEmail is returned when registering an email that already exists.
	ErrDuplicateEmail = errors.New("email already registered")
	// ErrDuplicateHubName is returned when creating a hub token with an existing name.
	ErrDuplicateHubName = errors.New("hub name already exists for this user")
	// ErrInvalidCredentials is returned when login credentials are incorrect.
	ErrInvalidCredentials = errors.New("invalid email or password")
	// ErrHubTokenNotFound is returned when a hub token cannot be found or accessed.
	ErrHubTokenNotFound = errors.New("hub token not found")
	// ErrWeakPassword is returned when password is too short.
	ErrWeakPassword = errors.New("password must be at least 8 characters long")
	// ErrInvalidEmail is returned when email format is invalid.
	ErrInvalidEmail = errors.New("invalid email format")
)

const (
	userTokenExpiry = 24 * time.Hour
	hubTokenExpiry  = 365 * 24 * time.Hour
	bcryptCost      = bcrypt.DefaultCost
)

// AuthService handles user registration, login, and hub device token management.
type AuthService struct {
	db         *gorm.DB
	jwtManager *authjwt.JWTManager
}

// NewAuthService creates a new AuthService.
func NewAuthService(db *gorm.DB, jwtManager *authjwt.JWTManager) *AuthService {
	return &AuthService{db: db, jwtManager: jwtManager}
}

// ── User Registration and Authentication ──────────────────────────────────────

// RegisterUser creates a new user account. Returns the new user's ID.
func (s *AuthService) RegisterUser(ctx context.Context, email, password string) (string, error) {
	if email == "" {
		return "", errors.New("email cannot be empty")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return "", ErrInvalidEmail
	}
	if password == "" {
		return "", errors.New("password cannot be empty")
	}
	if len(password) < 8 {
		return "", ErrWeakPassword
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}

	user := User{Email: email, PasswordHash: string(hash)}
	if err := s.db.WithContext(ctx).Create(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || isUniqueViolation(err) {
			return "", fmt.Errorf("%w: %s", ErrDuplicateEmail, email)
		}
		return "", fmt.Errorf("failed to save user: %w", err)
	}

	return fmt.Sprint(user.ID), nil
}

// LoginUser validates credentials and returns a JWT user token (24h expiry).
func (s *AuthService) LoginUser(ctx context.Context, email, password string) (string, error) {
	if email == "" {
		return "", errors.New("email cannot be empty")
	}
	if password == "" {
		return "", errors.New("password cannot be empty")
	}

	var user User
	if err := s.db.WithContext(ctx).Where("email = ?", email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrInvalidCredentials
		}
		return "", fmt.Errorf("failed to load user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return "", ErrInvalidCredentials
		}
		return "", fmt.Errorf("failed to verify password: %w", err)
	}

	token, err := s.jwtManager.GenerateToken(fmt.Sprint(user.ID), email, userTokenExpiry)
	if err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}
	return token, nil
}

// ── Hub Token Management ──────────────────────────────────────────────────────

// CreateHubToken creates a new hub device token tied to the given user.
// Returns the raw token string (shown only once — caller must store it).
func (s *AuthService) CreateHubToken(ctx context.Context, userID, hubName string) (string, error) {
	if userID == "" {
		return "", errors.New("userID cannot be empty")
	}
	if hubName == "" {
		return "", errors.New("hub name cannot be empty")
	}

	var count int64
	if err := s.db.WithContext(ctx).Model(&HubToken{}).
		Where("user_id = ? AND hub_name = ? AND revoked = false", userID, hubName).
		Count(&count).Error; err != nil {
		return "", fmt.Errorf("failed to check existing hub token: %w", err)
	}
	if count > 0 {
		return "", fmt.Errorf("%w: %s", ErrDuplicateHubName, hubName)
	}

	// Service accounts use empty username to signal "hub device".
	token, err := s.jwtManager.GenerateToken(userID, "", hubTokenExpiry)
	if err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}

	hash := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(hash[:])

	var userIDUint uint
	if _, err := fmt.Sscan(userID, &userIDUint); err != nil {
		return "", fmt.Errorf("invalid user ID format: %w", err)
	}

	hubToken := HubToken{
		UserID:    userIDUint,
		HubName:   hubName,
		TokenHash: tokenHash,
		Revoked:   false,
	}
	if err := s.db.WithContext(ctx).Create(&hubToken).Error; err != nil {
		return "", fmt.Errorf("failed to store hub token: %w", err)
	}

	return token, nil
}

// HubTokenInfo is hub token metadata for API responses (no token value).
type HubTokenInfo struct {
	ID        string
	UserID    string
	HubName   string
	CreatedAt time.Time
	Revoked   bool
}

// ListHubTokens returns active (non-revoked) hub tokens for the given user.
func (s *AuthService) ListHubTokens(ctx context.Context, userID string) ([]HubTokenInfo, error) {
	if userID == "" {
		return nil, errors.New("userID cannot be empty")
	}

	var tokens []HubToken
	if err := s.db.WithContext(ctx).
		Where("user_id = ? AND revoked = false", userID).
		Order("created_at DESC").
		Find(&tokens).Error; err != nil {
		return nil, fmt.Errorf("failed to query hub tokens: %w", err)
	}

	result := make([]HubTokenInfo, len(tokens))
	for i, t := range tokens {
		result[i] = HubTokenInfo{
			ID:        fmt.Sprint(t.ID),
			UserID:    fmt.Sprint(t.UserID),
			HubName:   t.HubName,
			CreatedAt: t.CreatedAt,
			Revoked:   t.Revoked,
		}
	}
	return result, nil
}

// RevokeHubToken marks a hub token as revoked (soft delete, verifies ownership).
func (s *AuthService) RevokeHubToken(ctx context.Context, userID, hubTokenID string) error {
	if userID == "" {
		return errors.New("userID cannot be empty")
	}
	if hubTokenID == "" {
		return errors.New("hub token ID cannot be empty")
	}

	result := s.db.WithContext(ctx).
		Model(&HubToken{}).
		Where("id = ? AND user_id = ? AND revoked = false", hubTokenID, userID).
		Update("revoked", true)

	if result.Error != nil {
		return fmt.Errorf("failed to revoke hub token: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: %s", ErrHubTokenNotFound, hubTokenID)
	}
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return containsString(msg, "duplicate key") ||
		containsString(msg, "duplicated key") ||
		containsString(msg, "unique constraint") ||
		containsString(msg, "UNIQUE constraint")
}

func containsString(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
