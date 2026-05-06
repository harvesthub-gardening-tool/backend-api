package auth

import "time"

// MotorCommand persists the backend command queue for hub-polled motor actions.
//
// Note: the intended invariant is one active command per node at a time, but we
// intentionally do not rely on a partial unique index here because this project
// uses GORM AutoMigrate for both PostgreSQL dev databases and SQLite tests.
// Later service-layer logic should enforce that invariant for active statuses.
type MotorCommand struct {
	ID uint `gorm:"primarykey"`

	CommandID      string `gorm:"not null;uniqueIndex:idx_motor_commands_command_id"`
	UserID         uint   `gorm:"not null;index;uniqueIndex:idx_motor_commands_idempotency,priority:1"`
	User           User   `gorm:"foreignKey:UserID"`
	HubID          uint   `gorm:"not null;index:idx_motor_commands_polling,priority:1"`
	Hub            Hub    `gorm:"foreignKey:HubID"`
	NodeID         string `gorm:"not null;index:idx_motor_commands_lookup,priority:2;uniqueIndex:idx_motor_commands_idempotency,priority:2"`
	Action         string `gorm:"not null"`
	DurationMS     int64  `gorm:"not null"`
	Status         string `gorm:"not null;index:idx_motor_commands_polling,priority:2;index:idx_motor_commands_lookup,priority:1"`
	IdempotencyKey string `gorm:"not null;uniqueIndex:idx_motor_commands_idempotency,priority:3"`

	ReasonCode    string
	ReasonMessage string

	LeaseToken     *string
	LeasedAt       *time.Time
	LeaseExpiresAt *time.Time `gorm:"index:idx_motor_commands_polling,priority:4"`
	ExpiresAt      time.Time  `gorm:"not null;index:idx_motor_commands_polling,priority:3"`
	CompletedAt    *time.Time

	TerminalResultCode    string
	TerminalResultMessage string

	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`

	Events []MotorCommandEvent `gorm:"foreignKey:MotorCommandID"`
}

func (MotorCommand) TableName() string { return "motor_commands" }

// MotorCommandEvent is an append-only audit log for command state transitions.
type MotorCommandEvent struct {
	ID uint `gorm:"primarykey"`

	MotorCommandID uint         `gorm:"not null;index"`
	MotorCommand   MotorCommand `gorm:"foreignKey:MotorCommandID"`
	CommandID      string       `gorm:"not null;index"`

	ActorType string `gorm:"not null"`
	ActorID   string `gorm:"not null"`

	PreviousStatus string
	NewStatus      string `gorm:"not null"`
	ReasonCode     string
	ReasonMessage  string
	Message        string

	OccurredAt time.Time `gorm:"not null;autoCreateTime;index"`
}

func (MotorCommandEvent) TableName() string { return "motor_command_events" }
