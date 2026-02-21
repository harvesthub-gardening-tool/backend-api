# API Tests

HTTP request files for testing the Garden API with [rest.nvim](https://github.com/rest-nvim/rest.nvim).

## Setup

1. **Start the stack:**
   ```bash
   docker-compose up
   ```

2. **Run requests in order** — tokens are captured automatically:
   - Execute **Register** or **Login** in `01-auth.http` → sets `{{USER_TOKEN}}`
   - Execute **CreateHubToken** in `01-auth.http` → sets `{{HUB_TOKEN}}`
   - All other requests use `{{USER_TOKEN}}` / `{{HUB_TOKEN}}` without manual copy-paste

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
  client.global.set("USER_TOKEN", json.token)
end
%}
```

Variables persist for the duration of the Neovim session. Re-run Register/Login to refresh an expired token.

## Files

- `01-auth.http` - Register, login, and hub token management (sets `USER_TOKEN`, `HUB_TOKEN`)
- `01-health.http` - Basic connectivity check
- `02-insert-sensor-data.http` - Hub writes sensor data (uses `HUB_TOKEN`)
- `03-get-summary.http` - Query aggregated data (uses `USER_TOKEN`)
- `99-unauthorized.http` - Test auth failures

## Notes

- All garden endpoints require authentication via `Authorization: Bearer {{TOKEN}}`
- User tokens expire in 24h; hub tokens expire in 1 year
- Tokens are RS256 JWTs signed with the server's RSA key pair
