#!/usr/bin/env bash
# Launch the Douceville Go server with the variables from .env.
# The variables are exported only into this script's process (and the server),
# never into the calling shell.
#
# Usage: ./launch_server.sh [path/to/.env]
set -euo pipefail

cd "$(dirname "$0")"
ENV_FILE="${1:-.env}"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "error: $ENV_FILE not found (copy sample.env to .env and fill it in)" >&2
  exit 1
fi

set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
set +a

# IPv6 loopback (::1) hangs on this host; force IPv4 unless overridden in .env.
export POSTGRES_HOST="${POSTGRES_HOST:-127.0.0.1}"

# The OpenRouteService key is only needed with the default routing provider.
required=(JWT_SECRET POSTGRES_PASSWORD)
[[ "${ROUTING_PROVIDER:-ors}" == valhalla ]] || required+=(OPENROUTESERVICE_KEY)
missing=()
for var in "${required[@]}"; do
  [[ -n "${!var:-}" ]] || missing+=("$var")
done
if (( ${#missing[@]} )); then
  echo "error: missing variables in $ENV_FILE: ${missing[*]}" >&2
  exit 1
fi

echo "Starting Douceville on ${LISTEN_ADDR:-:3566} (db ${POSTGRES_HOST}:${POSTGRES_PORT:-5432})"
exec go run ./cmd/server
