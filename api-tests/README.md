# API Tests

HTTP request files for testing the Garden API with [rest.nvim](https://github.com/rest-nvim/rest.nvim).

## Setup

1. **Start the stack:**
   ```bash
   docker-compose up
   ```

2. **Run requests in order** — tokens are captured automatically:
   - Execute **Register** or **Login** in `04-auth-v2.http` → sets `{{USER_TOKEN_V2}}`
   - Execute **AssociateHub** in `04-auth-v2.http` → sets `{{DEVICE_ID}}`, `{{HUB_SECRET}}`, `{{HUB_ID_V2}}`
   - Execute **ClaimHubToken** in `05-claim-hub-token.http` → sets `{{HUB_TOKEN_V2}}`
   - Subsequent requests use the captured tokens without manual copy-paste

3. **Run with rest.nvim:**
   - Open any `.http` file in `api-tests/`
   - Position cursor on a request
   - Run `:lua require('rest-nvim').run()`

## Token Capture

Post-request Lua handlers automatically store tokens in global variables:

```lua
> {%
# @lang=lua
local json = vim.json.decode(response.body)
if json and json.token then
  client.global.set("USER_TOKEN_V2", json.token)
end
%}
```

Variables persist for the duration of the Neovim session. Re-run Register/Login to refresh an expired token.

## Files

Run in this order to exercise the full QR-code provisioning flow:

1. `04-auth-v2.http` — Register/Login + AssociateHub + ListHubs + RevokeHub
   (sets `USER_TOKEN_V2`, `DEVICE_ID`, `HUB_SECRET`, `HUB_ID_V2`)
2. `05-claim-hub-token.http` — Public hub token claim flow with claim-once enforcement
   (sets `HUB_TOKEN_V2`)
3. `06-insert-with-v2-hub.http` — Verify the v2-claimed JWT works for `InsertSensorData`
4. `01-health.http` — Basic connectivity check
5. `02-insert-sensor-data.http` — Hub writes sensor data (uses `HUB_TOKEN_V2`)
6. `03-get-summary.http` — Query aggregated data (uses `USER_TOKEN_V2`)
7. `99-unauthorized.http` — Test auth failures

## Notes

- All garden endpoints require authentication via `Authorization: Bearer {{TOKEN}}`
- User tokens expire in 24h; hub tokens expire in 1 year
- Tokens are RS256 JWTs signed with the server's RSA key pair
