#!/usr/bin/env bash
# Build Valhalla routing tiles from OpenStreetMap extracts and upload them to an
# S3-compatible object storage, from which the Valhalla service of Douceville
# downloads them on demand (mjolnir.tile_url).
#
# This script runs on a BUILD machine, never in the production image: the
# production Valhalla container only reads the tiles from S3.
#
# Usage:
#   scripts/build_valhalla_tiles.sh [options]
#
# Options:
#   --region URL          OSM extract (.osm.pbf) to build; repeatable.
#                         Default: Geofabrik France (mainland + Corsica).
#   --drom                Also build the overseas departments (Guadeloupe,
#                         Martinique, Guyane, Réunion, Mayotte).
#   --workdir DIR         Working directory (default: ./.valhalla-build).
#   --upload S3_URI       Upload the tiles to S3_URI (e.g. s3://bucket/valhalla/fr).
#   --endpoint URL        S3 endpoint URL (any S3-compatible storage), required
#                         with --upload unless AWS_ENDPOINT_URL is set.
#   --public-url URL      Public HTTP URL of S3_URI, used to print the
#                         VALHALLA_TILE_URL value (default: <endpoint>/<bucket>/<prefix>).
#   --docker-network NET  Docker network of the upload container (when the S3
#                         storage is itself a local container).
#   --skip-build          Only upload the tiles already in --workdir.
#   --dry-run             Print the commands without running them.
#   -h, --help            Show this help.
#
# Environment:
#   VALHALLA_IMAGE        Valhalla image (default ghcr.io/valhalla/valhalla:3.9.3).
#                         Use the SAME version as the service: the tile format
#                         changes between Valhalla versions.
#   AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_DEFAULT_REGION
#                         Credentials of the S3 storage (upload only), passed
#                         to the amazon/aws-cli container.
#   AWS_ENDPOINT_URL      Default for --endpoint.
#
# Requirements and notes:
#   - Docker. For all of France: about 8-16 GB of RAM, 25-30 GB of free disk
#     and 1-3 hours on 4-8 cores (much less for a small --region).
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

VALHALLA_IMAGE=${VALHALLA_IMAGE:-ghcr.io/valhalla/valhalla:3.9.3}
AWS_IMAGE=${AWS_IMAGE:-amazon/aws-cli:latest}
GEOFABRIK=https://download.geofabrik.de
FRANCE=$GEOFABRIK/europe/france-latest.osm.pbf
DROM=(
  "$GEOFABRIK/europe/france/guadeloupe-latest.osm.pbf"
  "$GEOFABRIK/europe/france/martinique-latest.osm.pbf"
  "$GEOFABRIK/europe/france/guyane-latest.osm.pbf"
  "$GEOFABRIK/europe/france/reunion-latest.osm.pbf"
  "$GEOFABRIK/europe/france/mayotte-latest.osm.pbf"
)

regions=()
drom=false
workdir=./.valhalla-build
upload=""
endpoint=${AWS_ENDPOINT_URL:-}
public_url=""
docker_network=""
skip_build=false
dry_run=false

usage() { sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'; }
die() { echo "error: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case $1 in
    --region) regions+=("${2:?--region needs a URL}"); shift 2 ;;
    --drom) drom=true; shift ;;
    --workdir) workdir=${2:?--workdir needs a directory}; shift 2 ;;
    --upload) upload=${2:?--upload needs an s3:// URI}; shift 2 ;;
    --endpoint) endpoint=${2:?--endpoint needs a URL}; shift 2 ;;
    --public-url) public_url=${2:?--public-url needs a URL}; shift 2 ;;
    --docker-network) docker_network=${2:?--docker-network needs a network}; shift 2 ;;
    --skip-build) skip_build=true; shift ;;
    --dry-run) dry_run=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option $1 (see --help)" ;;
  esac
done

[ ${#regions[@]} -gt 0 ] || regions=("$FRANCE")
if $drom; then regions+=("${DROM[@]}"); fi
if [ -n "$upload" ]; then
  [[ $upload == s3://*/* || $upload == s3://* ]] || die "--upload must be an s3:// URI (got $upload)"
  [ -n "$endpoint" ] || die "--endpoint (or AWS_ENDPOINT_URL) is required with --upload"
fi
$skip_build && [ -z "$upload" ] && die "--skip-build without --upload does nothing"

run() {
  echo "+ $*"
  $dry_run || "$@"
}

$dry_run || mkdir -p "$workdir"
workdir=$(realpath -m "$workdir")

if ! $skip_build; then
  # 1. OSM extracts.
  pbfs=()
  for url in "${regions[@]}"; do
    f=$(basename "$url")
    pbfs+=("/build/pbf/$f")
    if [ -s "$workdir/pbf/$f" ]; then
      echo "= $f already downloaded"
    else
      run mkdir -p "$workdir/pbf"
      run curl -fL --retry 3 -o "$workdir/pbf/$f.part" "$url"
      run mv "$workdir/pbf/$f.part" "$workdir/pbf/$f"
    fi
  done

  # 2. Configuration, administrative areas and tiles, in the Valhalla image.
  #    Previous tiles are removed: valhalla_build_tiles does not delete tiles
  #    of roads that disappeared.
  run rm -rf "$workdir/tiles"
  run docker run --rm -v "$workdir:/build" "$VALHALLA_IMAGE" bash -euc "
    valhalla_build_config --mjolnir-tile-dir /build/tiles --mjolnir-tile-extract '' \
      --mjolnir-traffic-extract '' --mjolnir-admin /build/admin.sqlite \
      --mjolnir-timezone '' > /build/valhalla.json
    valhalla_build_admins -c /build/valhalla.json ${pbfs[*]}
    valhalla_build_tiles -c /build/valhalla.json ${pbfs[*]}
    find /build/tiles -name '*.gph' | wc -l | xargs echo 'tiles built:'
  "
fi

# 3. Upload to the S3-compatible storage (tiles directory, mirrored).
if [ -n "$upload" ]; then
  [ $dry_run = true ] || [ -d "$workdir/tiles" ] || die "no tiles in $workdir/tiles (build them first)"
  net=()
  [ -z "$docker_network" ] || net=(--network "$docker_network")
  run docker run --rm "${net[@]}" -v "$workdir/tiles:/tiles:ro" \
    -e AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY -e AWS_DEFAULT_REGION \
    "$AWS_IMAGE" s3 sync /tiles "$upload" --endpoint-url "$endpoint" --delete --only-show-errors

  if [ -z "$public_url" ]; then
    public_url="${endpoint%/}/${upload#s3://}"
  fi
  echo
  echo "Tiles uploaded. Configure the Valhalla service with:"
  echo "  VALHALLA_TILE_URL=${public_url%/}/{tilePath}"
  echo "then empty ./.valhalla/tiles and restart it: docker compose --profile valhalla up -d --force-recreate valhalla"
fi
