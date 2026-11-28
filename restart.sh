#! /bin/sh

# Rebuild and restart the local dev stack (PostGIS db).
# The Go backend is run separately with: go run ./cmd/server
docker compose --profile valhalla down
docker compose --profile valhalla up --force-recreate --remove-orphans --build -d
docker compose --profile valhalla logs -f
