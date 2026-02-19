#!/bin/bash
set -e

# Bootstrap script for Zitadel development setup
# Creates project, application, and service account automatically

ZITADEL_URL="${ZITADEL_URL:-http://localhost:8085}"
ADMIN_USER="${ADMIN_USER:-root@harvesthub.localhost}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-RootPassword1!}"
PROJECT_NAME="${PROJECT_NAME:-Garden API}"
APP_NAME="${APP_NAME:-Garden Backend}"
SERVICE_ACCOUNT_NAME="${SERVICE_ACCOUNT_NAME:-hub-service-account}"

echo "🚀 Bootstrapping Zitadel for development..."

# Wait for Zitadel to be ready
echo "⏳ Waiting for Zitadel to be ready..."
for i in {1..30}; do
  if curl -sf "$ZITADEL_URL/debug/ready" > /dev/null 2>&1; then
    echo "✅ Zitadel is ready"
    break
  fi
  if [ $i -eq 30 ]; then
    echo "❌ Zitadel failed to start"
    exit 1
  fi
  sleep 2
done

# Login and get access token
echo "🔐 Logging in as admin..."
LOGIN_RESPONSE=$(curl -s -X POST "$ZITADEL_URL/oauth/v2/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=password" \
  -d "username=$ADMIN_USER" \
  -d "password=$ADMIN_PASSWORD" \
  -d "scope=openid profile email urn:zitadel:iam:org:project:id:zitadel:aud") || {
    echo "❌ Login failed. Check credentials."
    exit 1
  }

ACCESS_TOKEN=$(echo "$LOGIN_RESPONSE" | jq -r '.access_token')

if [ "$ACCESS_TOKEN" = "null" ] || [ -z "$ACCESS_TOKEN" ]; then
  echo "❌ Failed to get access token"
  echo "Response: $LOGIN_RESPONSE"
  exit 1
fi

echo "✅ Logged in successfully"

# Get organization ID
echo "📋 Getting organization ID..."
ORG_RESPONSE=$(curl -s -X POST "$ZITADEL_URL/management/v1/orgs/_search" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"queries":[{"nameQuery":{"name":"HarvestHub","method":"TEXT_QUERY_METHOD_EQUALS"}}]}')

ORG_ID=$(echo "$ORG_RESPONSE" | jq -r '.result[0].id')

if [ "$ORG_ID" = "null" ] || [ -z "$ORG_ID" ]; then
  echo "❌ Failed to get organization ID"
  exit 1
fi

echo "✅ Organization ID: $ORG_ID"

# Check if project already exists
echo "🔍 Checking for existing project..."
PROJECT_RESPONSE=$(curl -s -X POST "$ZITADEL_URL/management/v1/projects/_search" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"queries\":[{\"nameQuery\":{\"name\":\"$PROJECT_NAME\",\"method\":\"TEXT_QUERY_METHOD_EQUALS\"}}]}")

PROJECT_ID=$(echo "$PROJECT_RESPONSE" | jq -r '.result[0].id')

if [ "$PROJECT_ID" = "null" ] || [ -z "$PROJECT_ID" ]; then
  echo "📦 Creating project..."
  CREATE_PROJECT_RESPONSE=$(curl -s -X POST "$ZITADEL_URL/management/v1/projects" \
    -H "Authorization: Bearer $ACCESS_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"$PROJECT_NAME\"}")

  PROJECT_ID=$(echo "$CREATE_PROJECT_RESPONSE" | jq -r '.id')
  echo "✅ Project created: $PROJECT_ID"
else
  echo "✅ Project already exists: $PROJECT_ID"
fi

# Check if API application already exists
echo "🔍 Checking for existing API application..."
APP_RESPONSE=$(curl -s -X POST "$ZITADEL_URL/management/v1/projects/$PROJECT_ID/apps/_search" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"queries\":[{\"nameQuery\":{\"name\":\"$APP_NAME\",\"method\":\"TEXT_QUERY_METHOD_EQUALS\"}}]}")

