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

// HubToken represents a long-lived service account token for a Hub device,
// issued via auth.v2/ClaimHubToken. Token values are SHA-256 hashed before
// storage; the plaintext JWT is shown only once on issuance.
type HubToken struct {
	ID        uint      `gorm:"primarykey"`
	UserID    uint      `gorm:"not null;index"`
	User      User      `gorm:"foreignKey:UserID"`
	HubName   string    `gorm:"not null"`
	TokenHash string    `gorm:"not null"`
	Revoked   bool      `gorm:"default:false"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
	HubID     *uint     `gorm:"index"`
	ClaimedAt *time.Time
}

func (HubToken) TableName() string { return "hub_tokens" }

// Hub represents a physical ESP32-S3 hub device owned by a user.
// A hub scans nearby Probe BLE advertisements and forwards sensor data to the API.
// DeviceID and HubSecretHash are populated during QR-code provisioning and used
// by ClaimHubToken to authenticate the hub on first boot.
type Hub struct {
	ID              uint   `gorm:"primarykey"`
	UserID          uint   `gorm:"not null;index"`
	User            User   `gorm:"foreignKey:UserID"`
	Name            string `gorm:"not null"`
	Location        string
	FirmwareVersion string
	LastSeen        *time.Time
	CreatedAt       time.Time `gorm:"autoCreateTime"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime"`
	DeviceID        *string   `gorm:"uniqueIndex"`
	HubSecretHash   *string
}

func (Hub) TableName() string { return "hubs" }

// SensorNode represents a physical STM32 probe device discovered by a hub.
// NodeID matches the node_id field in sensor_data. HubID is nullable — a node
// is "unregistered" until a user claims it to a hub.
type SensorNode struct {
	ID        uint   `gorm:"primarykey"`
	NodeID    string `gorm:"uniqueIndex;not null"` // matches sensor_data.node_id
	HubID     *uint  `gorm:"index"`                // nullable: probe may be unregistered
	Hub       *Hub   `gorm:"foreignKey:HubID"`
	Name      string
	Location  string
	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (SensorNode) TableName() string { return "sensor_nodes" }
