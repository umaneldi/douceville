#!/usr/bin/env bash
# Build Valhalla routing tiles from OpenStreetMap extracts and publish them to
# the S3-compatible object storage configured in .env, from which the Valhalla
# service of Douceville downloads them on demand (mjolnir.tile_url).
#
# This script runs on a BUILD machine (Linux or macOS), never in the production
# image: the production Valhalla container only reads the tiles from S3. It
# uses no Docker: the Valhalla tools and the AWS CLI run natively.
#
# Usage:
#   scripts/build_valhalla_tiles.sh [options]
#
# Options:
#   --env-file PATH       Configuration file (default: .env at the repository root).
#   --region URL          OSM extract (.osm.pbf) to build; repeatable.
#                         Default: Geofabrik France (mainland + Corsica).
#   --drom                Also build the overseas departments (Guadeloupe,
#                         Martinique, Guyane, Réunion, Mayotte).
#   --workdir DIR         Local working directory: OSM extracts and tiles being
#                         built (default: ./.valhalla-build).
#   --jobs N              Build threads (default: number of CPUs).
#   --skip-build          Only publish the tiles already built in --workdir.
#   --dry-run             Print the commands without running them.
#   -h, --help            Show this help.
#
# Configuration (read from the env file; the environment is used as fallback):
#   VALHALLA_S3_URI       Required. Target of the tiles, e.g. s3://bucket/valhalla/fr.
#                         Its content is MIRRORED (stale tiles are deleted).
#   VALHALLA_S3_ENDPOINT  Required. S3 endpoint URL (any S3-compatible storage);
#                         fallback AWS_ENDPOINT_URL.
#   AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_DEFAULT_REGION
#                         Credentials of the S3 storage (or AWS_PROFILE / ~/.aws).
#   VALHALLA_TILE_URL     Public URL of the tiles (.../{tilePath}), as used by the
#                         service: checked after publication if set.
#   VALHALLA_VERSION      Valhalla version (default: tag of VALHALLA_IMAGE, i.e.
#                         3.9.3). Use the SAME version as the service: the tile
#                         format changes between Valhalla versions.
#   VALHALLA_ALLOW_VERSION_MISMATCH=1
#                         Accept installed Valhalla tools of another version.
#
# Requirements and notes:
#   - bash 3.2+, curl, and either:
#       * uv (macOS: `brew install uv`): the tools come from the pyvalhalla
#         package of the right version (wheels: Linux x86_64/arm64, macOS 15+
#         on Apple Silicon) and the AWS CLI from the awscli package; or
#       * Valhalla tools of the same version on PATH (e.g. `brew install
#         valhalla`, needed on Intel Macs) and the AWS CLI (`brew install awscli`).
#   - For all of France: about 8-16 GB of RAM, 25-30 GB of free disk and 1-3
#     hours on 4-8 cores (much less for a small --region).
#   - Valhalla only writes tiles to a local disk: they are built in --workdir,
#     then immediately synchronised to VALHALLA_S3_URI (aws s3 sync --delete).
#   - The tiles are a directory of .gph files (no .tar: a tar cannot be loaded
#     lazily over HTTP).
#   - The bucket (or prefix) must be PUBLICLY READABLE over HTTP, or served by a
#     proxy: Valhalla does not sign S3 requests (it only supports HTTP basic
#     auth, see VALHALLA_TILE_URL_USER_PW).
#   - After an update, empty the tile cache of the service (./.valhalla/tiles)
#     and restart it, otherwise it keeps serving the previously cached tiles.
#   - The tiles are derived from OpenStreetMap data (ODbL): keep the
#     attribution "© OpenStreetMap contributors".
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
GEOFABRIK=https://download.geofabrik.de
FRANCE=$GEOFABRIK/europe/france-latest.osm.pbf
DROM=(
  "$GEOFABRIK/europe/france/guadeloupe-latest.osm.pbf"
  "$GEOFABRIK/europe/france/martinique-latest.osm.pbf"
  "$GEOFABRIK/europe/france/guyane-latest.osm.pbf"
  "$GEOFABRIK/europe/france/reunion-latest.osm.pbf"
  "$GEOFABRIK/europe/france/mayotte-latest.osm.pbf"
)

