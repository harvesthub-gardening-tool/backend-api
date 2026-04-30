# Hub Provisioning Flow (auth/v2)

End-to-end QR-based hub provisioning with strict per-user data isolation.

## Overview

```
┌─────────┐  scan QR   ┌──────────┐ AssociateHub  ┌─────────┐
│  User   │ ─────────► │ Mobile   │ ────────────► │ Backend │
│         │            │ App      │ (auth'd)      │   API   │
└─────────┘            └──────────┘               └────┬────┘
                                                       │ persists
                                                       │ device_id +
                                                       │ hub_secret_hash
                                                       │ (owner = user)
                                                       ▼
                                                  ┌─────────┐
                                                  │  Hub    │ ClaimHubToken
                                                  │ (ESP32) │ (public, once)
                                                  └────┬────┘
                                                       │ JWT (HubID claim)
                                                       ▼
                                                  ┌─────────┐
                                                  │ Insert  │
                                                  │ Sensor  │
                                                  │ Data    │
                                                  └─────────┘
```

## Actors & Tokens

| Actor | Token type | Carries | Expiry |
|-------|-----------|---------|--------|
| **User** (mobile app) | User JWT | `user_id`, `username` | 24 h |
| **Hub** (ESP32 device) | Hub JWT (service account) | `user_id=0`, `hub_id` | 1 year |

## Flow

### 1. User signs up / logs in

`POST /auth.v2.AuthService/Register` or `/Login` → returns user JWT.

### 2. User associates a hub (after scanning QR)

`POST /auth.v2.AuthService/AssociateHub` *(requires user JWT)*

Request:

```json
{
  "device_id":  "<from QR>",
  "hub_secret": "<from QR>",
  "hub_name":   "Greenhouse Hub"
}
```

Backend:

- Creates a `hubs` row owned by the calling user
- Stores `device_id` (unique) + hex-encoded SHA-256 hash of `hub_secret`
- One hub ↔ one user (enforced by unique `device_id`)

### 3. Hub claims its token (once)

`POST /auth.v2.AuthService/ClaimHubToken` *(public, no auth)*

Request:

```json
{
  "device_id":  "<same as QR>",
  "hub_secret": "<same as QR>"
}
```

Backend:

- Verifies `device_id` exists and `hub_secret` matches the stored hash
- **Claim-once**: refuses if a `hub_tokens` row already exists for this hub
- Issues a hub JWT with `HubID` claim
- Stores token hash in `hub_tokens` so re-claim is blocked

Response: `{ "token": "<hub JWT>" }` — hub stores this in flash memory.

### 4. Hub sends sensor data

`POST /garden.v2.GardenService/InsertSensorData` *(requires hub JWT)*

Backend (`internal/service/garden.go`):

- Requires service-account JWT with non-empty `HubID`
- Auto-binds each `sensor_node` to the hub on first sight
- A node already bound to another hub → `PermissionDenied` (anti-spoof)

### 5. User reads aggregated data

`POST /garden.v2.GardenService/GetSummary` *(requires user JWT)*

Backend joins `sensor_data → sensor_nodes → hubs` filtered by `hubs.user_id = caller`.
**A user only ever sees data from probes attached to hubs they own.**
Optional `hub_id` request field narrows results to a single hub; each `SensorSummary` carries its `hub_id` so clients can group by hub.

## Hub re-provisioning (factory reset / reflash)

If a hub needs a fresh token (e.g. firmware reflash), the owner calls:

`POST /auth.v2.AuthService/RevokeHub` *(requires user JWT)*

```json
{ "hub_id": 42 }
```

This **hard-deletes** the `hub_tokens` row, unblocking a fresh `ClaimHubToken` with the same `device_id` + `hub_secret`. Sensor data remains intact (still bound to that `hub_id`).

### List user's hubs

`POST /auth.v2.AuthService/ListHubs` *(requires user JWT)* — returns all hubs owned by caller.

## Endpoint summary

| RPC | Auth | Purpose |
|-----|------|---------|
| `auth.v2/Register` | public | Create user account |
| `auth.v2/Login` | public | Get user JWT |
| `auth.v2/AssociateHub` | user | Bind a hub to caller (post-QR-scan) |
| `auth.v2/ListHubs` | user | List caller's hubs |
| `auth.v2/RevokeHub` | user | Delete hub token → allow re-claim |
| `auth.v2/ClaimHubToken` | public | Hub fetches its JWT (once) |
| `garden.v2/InsertSensorData` | hub | Hub uploads readings |
| `garden.v2/GetSummary` | user | User reads own aggregated data (optional `hub_id` filter) |

## Security guarantees

1. **One hub, one owner** — `device_id` UNIQUE in `hubs`.
2. **Claim-once** — `hub_tokens` row blocks duplicate JWT issuance until owner explicitly revokes.
3. **Hub identity in JWT** — `HubID` claim is signed (RS256) and verified on every `InsertSensorData`.
4. **No cross-user data leak** — `GetSummary` JOINs through `hubs.user_id`; users querying others' data get nothing.
5. **No probe spoofing** — sensor nodes are pinned to their first-seen hub; mismatched hub → request rejected.
