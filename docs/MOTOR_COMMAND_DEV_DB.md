# Motor command dev DB notes

`motor_commands` and `motor_command_events` are managed by GORM `AutoMigrate` in `server/main.go`, alongside the existing auth and hub tables.

## Queue invariants

- `sensor_data` remains Timescale-managed via `db/schema.sql`; these command tables do not change hypertable behavior.
- Command lookup is unique by `command_id`.
- Idempotency is enforced with a composite uniqueness constraint on `(user_id, node_id, idempotency_key)`.
- Hub polling is supported by indexes over `(hub_id, status, expires_at, lease_expires_at)` and `(status, node_id)`.
- The intended "one active command per node" rule is documented but not implemented as a DB partial unique index because the current project relies on portable GORM `AutoMigrate` for both PostgreSQL dev databases and SQLite tests. Service-layer command creation should enforce that invariant for non-terminal statuses.

## Clean dev DB reset

If you need a fresh local Timescale dev database so `AutoMigrate` recreates the command tables from scratch, follow the [Local Development Database Workflow](DEV_DB_WORKFLOW.md#destructive-dev-db-reset).

**WARNING**: `docker compose down -v` removes the local `postgres_data` Docker volume and is for development only.

## Test Examples

See [Motor Command Workflow Examples](DEV_DB_WORKFLOW.md#motor-command-workflow-examples) for exact `curl` commands using the `control.v1.ControlService` endpoints.
