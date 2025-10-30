# Harvest Hub - Backend API

A gRPC/Connect API service for collecting and querying garden sensor data, built with Go, TimescaleDB, and Protocol Buffers.

## Features

- **Time-series data collection** from garden sensors (temperature, humidity, soil moisture)
- **TimescaleDB** for efficient time-series storage and queries
- **gRPC/Connect** API with automatic OpenAPI/Swagger documentation
- **Protocol Buffers** for API definition
- **Docker Compose** setup for easy deployment

## Tech Stack

- **Go 1.24**
- **TimescaleDB** (PostgreSQL extension for time-series data)
- **Connect RPC** (gRPC-compatible protocol)
- **Protocol Buffers** (Buf tooling)
- **Swagger UI** for API documentation

## Project Structure

```
.
├── proto/              # Protocol Buffer definitions
│   └── garden/v1/      # Garden service API definition
├── gen/                # Generated code
│   ├── proto/          # Go protobuf & Connect handlers
│   └── openapiv2/      # OpenAPI/Swagger specs
├── internal/           # Internal packages
│   └── service/        # Business logic
├── server/             # HTTP server entry point
├── init.sql            # Database initialization
├── docker-compose.yml  # Docker services configuration
└── Dockerfile          # API service image
```

## Quick Start

### Prerequisites

- Docker & Docker Compose
- Go 1.24+ (for local development)
- Buf CLI (for protobuf generation)

### Run with Docker Compose

```bash
docker-compose up
```

This starts:
- **API service** on `http://localhost:8080`
- **TimescaleDB** on `localhost:5432`
- **Swagger UI** on `http://localhost:8081`

### Local Development

1. **Start TimescaleDB**:
```bash
docker-compose up db
```

2. **Run the API**:
```bash
DATABASE_URL="postgres://user:password@localhost:5432/garden_db?sslmode=disable" go run server/main.go
```

## API Endpoints

### Insert Sensor Data
```bash
POST /garden.v1.GardenService/InsertSensorData
Content-Type: application/json

{
  "node_id": "sensor_01",
  "temperature": 22.5,
  "humidity": 65.0,
  "soil_moisture": 45.0,
  "timestamp": 1698765432000
}
```

### Get Summary
```bash
POST /garden.v1.GardenService/GetSummary
Content-Type: application/json

{
  "node_id": "sensor_01",
  "hours": 24
}
```

## Protocol Buffers

The API is defined using Protocol Buffers in `proto/garden/v1/garden.proto`.

### Regenerate Code

```bash
buf generate
```

This generates:
- Go code with Connect handlers
- OpenAPI/Swagger documentation

## Database Schema

The service uses TimescaleDB with a hypertable for sensor data:

```sql
CREATE TABLE sensor_data (
  time TIMESTAMPTZ NOT NULL,
  node_id TEXT NOT NULL,
  temperature DOUBLE PRECISION,
  humidity DOUBLE PRECISION,
  soil_moisture DOUBLE PRECISION
);

SELECT create_hypertable('sensor_data', 'time');
```

## Environment Variables

- `DATABASE_URL`: PostgreSQL connection string (default: `postgres://user:password@localhost:5432/garden_db?sslmode=disable`)

## API Documentation

Access Swagger UI at `http://localhost:8081` when running with Docker Compose.

## License

Part of the Harvest Hub project.
