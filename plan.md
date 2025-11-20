# Zitadel Authentication Integration Plan

## Overview

Integrate Zitadel as the authentication provider for Harvest Hub backend API, supporting:

- **Service account** for the Rust Hub (single machine-to-machine auth with API key)
- **OIDC/OAuth2** for TypeScript mobile app users
- **JWT validation** via Connect RPC interceptors

## Architecture

```
┌─────────────┐
│  Sensors    │ (BLE/WiFi, no auth)
│ (ESP32/IoT) │
└──────┬──────┘
       │ Local communication
       ▼
┌─────────────────┐
│   Hub (Rust)    │ ────────────────────────────┐
│  Aggregator     │                             │
└─────────────────┘                             │
                                                │
                                                │ Authenticated writes
                          ┌──────────────┐      │ (service account)
                          │   Zitadel    │      │
                          │   (Auth)     │      │
                          └──────┬───────┘      │
                                 │ Validates    │
                                 │ JWT tokens   │
┌─────────────┐                  │              │
│ Mobile App  │                  ▼              ▼
│(TypeScript) │──────────>┌────────────────────────┐
│             │           │   Backend API          │
│             │<──────────│   (Connect RPC)        │
└─────────────┘           │   + JWT validate       │
  Authenticated           └────────────────────────┘
  reads (OIDC)                      │
                                    ▼
                              ┌──────────┐
                              │TimescaleDB│
                              └──────────┘
```

**Key points:**
- **Sensors → Hub**: Local network, no backend auth needed
- **Hub → Backend**: Single service account with API key (writes sensor data)
- **Mobile App → Backend**: User authentication via OIDC (reads summaries)
- **Hub and Mobile App never communicate directly** - both talk to Backend API

---

## Phase 1: Infrastructure Setup

### 1.1 Add Zitadel to Docker Compose

**File:** `docker-compose.yml`

Add services:

- `zitadel`: Main auth server (PostgreSQL-backed)
- `zitadel-init`: One-time setup container for creating service accounts

**Configuration:**

- Zitadel admin console: `http://localhost:8085`
- Zitadel issuer URL: `http://localhost:8085`
- Database: Share TimescaleDB or create separate `zitadel_db`

**Environment variables:**

```yaml
ZITADEL_MASTERKEY: <generate secure key>
ZITADEL_EXTERNALSECURE: false # true in production
ZITADEL_EXTERNALPORT: 8085
ZITADEL_DATABASE_*: <postgres connection>
```

### 1.2 Database Schema

**File:** `init.sql` (or new `zitadel-init.sql`)

Option A: Separate database for Zitadel (recommended)

```sql
CREATE DATABASE zitadel_db;
```

Option B: Same database, different schema

```sql
CREATE SCHEMA zitadel;
-- Zitadel manages its own tables
```

**Decision:** Use separate database to avoid conflicts with TimescaleDB hypertables.

---

## Phase 2: Protocol Buffers - Auth Service Definition

### 2.1 Create Auth Proto (Optional)

**Repository:** `../protos` (harvest-hub/protos)
**File:** `auth/v1/auth.proto`

Define minimal service for mobile app:

```protobuf
syntax = "proto3";

package auth.v1;

service AuthService {
  // User authentication (mobile app only)
  rpc Login(LoginRequest) returns (LoginResponse);
  rpc Register(RegisterRequest) returns (RegisterResponse);
  rpc RefreshToken(RefreshTokenRequest) returns (RefreshTokenResponse);
}

message LoginRequest {
  string email = 1;
  string password = 2;
}

message LoginResponse {
  string access_token = 1;
  string refresh_token = 2;
  int64 expires_in = 3; // seconds
  string user_id = 4;
}

// ... other messages
```

**Note:** Service account management is done via Zitadel admin console (one-time setup), not via API.

**Actions:**

1. Create `auth/v1/auth.proto` in protos repo
2. Run `buf generate` to generate Go/Rust/TypeScript clients
3. Update `protos-go`, `protos-rust`, `protos-ts` modules
4. Update backend `go.mod` to use new version

### 2.2 Update Garden Proto (Optional)

**File:** `garden/v1/garden.proto`

Add authentication annotations if needed:

```protobuf
import "google/api/annotations.proto";

service GardenService {
  rpc InsertSensorData(...) {
    // Requires authentication
    option (google.api.method_signature) = "authorization";
  }
}
```

**Decision:** Keep garden.proto unchanged, handle auth at middleware level.

---

## Phase 3: Backend API Changes

### 3.1 Add Dependencies

**File:** `go.mod`

```bash
go get github.com/zitadel/oidc/v3
go get github.com/zitadel/zitadel-go/v3
go get github.com/lestrrat-go/jwx/v2
```

### 3.2 Create Auth Package

**New directory:** `internal/auth/`

**Files:**

- `internal/auth/zitadel.go` - Zitadel client wrapper
- `internal/auth/jwt.go` - JWT validation logic
- `internal/auth/middleware.go` - Connect interceptor for auth
- `internal/auth/service.go` - AuthService implementation

**Key functions:**

