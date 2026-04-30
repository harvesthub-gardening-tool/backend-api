package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	authjwt "harvest-hub/api/internal/auth/jwt"
)

var (
	// ErrDuplicateEmail is returned when registering an email that already exists.
	ErrDuplicateEmail = errors.New("email already registered")
	// ErrInvalidCredentials is returned when login credentials are incorrect.
	ErrInvalidCredentials = errors.New("invalid email or password")
	// ErrWeakPassword is returned when password is too short.
	ErrWeakPassword = errors.New("password must be at least 8 characters long")
	// ErrInvalidEmail is returned when email format is invalid.
	ErrInvalidEmail = errors.New("invalid email format")
	// ErrDeviceAlreadyAssociated is returned when AssociateHub is called with a device_id already bound to a hub.
	ErrDeviceAlreadyAssociated = errors.New("device already associated with a hub")
	// ErrInvalidDeviceCredentials is returned when device_id is unknown or hub_secret does not match.
	ErrInvalidDeviceCredentials = errors.New("invalid device credentials")
	// ErrHubAlreadyClaimed is returned when ClaimHubToken is called for a hub that already has an issued token.
	ErrHubAlreadyClaimed = errors.New("hub token already claimed")
	// ErrHubNotFound is returned when a hub cannot be found or is not owned by the caller.
	ErrHubNotFound = errors.New("hub not found")
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

	token, err := s.jwtManager.GenerateToken(fmt.Sprint(user.ID), email, "", userTokenExpiry)
	if err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}
	return token, nil
}

// ── v2: QR-Code Hub Provisioning ──────────────────────────────────────────────

// HubInfo is hub metadata for v2 API responses.
type HubInfo struct {
	ID        string
	UserID    string
	Name      string
	DeviceID  string
	Claimed   bool
	Revoked   bool
	CreatedAt time.Time
	ClaimedAt *time.Time
}

// hashHubSecret returns the hex-encoded SHA-256 of a hub secret.
// SHA-256 (not bcrypt) is used because hub_secret is a high-entropy machine-generated
// value, not a low-entropy human password — fast comparison is sufficient and bcrypt's
// cost would be wasted.
func hashHubSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// AssociateHub binds a physical hub (identified by device_id from the QR code) to a user.
// Verifies the hub_secret was not yet associated. Each device_id can only be associated once.
// Returns the hub's database ID.
func (s *AuthService) AssociateHub(ctx context.Context, userID, deviceID, hubSecret, hubName string) (string, error) {
	if userID == "" {
		return "", errors.New("userID cannot be empty")
	}
	if deviceID == "" {
		return "", errors.New("device ID cannot be empty")
	}
	if hubSecret == "" {
		return "", errors.New("hub secret cannot be empty")
	}
	if hubName == "" {
		return "", errors.New("hub name cannot be empty")
	}

	var userIDUint uint
	if _, err := fmt.Sscan(userID, &userIDUint); err != nil {
		return "", fmt.Errorf("invalid user ID format: %w", err)
	}

	secretHash := hashHubSecret(hubSecret)

	hub := Hub{
		UserID:        userIDUint,
		Name:          hubName,
		DeviceID:      &deviceID,
		HubSecretHash: &secretHash,
	}
	if err := s.db.WithContext(ctx).Create(&hub).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || isUniqueViolation(err) {
			return "", fmt.Errorf("%w: %s", ErrDeviceAlreadyAssociated, deviceID)
		}
		return "", fmt.Errorf("failed to associate hub: %w", err)
	}

	return fmt.Sprint(hub.ID), nil
}

