package authjwt

import "github.com/golang-jwt/jwt/v5"

// Claims represents the custom JWT claims for all tokens in the system.
// Service accounts (Hub devices) are identified by an empty Username field.
type Claims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	HubID    string `json:"hub_id,omitempty"`
	jwt.RegisteredClaims
}

// IsServiceAccount returns true if the token belongs to a Hub device (no username).
func (c *Claims) IsServiceAccount() bool {
	return c.Username == ""
}