```go
// jwt.go
func ValidateToken(token string, issuer string, audience string) (*Claims, error)

// middleware.go
func AuthInterceptor(zitadelClient *ZitadelClient) connect.UnaryInterceptorFunc

// service.go
type AuthService struct {
    zitadel *ZitadelClient
    db      *pgxpool.Pool
}
func (s *AuthService) Login(ctx, req) (*LoginResponse, error)
```

### 3.3 Update Main Server

**File:** `server/main.go`

Changes:

1. Initialize Zitadel client
2. Create AuthService
3. Register AuthService handler
4. Add auth interceptor to GardenService

```go
// Initialize Zitadel
zitadelClient := auth.NewZitadelClient(
    os.Getenv("ZITADEL_ISSUER"),
    os.Getenv("ZITADEL_PROJECT_ID"),
)

// Create services
authSvc := auth.NewAuthService(zitadelClient, db)
gardenSvc := service.NewGardenService(db)

// Register handlers
mux := http.NewServeMux()

// Auth service (no interceptor)
authPath, authHandler := authv1connect.NewAuthServiceHandler(authSvc)
mux.Handle(authPath, authHandler)

// Garden service (with auth interceptor)
gardenPath, gardenHandler := gardenv1connect.NewGardenServiceHandler(
    gardenSvc,
    connect.WithInterceptors(auth.AuthInterceptor(zitadelClient)),
)
mux.Handle(gardenPath, gardenHandler)
```

### 3.4 Update Database Schema

**File:** `init.sql`

Add optional user metadata table (if not storing everything in Zitadel):

```sql
CREATE TABLE users (
    user_id TEXT PRIMARY KEY,      -- Zitadel user ID
    email TEXT UNIQUE NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Optional: Track Hub service account for auditing
CREATE TABLE hub_metadata (
    hub_id TEXT PRIMARY KEY,           -- Zitadel service account ID
    name TEXT NOT NULL,
    location TEXT,                     -- Physical location of hub
    registered_at TIMESTAMPTZ DEFAULT NOW(),
    last_seen TIMESTAMPTZ
);

-- Update last_seen on each insert
CREATE OR REPLACE FUNCTION update_hub_last_seen()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE hub_metadata 
    SET last_seen = NOW() 
    WHERE hub_id = current_setting('app.hub_id', true);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
```

**Decision:** Keep user data in Zitadel, only store minimal references in our DB.

### 3.5 Update Garden Service (Authorization)

**File:** `internal/service/garden.go`

Add authorization checks:

```go
func (s *GardenService) InsertSensorData(ctx context.Context, req *connect.Request[...]) {
    // Extract user/service account from context (set by interceptor)
    claims := auth.GetClaimsFromContext(ctx)
    if claims == nil {
        return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthorized"))
    }
    
    // Only Hub service account can write sensor data
    if !claims.IsServiceAccount() {
        return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only hub can insert data"))
    }
    
    // Optional: Verify it's the correct Hub service account
    if claims.Subject != os.Getenv("HUB_SERVICE_ACCOUNT_ID") {
        return nil, connect.NewError(connect.CodePermissionDenied, errors.New("unauthorized hub"))
    }
    
    // ... existing insert logic
}

func (s *GardenService) GetSummary(ctx context.Context, req *connect.Request[...]) {
    claims := auth.GetClaimsFromContext(ctx)
    if claims == nil {
        return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthorized"))
    }
    
    // Both users and hub can read data
    // Future: Filter by user's gardens (multi-tenancy)
    
    // ... existing query logic
}
```

---

## Phase 4: Client Integration Guides

### 4.1 Rust Client (Sensors)

**Documentation:** Update backend README

**Service account flow:**

1. Admin creates service account in Zitadel console
2. Get API key (JWT or long-lived token)
3. Store in sensor configuration
4. Include in every request:
    ```rust
    let client = GardenServiceClient::new(
        "http://localhost:8080",
        Some("Bearer <API_KEY>")
    );
    ```

### 4.2 TypeScript Client (Mobile App)

**Documentation:** Update backend README

**OIDC flow:**

1. User clicks "Login"
2. App redirects to Zitadel login page (or uses embedded webview)
3. User authenticates
4. Zitadel redirects back with authorization code
5. App exchanges code for access token
6. Store token securely (encrypted storage)
7. Include in requests:
    ```typescript
    const transport = createConnectTransport({
        baseUrl: "http://localhost:8080",
        interceptors: [
            (next) => async (req) => {
                req.header.set("Authorization", `Bearer ${accessToken}`);
                return await next(req);
            },
        ],
    });
    ```

---

## Phase 5: Configuration & Deployment

### 5.1 Environment Variables

**File:** `.env.example` (create new)

```bash
# Backend API
DATABASE_URL=postgres://user:password@db:5432/garden_db?sslmode=disable

# Zitadel
ZITADEL_ISSUER=http://localhost:8085
ZITADEL_PROJECT_ID=<project_id>
ZITADEL_DATABASE_URL=postgres://user:password@db:5432/zitadel_db?sslmode=disable
ZITADEL_MASTERKEY=<generate_with_openssl_rand_base64_32>

# Production overrides
# ZITADEL_EXTERNALSECURE=true
# ZITADEL_ISSUER=https://auth.harvesthub.com
```