APP_ID=$(echo "$APP_RESPONSE" | jq -r '.result[0].id')
CLIENT_ID=$(echo "$APP_RESPONSE" | jq -r '.result[0].apiConfig.clientId')

if [ "$APP_ID" = "null" ] || [ -z "$APP_ID" ]; then
  echo "🔧 Creating API application..."
  CREATE_APP_RESPONSE=$(curl -s -X POST "$ZITADEL_URL/management/v1/projects/$PROJECT_ID/apps/api" \
    -H "Authorization: Bearer $ACCESS_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"$APP_NAME\",\"authMethodType\":\"API_AUTH_METHOD_TYPE_BASIC\"}")

  CLIENT_ID=$(echo "$CREATE_APP_RESPONSE" | jq -r '.clientId')
  echo "✅ API application created"
else
  echo "✅ API application already exists"
fi

echo "✅ Client ID: $CLIENT_ID"

# Check if service account already exists
echo "🔍 Checking for existing service account..."
SA_RESPONSE=$(curl -s -X POST "$ZITADEL_URL/management/v1/users/_search" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"queries\":[{\"userNameQuery\":{\"userName\":\"$SERVICE_ACCOUNT_NAME\",\"method\":\"TEXT_QUERY_METHOD_EQUALS\"}}]}")

SA_ID=$(echo "$SA_RESPONSE" | jq -r '.result[0].id')

if [ "$SA_ID" = "null" ] || [ -z "$SA_ID" ]; then
  echo "🤖 Creating service account..."
  CREATE_SA_RESPONSE=$(curl -s -X POST "$ZITADEL_URL/management/v1/users/machine" \
    -H "Authorization: Bearer $ACCESS_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"userName\":\"$SERVICE_ACCOUNT_NAME\",\"name\":\"Hub Service Account\",\"description\":\"Service account for Hub device\",\"accessTokenType\":\"ACCESS_TOKEN_TYPE_JWT\"}")

  SA_ID=$(echo "$CREATE_SA_RESPONSE" | jq -r '.userId')
  echo "✅ Service account created: $SA_ID"
else
  echo "✅ Service account already exists: $SA_ID"
fi

# Create Personal Access Token for service account
echo "🔑 Creating Personal Access Token..."
PAT_RESPONSE=$(curl -s -X POST "$ZITADEL_URL/management/v1/users/$SA_ID/pats" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"expirationDate":"2029-12-31T23:59:59Z"}')

PAT_TOKEN=$(echo "$PAT_RESPONSE" | jq -r '.token')

if [ "$PAT_TOKEN" = "null" ] || [ -z "$PAT_TOKEN" ]; then
  echo "⚠️  Could not create new PAT (may already exist)"
  echo "💡 Use existing token or manually create one in Zitadel console"
  PAT_TOKEN="<manually_generated_token>"
else
  echo "✅ Personal Access Token created"
fi

# Write configuration to .env file
echo ""
echo "📝 Writing configuration to .env..."
cat > .env <<EOF
# Generated by bootstrap-zitadel.sh on $(date)

# API Configuration
API_URL=http://localhost:8080

# Zitadel Configuration
ZITADEL_DOMAIN=localhost:8085
ZITADEL_CLIENT_ID=$CLIENT_ID
HUB_SERVICE_ACCOUNT_ID=$SA_ID

# Service Account Token (Hub)
HUB_TOKEN=$PAT_TOKEN

# User Token (get from OAuth flow)
USER_TOKEN=
EOF

echo "✅ Configuration written to .env"
echo ""
echo "🎉 Bootstrap complete!"
echo ""
echo "📋 Summary:"
echo "  - Project ID: $PROJECT_ID"
echo "  - Client ID: $CLIENT_ID"
echo "  - Service Account ID: $SA_ID"
echo ""
echo "🚀 Next steps:"
echo "  1. Restart the API: docker-compose restart api"
echo "  2. Test with: cd api-tests && <run .http files>"
echo ""
echo "💡 For user tokens, login at: $ZITADEL_URL/ui/console"
