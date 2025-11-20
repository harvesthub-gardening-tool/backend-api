#!/bin/bash

GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[0;33m'
NC='\033[0m'

echo -e "${BLUE}=== Zitadel Setup Script ===${NC}\n"

# Check if .env exists
if [ ! -f .env ]; then
    echo -e "${YELLOW}Creating .env from .env.example...${NC}"
    cp .env.example .env
fi

echo -e "${BLUE}Starting services...${NC}"
docker-compose up -d

echo -e "\n${BLUE}Waiting for Zitadel to be ready...${NC}"
max_attempts=60
attempt=0
while [ $attempt -lt $max_attempts ]; do
    if docker-compose ps zitadel | grep -q "healthy"; then
        echo -e "\n${GREEN}✓ Zitadel is ready!${NC}\n"
        break
    fi
    attempt=$((attempt + 1))
    echo -n "."
    sleep 2
done

if [ $attempt -eq $max_attempts ]; then
    echo -e "\n${YELLOW}Zitadel is taking longer than expected. Check logs with: docker-compose logs zitadel${NC}"
    exit 1
fi

echo -e "${GREEN}=== Setup Complete! ===${NC}\n"
echo "🌐 Zitadel Console: http://localhost:8085"
echo "🔐 Login: admin@zitadel.localhost / Password1!"
echo ""
echo -e "${BLUE}Next steps:${NC}"
echo "1. Open http://localhost:8085 and login"
echo "2. Create a new project: 'Harvest Hub'"
echo "3. Create API Application:"
echo "   - Go to your project → Applications → New"
echo "   - Type: API"
echo "   - Auth Method: JWT"
echo "   - Copy the Client ID → Update .env: ZITADEL_CLIENT_ID=<client_id>"
echo ""
echo "4. Create Hub Service Account:"
echo "   - Go to Users → Service Accounts → New"
echo "   - Name: 'harvest-hub-iot'"
echo "   - Create → Generate Key (JWT)"
echo "   - Download the key.json (for your Rust Hub)"
echo "   - Copy User ID → Update .env: HUB_SERVICE_ACCOUNT_ID=<user_id>"
echo ""
echo "5. Restart API: docker-compose restart api"