### 5.2 Zitadel Initial Setup Script

**File:** `scripts/setup-zitadel.sh` (create new)

Automate initial Zitadel configuration:

1. Create project
2. Create API application
3. Create first service account for testing
4. Output configuration values

### 5.3 Update Docker Compose for Production

**File:** `docker-compose.prod.yml` (create new)

Changes:

- Enable TLS for Zitadel
- Use proper secrets management
- Set `ZITADEL_EXTERNALSECURE=true`
- Configure reverse proxy (nginx/traefik)

---

## Phase 6: Testing & Documentation

### 6.1 Testing Plan

- [ ] Unit tests for JWT validation
- [ ] Integration tests for auth flow
- [ ] Test service account token validation
- [ ] Test OIDC flow (manual, document steps)
- [ ] Test unauthorized access returns 401
- [ ] Test permission denied returns 403

### 6.2 Update Documentation

**Files to update:**

- `README.md` - Add authentication section
- `CONTRIBUTING.md` (if exists) - How to test with auth locally
- API docs (auto-generated from protos)

**New documentation:**

- `docs/authentication.md` - Detailed auth architecture
- `docs/setup-guide.md` - First-time Zitadel setup
- `docs/client-integration.md` - Rust & TypeScript examples

---

## Migration Strategy

### Option A: Big Bang (Recommended for early project)

1. Implement all changes in feature branch
2. Test thoroughly
3. Deploy with Zitadel in one go
4. Existing test data still accessible (no auth on db level)

### Option B: Gradual Rollout

1. Add Zitadel infrastructure (no enforcement)
2. Add auth endpoints (optional to use)
3. Make auth mandatory for new endpoints
4. Migrate existing endpoints

**Decision:** Use Option A since project is early stage.

---

## Security Considerations

1. **Secrets Management:**
    - Never commit `ZITADEL_MASTERKEY` to git
    - Use environment variables or secrets manager
    - Rotate service account keys regularly

2. **Token Validation:**
    - Validate JWT signature against Zitadel's JWKS
    - Check token expiration
    - Verify audience and issuer
    - Cache JWKS with TTL (reduce Zitadel load)

3. **Authorization:**
    - Service accounts: limit to specific node_ids
    - Users: can only read their own data (future: multi-tenancy)
    - Admins: can create service accounts

4. **Rate Limiting:**
    - Add rate limiter middleware (future phase)
    - Protect auth endpoints from brute force

5. **Production Checklist:**
    - [ ] Enable HTTPS for Zitadel
    - [ ] Set `ZITADEL_EXTERNALSECURE=true`
    - [ ] Configure proper CORS origins
    - [ ] Enable Zitadel audit logs
    - [ ] Set up monitoring for auth failures

---

## Timeline Estimate

- **Phase 1** (Infrastructure): 2-3 hours
- **Phase 2** (Protos): 2-3 hours
- **Phase 3** (Backend): 6-8 hours
- **Phase 4** (Client guides): 2 hours
- **Phase 5** (Config/deploy): 2 hours
- **Phase 6** (Testing/docs): 3-4 hours

**Total:** ~20-25 hours

---

## Open Questions / Decisions Needed

1. **Database:** Separate DB for Zitadel or shared with TimescaleDB?
    - **Answer:** Separate DB

2. **User registration:** Self-service or admin-only?
    - **Answer:** Self-service for mobile app users
    - **Hub service account:** Admin-only (one-time setup via Zitadel console)

3. **Token expiration:** How long should access tokens last?
    - **Answer:** 
      - Mobile app: 1 hour (access), 7 days (refresh)
      - Hub service account: 1 year (long-lived, rotate annually)

4. **Multi-tenancy:** Will users have separate "gardens" in the future?
    - **Answer:** Not for now
    - **Current:** Single Hub per deployment, all users view same garden
    - **Future consideration:** Multiple hubs with garden_id scoping

5. **Branding:** Use Zitadel UI as-is or custom UI immediately?
    - **Answer:** Zitadel UI for admin panel, but custom on mobil

---

## Rollback Plan

If issues arise after deployment:

1. **Emergency:** Remove auth interceptor from GardenService (open access)
2. **Partial:** Keep auth service running but make it optional
3. **Full rollback:** Revert to previous version, remove Zitadel containers

**Data safety:** Authentication changes don't affect sensor_data table, rollback is safe.

---

## Next Steps

1. Review this plan and discuss open questions
2. Create feature branch: `feature/zitadel-auth`
3. Start with Phase 1 (docker-compose changes)
4. Create proto definitions in parallel
5. Implement backend changes
6. Test with mock clients
7. Document and merge

---

## References

- Zitadel Docs: https://zitadel.com/docs
- Zitadel Go SDK: https://github.com/zitadel/zitadel-go
- Connect RPC Interceptors: https://connectrpc.com/docs/go/interceptors
- JWT Best Practices: https://datatracker.ietf.org/doc/html/rfc8725
