#! /bin/sh

# Rebuild and restart the local dev stack (PostGIS db).
# The Go backend is run separately with: go run ./cmd/server
docker compose down
docker compose up --force-recreate --remove-orphans -d
docker compose logs -f
