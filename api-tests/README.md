# API Tests

HTTP request files for testing the Garden API with [rest.nvim](https://github.com/rest-nvim/rest.nvim).

## Setup

### Automated (Recommended)

1. **Start Zitadel and database:**
   ```bash
   docker-compose up -d db zitadel
   ```

2. **Run bootstrap script** (creates everything automatically):
   ```bash
   ./scripts/bootstrap-zitadel.sh
   ```

   This creates project, service account, and writes `.env` with all tokens.

3. **Start the API:**
   ```bash
   docker-compose up -d api
   ```

4. **Run tests with rest.nvim:**
   - Open any `.http` file in `api-tests/`
   - Position cursor on a request
   - Run `:lua require('rest-nvim').run()`

### Manual (if bootstrap fails)

1. **Start the stack:**
   ```bash
   docker-compose up
   ```

2. **Get tokens from Zitadel:**
   - Open http://localhost:8085/ui/console
   - Login as `root@harvesthub.localhost / RootPassword1!`
   - Create project → API application → copy Client ID
   - Create service account → generate PAT

3. **Set environment variables:**
   Create `.env` file in project root and set `ZITADEL_CLIENT_ID`, then restart API

## Files

- `01-health.http` - Basic connectivity check
- `02-insert-sensor-data.http` - Hub writes sensor data (requires service account token)
- `03-get-summary.http` - Query aggregated data (requires any valid token)
- `99-unauthorized.http` - Test auth failures

## Notes

- All endpoints require authentication via `Authorization: Bearer <token>`
- `InsertSensorData` only accepts service account tokens (no username in JWT)
- `GetSummary` accepts both user and service account tokens
- Tokens are JWTs validated against Zitadel at `localhost:8085`
