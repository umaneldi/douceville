#!/usr/bin/env bash
# Build Valhalla routing tiles from OpenStreetMap extracts. The tiles are left
# in --workdir/tiles; the script ends by printing how to copy them to the
# server running the Valhalla service of Douceville, which reads them from
# ./.valhalla/tiles (mounted as /data/tiles in the container).
#
# This script runs on a BUILD machine (Linux or macOS), or on the server
# itself if it has enough RAM, never in the production image: the production
# Valhalla container only reads the tiles. It uses no Docker: the Valhalla
# tools run natively.
#
# Usage:
#   scripts/build_valhalla_tiles.sh [options]
#
# Options:
#   --env-file PATH       Configuration file (default: .env at the repository
#                         root, read if present).
#   --region URL          OSM extract (.osm.pbf) to build; repeatable.
#                         Default: Geofabrik France (mainland + Corsica).
#   --drom                Also build the overseas departments (Guadeloupe,
#                         Martinique, Guyane, Réunion, Mayotte).
#   --workdir DIR         Working directory: OSM extracts and tiles, kept after
#                         the build (default: ./.valhalla-build).
#   --jobs N              Build threads (default: number of CPUs).
#   --dry-run             Print the commands without running them.
#   -h, --help            Show this help.
#
# Configuration (read from the env file; the environment is used as fallback):
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
#         on Apple Silicon); or
#       * Valhalla tools of the same version on PATH (e.g. `brew install
#         valhalla`, needed on Intel Macs).
#   - For all of France: about 8-16 GB of RAM, 25-30 GB of free disk and 1-3
#     hours on 4-8 cores (much less for a small --region). The tiles
#     themselves take a few GB.
#   - The tiles are a directory of .gph files, rebuilt from scratch at each
#     run (valhalla_build_tiles does not delete tiles of removed roads): copy
#     them with rsync --delete, then recreate the service (it keeps tiles and
#     its connectivity map in memory).
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
env_file_set=false
regions=()
drom=false
workdir=./.valhalla-build
jobs=""
dry_run=false

usage() { sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'; }
die() { echo "error: $*" >&2; exit 1; }
warn() { echo "warning: $*" >&2; }
# With set -e, a failing command would otherwise stop the script silently.
trap 'rc=$?; echo "error: command failed (exit $rc) at line $LINENO: $BASH_COMMAND" >&2' ERR

while [ $# -gt 0 ]; do
  case $1 in
    --env-file) env_file=${2:?--env-file needs a path}; env_file_set=true; shift 2 ;;
    --region) regions+=("${2:?--region needs a URL}"); shift 2 ;;
    --drom) drom=true; shift ;;
    --workdir) workdir=${2:?--workdir needs a directory}; shift 2 ;;
    --jobs) jobs=${2:?--jobs needs a number}; shift 2 ;;
    --dry-run) dry_run=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option $1 (see --help)" ;;
  esac
done

# --- Configuration (.env, optional), checked before any download -----------
if [ -f "$env_file" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$env_file"
  set +a
elif $env_file_set; then
  die "$env_file not found"
fi

image=${VALHALLA_IMAGE:-ghcr.io/valhalla/valhalla:3.9.3}
version=${VALHALLA_VERSION:-${image##*:}}
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
# valhalla_cmd: command prefix of the Valhalla tools.
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

have curl || die "curl not found"
resolve_valhalla

$dry_run || mkdir -p "$workdir"
# Absolute path (no `realpath -m`: absent on macOS).
case $workdir in
  /*) ;;
  *) workdir=$PWD/${workdir#./} ;;
esac
workdir=${workdir%/}

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

# 3. The tiles stay in $workdir/tiles: check them, then tell where they go.
if ! $dry_run; then
  [ -d "$workdir/tiles" ] || die "no tiles in $workdir/tiles: the build failed"
  count=$(find "$workdir/tiles" -name '*.gph' | wc -l | tr -d ' ')
  [ "$count" -gt 0 ] || die "no .gph tile in $workdir/tiles: the build failed"
  size=$(du -sh "$workdir/tiles" | cut -f1)
  echo "= $count tiles ($size) built in $workdir/tiles"
fi
echo
echo "Copy them to .valhalla/tiles/ in the Douceville checkout of the server running"
echo "the valhalla service (stale tiles must be removed: --delete), then recreate it:"
echo "  rsync -a --delete --delay-updates $workdir/tiles/ <server>:<douceville>/.valhalla/tiles/"
case $workdir/ in
  "$ROOT"/*)
    echo "or, if this checkout runs the service:"
    echo "  rsync -a --delete --delay-updates $workdir/tiles/ $ROOT/.valhalla/tiles/"
    ;;
esac
echo "then, on the server:"
echo "  docker compose --profile valhalla up -d --force-recreate valhalla"