// ClaimHubToken issues a JWT for a hub that previously called AssociateHub.
// Public endpoint — verifies device_id + hub_secret. Claim-once: a hub can only
// receive a token once; subsequent calls fail with ErrHubAlreadyClaimed.
// Returns the raw JWT (caller must store it; it is not retrievable later).
func (s *AuthService) ClaimHubToken(ctx context.Context, deviceID, hubSecret string) (string, error) {
	if deviceID == "" {
		return "", errors.New("device ID cannot be empty")
	}
	if hubSecret == "" {
		return "", errors.New("hub secret cannot be empty")
	}

	var hub Hub
	if err := s.db.WithContext(ctx).Where("device_id = ?", deviceID).First(&hub).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrInvalidDeviceCredentials
		}
		return "", fmt.Errorf("failed to load hub: %w", err)
	}

	if hub.HubSecretHash == nil {
		return "", ErrInvalidDeviceCredentials
	}
	// Constant-time compare blocks timing-side-channel attacks on hub_secret.
	expected := hashHubSecret(hubSecret)
	if subtle.ConstantTimeCompare([]byte(*hub.HubSecretHash), []byte(expected)) != 1 {
		return "", ErrInvalidDeviceCredentials
	}

	// Claim-once: any existing HubToken row (revoked or not) blocks re-claim.
	// Revocation must go through RevokeHub which also clears the row to allow re-provisioning.
	var existing int64
	if err := s.db.WithContext(ctx).Model(&HubToken{}).
		Where("hub_id = ?", hub.ID).
		Count(&existing).Error; err != nil {
		return "", fmt.Errorf("failed to check existing claim: %w", err)
	}
	if existing > 0 {
		return "", ErrHubAlreadyClaimed
	}

	// Service accounts use empty username to signal "hub device" to the JWT middleware.
	// HubID is embedded in the claim so InsertSensorData can enforce per-hub ownership.
	token, err := s.jwtManager.GenerateToken(fmt.Sprint(hub.UserID), "", fmt.Sprint(hub.ID), hubTokenExpiry)
	if err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}

	tokenHashArr := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(tokenHashArr[:])
	now := time.Now()
	hubID := hub.ID
	hubToken := HubToken{
		UserID:    hub.UserID,
		HubName:   hub.Name,
		TokenHash: tokenHash,
		Revoked:   false,
		HubID:     &hubID,
		ClaimedAt: &now,
	}
	if err := s.db.WithContext(ctx).Create(&hubToken).Error; err != nil {
		return "", fmt.Errorf("failed to store hub token: %w", err)
	}

	return token, nil
}

// ListHubs returns all hubs owned by the given user with their claim/revocation status.
func (s *AuthService) ListHubs(ctx context.Context, userID string) ([]HubInfo, error) {
	if userID == "" {
		return nil, errors.New("userID cannot be empty")
	}

	var hubs []Hub
	if err := s.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&hubs).Error; err != nil {
		return nil, fmt.Errorf("failed to query hubs: %w", err)
	}

	result := make([]HubInfo, len(hubs))
	for i, h := range hubs {
		var token HubToken
		claimed, revoked := false, false
		var claimedAt *time.Time
		err := s.db.WithContext(ctx).Where("hub_id = ?", h.ID).First(&token).Error
		if err == nil {
			claimed = true
			revoked = token.Revoked
			claimedAt = token.ClaimedAt
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("failed to query hub token: %w", err)
		}

		deviceID := ""
		if h.DeviceID != nil {
			deviceID = *h.DeviceID
		}
		result[i] = HubInfo{
			ID:        fmt.Sprint(h.ID),
			UserID:    fmt.Sprint(h.UserID),
			Name:      h.Name,
			DeviceID:  deviceID,
			Claimed:   claimed,
			Revoked:   revoked,
			CreatedAt: h.CreatedAt,
			ClaimedAt: claimedAt,
		}
	}
	return result, nil
}

// RevokeHub revokes all tokens associated with the given hub (verifies ownership).
// The hub row is preserved so the device_id remains reserved.
func (s *AuthService) RevokeHub(ctx context.Context, userID, hubID string) error {
	if userID == "" {
		return errors.New("userID cannot be empty")
	}
	if hubID == "" {
		return errors.New("hub ID cannot be empty")
	}

	var hub Hub
	if err := s.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", hubID, userID).
		First(&hub).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: %s", ErrHubNotFound, hubID)
		}
		return fmt.Errorf("failed to load hub: %w", err)
	}

	if err := s.db.WithContext(ctx).
		Where("hub_id = ?", hub.ID).
		Delete(&HubToken{}).Error; err != nil {
		return fmt.Errorf("failed to revoke hub tokens: %w", err)
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
