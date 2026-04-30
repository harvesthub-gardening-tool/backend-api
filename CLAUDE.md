# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Multi-Repo Context

This is the **backend-api** service within the Harvest Hub project (`/home/ewan/projets/Harvest-Hub/`). Sibling repos:

| Repo | Stack | Purpose |
|------|-------|---------|
| `protos` | Protobuf + Buf CLI | Shared API definitions, auto-published to `protos-go` |
| `mobile-app` | Expo 54 / React Native / TypeScript | Mobile client |
| `demo-website` | Next.js 15 / Three.js / Tailwind | Marketing website |
| `hub-core` | Rust / ESP32-S3 / embassy | IoT hub firmware (BLE scanner) |
| `probe-core` | Rust / STM32F103 | Sensor probe firmware (BLE advertiser) |

## Build & Run

```bash
# Full stack (API + TimescaleDB + Swagger)
docker-compose up

# DB only (for local API development)
docker-compose up db

# Run API locally
DATABASE_URL="postgres://postgres:postgres@localhost:5432/garden_db?sslmode=disable" go run server/main.go

# Build binary
go build -o server ./server

# Run tests
go test ./...

# Run single test
go test ./internal/service/ -run TestFunctionName -v
```

## Proto Definitions Workflow

API contracts live in the `protos` repo and are auto-published to `protos-go`:

```bash
# Update to latest proto-generated code
go get -u github.com/harvesthub-gardening-tool/protos-go@latest
go mod tidy

# Test a feature branch from protos repo
go get github.com/harvesthub-gardening-tool/protos-go@feature/branch-name
go mod tidy
```

Proto imports:
```go
gardenv2 "github.com/harvesthub-gardening-tool/protos-go/garden/v2"
"github.com/harvesthub-gardening-tool/protos-go/garden/v2/gardenv2connect"
authv2 "github.com/harvesthub-gardening-tool/protos-go/auth/v2"
"github.com/harvesthub-gardening-tool/protos-go/auth/v2/authv2connect"
```

## Architecture

**Go 1.24** with **Connect RPC** (gRPC-compatible), **TimescaleDB**, and **JWT-based authentication**.

### Key Files

- `server/main.go` — Entry point, HTTP server setup, CORS, Connect handler registration
- `internal/auth/middleware.go` — JWT interceptor (RS256 validation)
- `internal/auth/jwt/` — JWT token generation/validation, RSA key persistence (`package authjwt`)
- `internal/auth/context/` — AuthInfo type and context helpers (`package authctx`)
- `internal/service/garden.go` — Business logic for `InsertSensorData` and `GetSummary` (garden.v2)
- `internal/service/auth_service_v2.go` — Connect RPC handlers for auth.v2 endpoints
- `db/schema.sql` — Sensor_data hypertable definition (TimescaleDB)
- `db/seed.sql` — Sample seed data
- `docker-compose.yml` — Full stack: API, PostgreSQL 17

### Authentication Model

JWT-based authentication (RS256) with two token types:

1. **Hub tokens** (Hub devices) — Empty username, non-empty `HubID` claim. Authorized for `InsertSensorData` only. 1-year expiry. Issued via `auth.v2/ClaimHubToken` (QR-code provisioning, claim-once).
2. **User tokens** (mobile app) — Non-empty username/email. Authorized for read operations and hub management (`AssociateHub`, `ListHubs`, `RevokeHub`). 24-hour expiry.

The auth middleware in `internal/auth/middleware.go` inspects JWT claims to distinguish hubs from users and enforces per-RPC authorization. Per-user data isolation: `GetSummary` JOINs `sensor_data → sensor_nodes → hubs` filtered by `hubs.user_id = caller`.

RSA key pair is persisted to `.jwt_private.pem` / `.jwt_public.pem` so hub tokens survive server restarts.

### Package Structure

```
internal/auth/
  jwt/        — authjwt: JWTManager, Claims, key generation/persistence
  context/    — authctx: AuthInfo, SetAuthInfo, GetUserID, GetUsername, IsServiceAccount
  models.go   — GORM models: User, Hub, HubToken
  service.go  — AuthService (v2): Register, Login, AssociateHub, ListHubs, RevokeHub, ClaimHubToken
  middleware.go — NewJWTAuthInterceptor (Connect unary interceptor)
  testing.go  — NewTestGORMDB, CreateTestAuthContext (test helpers)
```

### Database

PostgreSQL 17 with TimescaleDB extension. Sensor data stored in a hypertable partitioned by time. Queries use `time_bucket()` for aggregation. Auth tables (`auth_users`, `hub_tokens`) are managed by GORM AutoMigrate.

### Docker Services

| Service | Port | Purpose |
|---------|------|---------|
| api | 8080 | Backend API |
| db | 5432 | TimescaleDB (PostgreSQL 17-based, garden_db) |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `DATABASE_URL` | PostgreSQL connection string |
| `JWT_KEY_PATH` | Directory for JWT RSA key storage (default: `.`) |

### System Data Flow

```
Probe (STM32, BLE) → Hub (ESP32-S3, BLE scan) → Backend API (Connect RPC + JWT) → TimescaleDB
                                                                                       ↑
                                                    Mobile App (Expo, JWT user token) ──┘
```

## API Endpoints

Connect protocol (gRPC-compatible, also accepts JSON over HTTP):

- `POST /garden.v2.GardenService/InsertSensorData` — Hub-only, writes sensor readings (auto-binds nodes to hub)
- `POST /garden.v2.GardenService/GetSummary` — User-only, reads aggregated data scoped to caller's hubs (optional `hub_id` filter; `hub_id` returned per summary)
- `POST /auth.v2.AuthService/Register` — Public, creates user account
- `POST /auth.v2.AuthService/Login` — Public, returns user JWT
- `POST /auth.v2.AuthService/AssociateHub` — User, binds a hub to caller (post-QR-scan)
- `POST /auth.v2.AuthService/ListHubs` — User, lists caller's hubs
- `POST /auth.v2.AuthService/RevokeHub` — User, deletes hub token row (unblocks re-claim)
- `POST /auth.v2.AuthService/ClaimHubToken` — Public (hub-initiated), hub exchanges `device_id` + `hub_secret` for hub JWT (claim-once)

Live API docs: https://harvesthub-gardening-tool.github.io/protos/

# currentDate
Today's date is 2026-02-20.

      IMPORTANT: this context may or may not be relevant to your tasks. You should not respond to this context unless it is highly relevant to your task.
