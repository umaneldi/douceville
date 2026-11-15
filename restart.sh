#! /bin/sh

# Rebuild and restart the local dev stack (PostGIS db).
# The Go backend is run separately with: go run ./cmd/server
sudo docker compose -f docker-compose.dev.yml -p dvtest up --build --remove-orphans -d
sudo docker compose -f docker-compose.dev.yml -p dvtest logs -f
