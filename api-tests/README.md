# API Tests

HTTP request files for testing the Garden API with [rest.nvim](https://github.com/rest-nvim/rest.nvim).

## Setup

1. **Start the stack:**
   ```bash
   docker-compose up
   ```

2. **Register a new user:**
   ```bash
   POST /auth.v1.AuthService/Register
   {"email": "user@example.com", "password": "yourpassword"}
   ```
   Returns `user_id` and JWT token.

3. **Or login with existing user:**
   ```bash
   POST /auth.v1.AuthService/Login
   {"email": "user@example.com", "password": "yourpassword"}
   ```
   Returns JWT token.

4. **Use token in requests:**
   Add header: `Authorization: Bearer <token>`

5. **Run tests with rest.nvim:**
   - Open any `.http` file in `api-tests/`
   - Position cursor on a request
   - Run `:lua require('rest-nvim').run()`

## Authentication

The API uses JWT-based authentication with bcrypt password hashing.

### Register a New User

```http
POST /auth.v1.AuthService/Register
Content-Type: application/json

{
  "email": "user@example.com",
  "password": "yourpassword"
}
```

Response includes `user_id` and JWT `token`.

### Login

```http
POST /auth.v1.AuthService/Login
Content-Type: application/json

{
  "email": "user@example.com",
  "password": "yourpassword"
}
```

Response includes JWT `token`.

### Use Token in Requests

Add the JWT token to the `Authorization` header:

```http
Authorization: Bearer <token>
```

## Files

- `01-auth.http` - Register, login, and hub token management
- `01-health.http` - Basic connectivity check
- `02-insert-sensor-data.http` - Hub writes sensor data (requires hub token)
- `03-get-summary.http` - Query aggregated data (requires user token)
- `99-unauthorized.http` - Test auth failures

## Notes

- All garden endpoints require authentication via `Authorization: Bearer <token>`
- User tokens are generated via Register/Login endpoints
- Hub tokens are managed separately for IoT device authentication
- Tokens are RS256 JWTs signed with server's RSA key pair
