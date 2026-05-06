# Local Development Database Workflow

This document explains how to manage the local TimescaleDB instance for Harvest Hub development.

## Architecture

The backend uses a dual-database approach:

1. **TimescaleDB (Hypertable)**: Manages `sensor_data` for efficient time-series storage. Initialized via `db/schema.sql`.
2. **PostgreSQL (GORM)**: Manages domain models (Users, Hubs, Probes, Motor Commands). Handled by `AutoMigrate` in `server/main.go`.

## Destructive Dev DB Reset

If your local database state is inconsistent or you need to test the full initialization flow from scratch:

```bash
# WARNING: This deletes all local database data (volumes).
# Run from the backend-api/ directory.
./scripts/reset-dev-db.sh
```

Or manually:

```bash
docker compose down -v && docker compose up -d --build
```

**What this does:**
1. Stops all backend containers.
2. **Deletes the `postgres_data` Docker volume.**
3. Recreates the `db` container.
4. Executes `db/schema.sql` (creates `sensor_data` hypertable).
5. Executes `db/seed.sql` (inserts sample sensor data).
6. Starts the `api` service.
7. `api` service runs `AutoMigrate`, recreating all domain tables (`auth_users`, `hubs`, `motor_commands`, etc.).

## Seeding & Test Data

### Automatic Seeding
When the database is first created (or reset using the command above), `db/seed.sql` automatically populates 48 hours of sensor data for `node-1` and `node-2`.

### Manual Seeding / Testing Hubs & Probes
Domain tables managed by GORM are not seeded by `db/seed.sql`. To test features like hub tokens or motor commands, follow this flow:

#### 1. Register and Login (User JWT)
```bash
# Register
curl -X POST http://localhost:8080/auth.v2.AuthService/Register \
  -H "Content-Type: application/json" \
  -d '{"email": "test@example.com", "password": "password123"}'

# Login to get JWT
USER_JWT=$(curl -s -X POST http://localhost:8080/auth.v2.AuthService/Login \
  -H "Content-Type: application/json" \
  -d '{"email": "test@example.com", "password": "password123"}' | jq -r .token)
```

#### 2. Provision a Hub (Hub JWT)
Follow `docs/HUB_PROVISIONING.md` or use these snippets:
```bash
# Associate hub to user
curl -X POST http://localhost:8080/auth.v2.AuthService/AssociateHub \
  -H "Authorization: Bearer $USER_JWT" \
  -H "Content-Type: application/json" \
  -d '{"deviceId": "hub-01", "hubSecret": "secret123", "hubName": "Dev Hub"}'

# Claim hub token
HUB_JWT=$(curl -s -X POST http://localhost:8080/auth.v2.AuthService/ClaimHubToken \
  -H "Content-Type: application/json" \
  -d '{"deviceId": "hub-01", "hubSecret": "secret123"}' | jq -r .token)
```

#### 3. Register a Probe (via Sensor Data)
Hubs automatically register probes when they send data for a new `nodeId`.
```bash
curl -X POST http://localhost:8080/garden.v2.GardenService/InsertSensorData \
  -H "Authorization: Bearer $HUB_JWT" \
  -H "Content-Type: application/json" \
  -d '{"nodeId": "probe-01", "temperature": 22.5, "humidity": 65.0, "timestamp": '$(date +%s000)'}'
```

## Motor Command Workflow Examples

Test the motor command queue using the `/control.v1.ControlService/` endpoints.

### 1. Create a Motor Command (User JWT)
```bash
curl -X POST http://localhost:8080/control.v1.ControlService/CreateMotorCommand \
  -H "Authorization: Bearer $USER_JWT" \
  -H "Content-Type: application/json" \
  -d '{
    "nodeId": "probe-01",
    "hubId": "1",
    "action": "MOTOR_COMMAND_ACTION_RUN_FOR_DURATION",
    "durationMs": 3000,
    "idempotencyKey": "unique-uuid-123"
  }'
```

### 2. Hub Polls for Pending Commands (Hub JWT)
```bash
curl -X POST http://localhost:8080/control.v1.ControlService/PullPendingMotorCommands \
  -H "Authorization: Bearer $HUB_JWT" \
  -H "Content-Type: application/json" \
  -d '{
    "maxCommands": 5,
    "leaseDurationMs": 15000
  }'
```

### 3. Hub Acknowledges Event (Hub JWT)
```bash
curl -X POST http://localhost:8080/control.v1.ControlService/AckMotorCommandEvent \
  -H "Authorization: Bearer $HUB_JWT" \
  -H "Content-Type: application/json" \
  -d '{
    "commandId": "uuid-from-step-1",
    "status": "MOTOR_COMMAND_STATUS_SENT_TO_PROBE",
    "reasonCode": "MOTOR_COMMAND_REASON_CODE_NONE"
  }'
```

### 4. User Checks Command Status (User JWT)
```bash
curl -X POST http://localhost:8080/control.v1.ControlService/GetMotorCommandStatus \
  -H "Authorization: Bearer $USER_JWT" \
  -H "Content-Type: application/json" \
  -d '{"commandId": "uuid-from-step-1"}'
```

## Running Tests from Clean State

To ensure your changes work with a fresh database:

1. Perform a [Destructive Reset](#destructive-dev-db-reset).
2. Run the Go test suite:
   ```bash
   go test ./...
   ```

Note that the Go tests typically use an isolated environment (SQLite or dedicated test DB) and do not depend on the running `db` container, unless they are specific integration tests designed to use the `DATABASE_URL`.
