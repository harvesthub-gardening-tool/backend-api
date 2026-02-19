# Development Scripts

## bootstrap-zitadel.sh

Automatically sets up Zitadel for local development:

- Creates project and API application
- Creates service account for Hub device
- Generates Personal Access Token
- Writes all config to `.env` file

### Usage

```bash
# Start Zitadel first
docker-compose up -d db zitadel

# Wait for it to be healthy (30-60 seconds)
docker-compose ps

# Run bootstrap
./scripts/bootstrap-zitadel.sh

# Restart API with new config
docker-compose restart api
```

### What it creates

- **Project**: "Garden API"
- **Application**: "Garden Backend" (API type with JWT)
- **Service Account**: "hub-service-account"
- **PAT**: Personal Access Token with 5-year expiry

### Output

Writes `.env` file with:
- `ZITADEL_CLIENT_ID`
- `HUB_SERVICE_ACCOUNT_ID`
- `HUB_TOKEN` (service account PAT)

### Requirements

- `curl`
- `jq` (install: `sudo pacman -S jq` or `sudo apt install jq`)
- Zitadel running and healthy

### Troubleshooting

**"Login failed"**: Check that root user exists with correct password
- Username: `root@harvesthub.localhost`
- Password: `RootPassword1!`

**"jq: command not found"**: Install jq JSON processor

**Token already exists**: If PAT creation fails, manually generate in console:
1. Go to http://localhost:8085/ui/console
2. Login as `root@harvesthub.localhost / RootPassword1!`
3. Users → Service Accounts → hub-service-account
4. Personal Access Tokens → Generate Token
5. Copy token to `.env` as `HUB_TOKEN`
