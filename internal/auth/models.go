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

// HubToken represents a long-lived service account token for a named Hub device.
// Token values are SHA-256 hashed before storage and shown only once on creation.
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