env_file=$ROOT/.env
regions=()
drom=false
workdir=./.valhalla-build
jobs=""
skip_build=false
dry_run=false

usage() { sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'; }
die() { echo "error: $*" >&2; exit 1; }
warn() { echo "warning: $*" >&2; }

while [ $# -gt 0 ]; do
  case $1 in
    --env-file) env_file=${2:?--env-file needs a path}; shift 2 ;;
    --region) regions+=("${2:?--region needs a URL}"); shift 2 ;;
    --drom) drom=true; shift ;;
    --workdir) workdir=${2:?--workdir needs a directory}; shift 2 ;;
    --jobs) jobs=${2:?--jobs needs a number}; shift 2 ;;
    --skip-build) skip_build=true; shift ;;
    --dry-run) dry_run=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option $1 (see --help)" ;;
  esac
done

# --- Configuration (.env), checked before any download ---------------------
[ -f "$env_file" ] || die "$env_file not found (copy sample.env to .env and fill the Valhalla section)"
set -a
# shellcheck disable=SC1090
. "$env_file"
set +a
# Empty credentials (as in sample.env) would hide AWS_PROFILE / ~/.aws.
for v in AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_DEFAULT_REGION AWS_PROFILE; do
  eval "[ -n \"\${$v:-}\" ] || unset $v"
done

s3_uri=${VALHALLA_S3_URI:-}
endpoint=${VALHALLA_S3_ENDPOINT:-${AWS_ENDPOINT_URL:-}}
tile_url=${VALHALLA_TILE_URL:-}
image=${VALHALLA_IMAGE:-ghcr.io/valhalla/valhalla:3.9.3}
version=${VALHALLA_VERSION:-${image##*:}}

[ -n "$s3_uri" ] || die "VALHALLA_S3_URI is not set in $env_file (e.g. s3://bucket/valhalla/fr)"
case $s3_uri in
  s3://?*) ;;
  *) die "VALHALLA_S3_URI must be an s3:// URI (got $s3_uri)" ;;
esac
[ -n "$endpoint" ] || die "VALHALLA_S3_ENDPOINT (or AWS_ENDPOINT_URL) is not set in $env_file"
case $version in
  [0-9]*.[0-9]*) ;;
  *) die "cannot infer the Valhalla version from VALHALLA_IMAGE=$image: set VALHALLA_VERSION" ;;
esac
if [ -n "$jobs" ]; then
  case $jobs in *[!0-9]*|0) die "--jobs must be a positive number (got $jobs)" ;; esac
else
  jobs=$(getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)
fi

[ ${#regions[@]} -gt 0 ] || regions=("$FRANCE")
if $drom; then regions+=("${DROM[@]}"); fi

run() {
  echo "+ $*"
  $dry_run || "$@"
}

have() { command -v "$1" >/dev/null 2>&1; }

# --- Tools (no Docker) ------------------------------------------------------
# valhalla_cmd / aws_cmd: command prefix of the Valhalla tools and of the AWS CLI.
valhalla_cmd=()
resolve_valhalla() {
  if have valhalla_build_tiles && have valhalla_build_admins && have valhalla_build_config; then
    local installed
    installed=$(valhalla_build_tiles --version 2>&1 | head -n 1 || true)
    case " $installed " in
      *[!0-9.]"$version"[!0-9.]*) ;;
      *)
        [ "${VALHALLA_ALLOW_VERSION_MISMATCH:-}" = 1 ] ||
          die "installed Valhalla tools report '$installed', expected $version (same version as the service); remove them from PATH to use uv, or set VALHALLA_ALLOW_VERSION_MISMATCH=1"
        warn "using Valhalla '$installed' instead of $version"
        ;;
    esac
    valhalla_cmd=()
  elif have uvx; then
    valhalla_cmd=(uvx --from "pyvalhalla==$version")
  else
    die "Valhalla tools not found: install uv (macOS: brew install uv) or Valhalla $version itself (brew install valhalla)"
  fi
}

