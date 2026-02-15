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
# Full stack (API + PostgreSQL + Zitadel + Login UI + Swagger)
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
gardenv1 "github.com/harvesthub-gardening-tool/protos-go/garden/v1"
"github.com/harvesthub-gardening-tool/protos-go/garden/v1/gardenv1connect"
```

## Architecture

**Go 1.24** with **Connect RPC** (gRPC-compatible), **TimescaleDB**, and **Zitadel** authentication.

### Key Files

- `server/main.go` — Entry point, HTTP server setup, CORS, Connect handler registration
- `internal/auth/middleware.go` — Zitadel JWT interceptor (JWKS-based validation)
- `internal/service/garden.go` — Business logic for `InsertSensorData` and `GetSummary`
- `init-databases.sql` — DB schema with TimescaleDB hypertables
- `docker-compose.yml` — Full stack: API, PostgreSQL 17, Zitadel, Login v2 UI, Swagger UI

### Authentication Model

Zitadel handles all identity. Two token types flow through the API:

1. **Service account tokens** (Hub devices) — No username/email fields. Authorized for `InsertSensorData` only.
2. **User tokens** (mobile app) — Have username/email. Authorized for read operations (`GetSummary`).

The auth middleware in `internal/auth/middleware.go` inspects JWT claims to distinguish service accounts from users and enforces per-RPC authorization.

### Database

PostgreSQL 17 with TimescaleDB extension. Sensor data stored in a hypertable partitioned by time. Queries use `time_bucket()` for aggregation.

### Docker Services

| Service | Port | Purpose |
|---------|------|---------|
| api | 8080 | Backend API |
| db | 5432 | PostgreSQL (garden_db + zitadel schemas) |
| zitadel | 8085 | Auth server |
| login (via zitadel) | 3000 | Zitadel Login v2 UI |
| swagger-ui | 8081 | API documentation |

### Environment Variables

Copy `.env.example` to `.env` and fill in:

| Variable | Description |
|----------|-------------|
| `DATABASE_URL` | PostgreSQL connection string |
| `ZITADEL_DOMAIN` | Zitadel server address (e.g., `localhost:8085`) |
| `ZITADEL_CLIENT_ID` | OAuth2 client ID from Zitadel |
| `HUB_SERVICE_ACCOUNT_ID` | Service account ID for hub device authorization |

### System Data Flow

```
Probe (STM32, BLE) → Hub (ESP32-S3, BLE scan) → Backend API (Connect RPC + JWT) → TimescaleDB
                                                                                       ↑
                                                    Mobile App (Expo, JWT user token) ──┘
```

## API Endpoints

Connect protocol (gRPC-compatible, also accepts JSON over HTTP):

- `POST /garden.v1.GardenService/InsertSensorData` — Hub-only, writes sensor readings
- `POST /garden.v1.GardenService/GetSummary` — Any authenticated user, reads aggregated data

Live API docs: https://harvesthub-gardening-tool.github.io/protos/
