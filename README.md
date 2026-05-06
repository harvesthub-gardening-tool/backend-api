# Harvest Hub - Backend API

A gRPC/Connect API service for collecting and querying garden sensor data, built with Go, TimescaleDB, and Protocol Buffers.

## Features

- **Time-series data collection** from garden sensors (temperature, humidity, soil moisture)
- **TimescaleDB** for efficient time-series storage and queries
- **gRPC/Connect** API with automatic OpenAPI/Swagger documentation
- **Protocol Buffers** for type-safe API definitions
- **Docker Compose** setup for easy deployment

## Tech Stack

- **Go 1.24**
- **TimescaleDB** (PostgreSQL extension for time-series data)
- **Connect RPC** (gRPC-compatible protocol)
- **Protocol Buffers** via [harvesthub-gardening-tool/protos](https://github.com/harvesthub-gardening-tool/protos)

## Project Structure

```
.
├── internal/           # Internal packages
│   └── service/        # Business logic (GardenService)
├── server/             # HTTP server entry point
├── init.sql            # Database initialization
├── docker-compose.yml  # Docker services configuration
├── Dockerfile          # API service image
└── go.mod              # Go dependencies
```

## 📚 API Documentation

**Live API Docs:** https://harvesthub-gardening-tool.github.io/protos/

Interactive Swagger UI with all endpoints, request/response examples, and schemas.

## Quick Start

### Prerequisites

- Docker & Docker Compose
- Go 1.24+ (for local development)

### Run with Docker Compose

```bash
docker-compose up
```

This starts:
- **API service** on `http://localhost:8080`
- **TimescaleDB** on `localhost:5432`

### Local Development

1. **Start TimescaleDB**:
```bash
docker-compose up db
```

2. **Run the API**:
```bash
DATABASE_URL="postgres://user:password@localhost:5432/garden_db?sslmode=disable" go run server/main.go
```

### Testing

Run the full backend test suite:

```bash
go test ./...
```

Run a single package:

```bash
go test ./internal/service/ -v
```

Run a single test by name:

```bash
go test ./internal/service/ -run TestFunctionName -v
```

Generate a coverage report:

```bash
go test ./... -coverprofile=coverage.out
go tool cover -func=coverage.out
```

## API Endpoints

All endpoints use Connect protocol (gRPC-compatible):

All endpoints require a JWT in the `Authorization: Bearer <token>` header except `auth.v2/Register`, `auth.v2/Login`, and `auth.v2/ClaimHubToken`.

### Insert Sensor Data *(requires hub JWT)*
```bash
curl -X POST http://localhost:8080/garden.v2.GardenService/InsertSensorData \
  -H "Authorization: Bearer <hub-jwt>" \
  -H "Content-Type: application/json" \
  -d '{
    "node_id": "sensor_01",
    "temperature": 22.5,
    "humidity": 65.0,
    "soil_moisture": 45.0,
    "timestamp": 1698765432000
  }'
```

### Get Summary *(requires user JWT)*
```bash
curl -X POST http://localhost:8080/garden.v2.GardenService/GetSummary \
  -H "Authorization: Bearer <user-jwt>" \
  -H "Content-Type: application/json" \
  -d '{
    "node_id": "sensor_01",
    "hours": 24
  }'
```

Optional `hub_id` field narrows results to a single hub; each `SensorSummary` includes the `hub_id` it belongs to.

See `docs/HUB_PROVISIONING.md` for the full QR-code-based hub provisioning flow.

## Protocol Buffers Integration

This backend uses the centrally managed proto definitions from [protos repository](https://github.com/harvesthub-gardening-tool/protos).

### Dependencies

The proto-generated code is imported as a Go module:

```go
import (
    gardenv2 "github.com/harvesthub-gardening-tool/protos-go/garden/v2"
    "github.com/harvesthub-gardening-tool/protos-go/garden/v2/gardenv2connect"
    authv2 "github.com/harvesthub-gardening-tool/protos-go/auth/v2"
    "github.com/harvesthub-gardening-tool/protos-go/auth/v2/authv2connect"
)
```

### Update Proto Definitions

```bash
# Get latest proto code
go get -u github.com/harvesthub-gardening-tool/protos-go@latest
go mod tidy

# Rebuild
go build ./server
```

### Test Proto Changes Before Merging

When testing a feature branch from the protos repo:

```bash
# Use feature branch code
go get github.com/harvesthub-gardening-tool/protos-go@feature/your-feature
go mod tidy
go build ./server

# Test the changes work
# ...

# Once merged, update to latest
go get -u github.com/harvesthub-gardening-tool/protos-go@latest
```

## Database Schema

The service uses TimescaleDB with a hypertable for efficient time-series queries:

```sql
CREATE TABLE sensor_data (
  time TIMESTAMPTZ NOT NULL,
  node_id TEXT NOT NULL,
  temperature DOUBLE PRECISION,
  humidity DOUBLE PRECISION,
  soil_moisture DOUBLE PRECISION
);

SELECT create_hypertable('sensor_data', 'time');
CREATE INDEX idx_node_id ON sensor_data (node_id, time DESC);
```

**Time-series features:**
- Automatic partitioning by time
- Efficient aggregation queries with `time_bucket()`
- Compression for older data
- Continuous aggregates (future)

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | `postgres://user:password@localhost:5432/garden_db?sslmode=disable` | PostgreSQL connection string |

## Development Workflow

1. **Update proto definitions** in [protos repo](https://github.com/harvesthub-gardening-tool/protos)
2. **Test with feature branch**: `go get @feature/xyz`
3. **Merge proto PR** → auto-publishes to protos-go
4. **Update backend**: `go get -u @latest`
5. **Deploy** updated backend

## Architecture

```
┌─────────────┐
│   Sensors   │ (ESP32, IoT devices)
└──────┬──────┘
       │ HTTP/JSON
       ▼
┌─────────────────────────────┐
│   Backend API (Connect)     │
│  - InsertSensorData         │
│  - GetSummary               │
└──────────┬──────────────────┘
           │
           ▼
    ┌──────────────┐
    │ TimescaleDB  │
    │ (time-series)│
    └──────────────┘
```

## License

Part of the Harvest Hub project.