aws_cmd=()
resolve_aws() {
  if have aws; then
    aws_cmd=(aws)
  elif have uvx; then
    aws_cmd=(uvx --from awscli aws)
  else
    die "AWS CLI not found: install uv (macOS: brew install uv) or the AWS CLI (brew install awscli)"
  fi
}

have curl || die "curl not found"
$skip_build || resolve_valhalla
resolve_aws

$dry_run || mkdir -p "$workdir"
# Absolute path (no `realpath -m`: absent on macOS).
case $workdir in
  /*) ;;
  *) workdir=$PWD/${workdir#./} ;;
esac
workdir=${workdir%/}

if ! $skip_build; then
  # 1. OSM extracts.
  pbfs=()
  for url in "${regions[@]}"; do
    f=$(basename "$url")
    pbfs+=("$workdir/pbf/$f")
    if [ -s "$workdir/pbf/$f" ]; then
      echo "= $f already downloaded"
    else
      run mkdir -p "$workdir/pbf"
      run curl -fL --retry 3 -o "$workdir/pbf/$f.part" "$url"
      run mv "$workdir/pbf/$f.part" "$workdir/pbf/$f"
    fi
  done

  # 2. Configuration, administrative areas and tiles. Previous tiles are
  #    removed: valhalla_build_tiles does not delete tiles of roads that
  #    disappeared.
  run rm -rf "$workdir/tiles"
  echo "+ ${valhalla_cmd[*]+${valhalla_cmd[*]} }valhalla_build_config ... > $workdir/valhalla.json"
  if ! $dry_run; then
    ${valhalla_cmd[@]+"${valhalla_cmd[@]}"} valhalla_build_config \
      --mjolnir-tile-dir "$workdir/tiles" --mjolnir-tile-extract '' \
      --mjolnir-traffic-extract '' --mjolnir-admin "$workdir/admin.sqlite" \
      --mjolnir-timezone '' --mjolnir-concurrency "$jobs" > "$workdir/valhalla.json"
  fi
  run ${valhalla_cmd[@]+"${valhalla_cmd[@]}"} valhalla_build_admins -c "$workdir/valhalla.json" "${pbfs[@]}"
  run ${valhalla_cmd[@]+"${valhalla_cmd[@]}"} valhalla_build_tiles -c "$workdir/valhalla.json" "${pbfs[@]}"
fi

# 3. Publication to the S3-compatible storage (tiles directory, mirrored).
if ! $dry_run; then
  [ -d "$workdir/tiles" ] || die "no tiles in $workdir/tiles (build them first)"
  count=$(find "$workdir/tiles" -name '*.gph' | wc -l | tr -d ' ')
  [ "$count" -gt 0 ] || die "no .gph tile in $workdir/tiles: refusing to mirror an empty directory to $s3_uri"
  echo "= $count tiles to publish"
fi
run "${aws_cmd[@]}" s3 sync "$workdir/tiles" "$s3_uri" \
  --endpoint-url "$endpoint" --delete --only-show-errors

# 4. Check that a tile is publicly readable, as the service will read it.
if [ -z "$tile_url" ]; then
  echo
  echo "Tiles published. Configure the Valhalla service in .env with, e.g.:"
  echo "  VALHALLA_TILE_URL=${endpoint%/}/${s3_uri#s3://}/{tilePath}"
elif ! $dry_run; then
  sample=$(cd "$workdir/tiles" && find . -name '*.gph' | head -n 1)
  sample=${sample#./}
  url=$(printf '%s' "$tile_url" | sed "s|{tilePath}|$sample|")
  if curl -fsI -o /dev/null "$url"; then
    echo "= tile readable at $url"
  else
    warn "tile not readable at $url: the bucket must be public (or VALHALLA_TILE_URL is wrong)"
  fi
fi
echo "Then empty ./.valhalla/tiles and restart the service: docker compose --profile valhalla up -d --force-recreate valhalla"
