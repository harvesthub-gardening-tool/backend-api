# Task 20 cross-repo integration harness

This task uses a hardware-free backend harness in `internal/service/control_service_task20_harness_test.go`.

## Backend harness

Run the targeted backend lifecycle and failure matrix with:

```bash
bash ./scripts/run-task-20-backend-harness.sh
```

The harness directly exercises `ControlService` with the same in-memory SQLite pattern already used by `control_service_test.go`, covering:

- create -> poll -> ack `SENT_TO_PROBE` -> ack `SUCCEEDED` -> status
- unauthorized create
- duplicate idempotency replay
- active-command duplicate rejection
- expired command not dispatched when the hub misses the poll window
- offline probe simulation via failed ack with `PROBE_UNREACHABLE`

## Cross-repo verification commands

Run these alongside the backend harness and store the captured outputs under `../hub-core/.sisyphus/evidence/`:

```bash
# mobile-app
npm test -- --runInBand __tests__/services/controlService.test.ts

# hub-core
cargo fmt --check && cargo check

# probe-core
cargo fmt --check && cargo check
```

## Hardware boundary

This harness does **not** claim physical BLE/UART end-to-end success. Hub/probe validation for Task 20 is compile/simulation evidence only because this environment has no target hardware attached, and probe/hub test targets are constrained by embedded tooling.
