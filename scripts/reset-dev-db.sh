#!/bin/bash

# Harvest Hub - Local Dev DB Reset Script
# WARNING: This script is destructive and deletes all local database data.

set -e

# Get the directory where the script is located
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_ROOT"

echo "⚠️  WARNING: This will delete the 'postgres_data' volume and all local database data."
read -p "Are you sure you want to proceed? (y/N) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]
then
    echo "Reset cancelled."
    exit 1
fi

echo "Stopping containers and removing volumes..."
docker compose down -v

echo "Rebuilding and starting containers..."
docker compose up -d --build

echo "✅ Local development database has been reset."
echo "Auto-seeding sensor data from db/seed.sql..."
echo "API domain tables will be recreated by GORM AutoMigrate on startup."
