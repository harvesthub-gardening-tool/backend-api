// Package auth provides user authentication and hub device token management
// for the Harvest Hub backend API.
//
// Sub-packages:
//   - auth/jwt    — JWT token generation, validation, and RSA key persistence
//   - auth/context — AuthInfo type and context storage/retrieval helpers
//
// This package contains:
//   - GORM models: User, HubToken (database representations)
//   - AuthService: Register, Login, CreateHubToken, ListHubTokens, RevokeHubToken
//   - NewJWTAuthInterceptor: Connect RPC middleware for RS256 JWT validation
//
// Two token types are in use:
//   - User tokens (24h expiry, non-empty Username claim) — for mobile app
//   - Hub tokens (1yr expiry, empty Username claim) — for IoT Hub devices
package auth
