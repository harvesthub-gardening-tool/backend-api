#!/bin/bash

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== Zitadel Setup Script ===${NC}\n"

# Check if .env file exists
if [ ! -f .env ]; then
    echo -e "${RED}Error: .env file not found${NC}"
    echo "Please create .env from .env.example first:"
    echo "  cp .env.example .env"
    exit 1
fi

# Check if ZITADEL_MASTERKEY is set
source .env
if [ -z "$ZITADEL_MASTERKEY" ]; then
    echo -e "${BLUE}Generating ZITADEL_MASTERKEY...${NC}"
    MASTERKEY=$(openssl rand -base64 32)
    echo "ZITADEL_MASTERKEY=$MASTERKEY" >> .env
    echo -e "${GREEN}✓ Master key generated and added to .env${NC}\n"
else
    echo -e "${GREEN}✓ ZITADEL_MASTERKEY already set${NC}\n"
fi

echo -e "${BLUE}Starting services...${NC}"
docker-compose up -d db zitadel

echo -e "\n${BLUE}Waiting for Zitadel to be healthy...${NC}"
max_attempts=30
attempt=0
while [ $attempt -lt $max_attempts ]; do
    if docker-compose ps zitadel | grep -q "healthy"; then
        echo -e "${GREEN}✓ Zitadel is healthy${NC}\n"
        break
    fi
    attempt=$((attempt + 1))
    echo -n "."
    sleep 2
done

if [ $attempt -eq $max_attempts ]; then
    echo -e "\n${RED}Error: Zitadel failed to become healthy${NC}"
    exit 1
fi

echo -e "${GREEN}=== Zitadel Setup Complete ===${NC}\n"
echo "Next steps:"
echo "1. Open Zitadel admin console: http://localhost:8085"
echo "2. Login with default credentials (first time setup will prompt you)"
echo "3. Create a new project for Harvest Hub"
echo "4. Create service account for Hub:"
echo "   - Go to Users → Service Accounts → New"
echo "   - Name: 'harvest-hub-iot'"
echo "   - Generate API key (JWT)"
echo "5. Create API application for mobile app:"
echo "   - Go to Projects → Your Project → Applications → New"
echo "   - Type: API"
echo "   - Authentication: JWT"
echo "6. Update .env with:"
echo "   - ZITADEL_PROJECT_ID=<your_project_id>"
echo "   - HUB_SERVICE_ACCOUNT_ID=<service_account_id>"
echo ""
echo "7. Save the Hub's JWT key securely for your Rust Hub configuration"
echo ""
echo -e "${BLUE}Documentation: https://zitadel.com/docs/guides/integrate/service-users/authenticate-service-users${NC}"
