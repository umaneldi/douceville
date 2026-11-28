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
#   VALHALLA_S3_ACL       Canned ACL of the uploaded tiles (default public-read;
#                         empty: none, e.g. for a storage without ACL support
#                         whose bucket is made public otherwise).
#   VALHALLA_TILE_URL     Public URL of the tiles (.../{tilePath}), as used by the
#                         service: anonymous reads are checked there after
#                         publication (default: URLs derived from the endpoint).
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
#   - The tiles must be PUBLICLY READABLE over HTTP, or served by a proxy:
#     Valhalla does not sign S3 requests (it only supports HTTP basic auth, see
#     VALHALLA_TILE_URL_USER_PW). A public-read bucket ACL only allows listing:
#     each object needs its own ACL, hence VALHALLA_S3_ACL. aws s3 sync only
#     uploads changed tiles: unchanged ones keep their previous ACL.
#   - Some storages (e.g. OVHcloud) reject anonymous path-style URLs
#     (endpoint/bucket/...) with HTTP 400: use the virtual-hosted form
#     (https://bucket.endpoint-host/...) in VALHALLA_TILE_URL.
#   - Publication is checked: bucket access before the upload, exit status of
#     the sync, then the number of .gph objects listed in VALHALLA_S3_URI must
#     equal the number of local tiles, and a few tiles must be readable
#     without credentials, as the service reads them; any failure stops the
#     script (exit 1).
#   - Recent AWS CLIs send integrity checksums that many S3-compatible storages
#     reject or mishandle: AWS_REQUEST_CHECKSUM_CALCULATION and
#     AWS_RESPONSE_CHECKSUM_VALIDATION default to when_required (overridable).
#   - After an update, or a change of VALHALLA_TILE_URL (the service then
#     aborts with "Tile URL changed"), empty the tile cache of the service (./.valhalla/tiles)
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
# With set -e, a failing command would otherwise stop the script silently.
trap 'rc=$?; echo "error: command failed (exit $rc) at line $LINENO: $BASH_COMMAND" >&2' ERR

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
s3_uri=${s3_uri%/}
endpoint=${VALHALLA_S3_ENDPOINT:-${AWS_ENDPOINT_URL:-}}
tile_url=${VALHALLA_TILE_URL:-}
tile_auth=${VALHALLA_TILE_URL_USER_PW:-}
# Unset: public-read; set but empty: no ACL sent.
acl=${VALHALLA_S3_ACL-public-read}
image=${VALHALLA_IMAGE:-ghcr.io/valhalla/valhalla:3.9.3}
version=${VALHALLA_VERSION:-${image##*:}}

[ -n "$s3_uri" ] || die "VALHALLA_S3_URI is not set in $env_file (e.g. s3://bucket/valhalla/fr)"
case $s3_uri in
  s3://?*) ;;
  *) die "VALHALLA_S3_URI must be an s3:// URI (got $s3_uri)" ;;
esac
[ -n "$endpoint" ] || die "VALHALLA_S3_ENDPOINT (or AWS_ENDPOINT_URL) is not set in $env_file"
bucket=${s3_uri#s3://}
bucket=${bucket%%/*}
prefix=${s3_uri#s3://"$bucket"}
prefix=${prefix#/}
# Checksums of recent AWS CLIs (CRC32, aws-chunked) break many S3-compatible
# storages: only send them when the API requires it.
export AWS_REQUEST_CHECKSUM_CALCULATION=${AWS_REQUEST_CHECKSUM_CALCULATION:-when_required}
export AWS_RESPONSE_CHECKSUM_VALIDATION=${AWS_RESPONSE_CHECKSUM_VALIDATION:-when_required}
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

  # 3a. Access to the bucket (credentials, endpoint, bucket name), before
  #     uploading anything.
  echo "+ ${aws_cmd[*]} s3api head-bucket --bucket $bucket --endpoint-url $endpoint"
  "${aws_cmd[@]}" s3api head-bucket --bucket "$bucket" --endpoint-url "$endpoint" ||
    die "bucket '$bucket' not reachable at $endpoint (see the AWS CLI message above): check VALHALLA_S3_URI, VALHALLA_S3_ENDPOINT and the AWS_* credentials"
fi

# 3b. Upload. Exit status of aws s3 sync: 1 = some transfers failed,
#     2 = some files skipped or invalid parameters.
acl_opt=()
[ -z "$acl" ] || acl_opt=(--acl "$acl")
echo "+ ${aws_cmd[*]} s3 sync $workdir/tiles $s3_uri --endpoint-url $endpoint --delete --only-show-errors${acl:+ --acl $acl}"
if ! $dry_run; then
  rc=0
  "${aws_cmd[@]}" s3 sync "$workdir/tiles" "$s3_uri" \
    --endpoint-url "$endpoint" --delete --only-show-errors ${acl_opt[@]+"${acl_opt[@]}"} || rc=$?
  case $rc in
    0) ;;
    1) die "upload to $s3_uri failed for one or more tiles (aws s3 sync exit 1, see the messages above)" ;;
    2) die "upload to $s3_uri incomplete: files skipped or invalid parameters (aws s3 sync exit 2, see the messages above)" ;;
    *) die "upload to $s3_uri failed (aws s3 sync exit $rc)" ;;
  esac

  # 3c. The tiles must really be in the bucket: compare the number of .gph
  #     objects listed under the prefix with the local count. `aws s3 ls`
  #     exits 1 without message when the prefix is empty: only stderr tells an
  #     error apart.
  ls_err=$workdir/s3-ls.err
  listing=$("${aws_cmd[@]}" s3 ls "$s3_uri/" --recursive --endpoint-url "$endpoint" 2>"$ls_err") ||
    [ ! -s "$ls_err" ] || die "cannot list $s3_uri after the upload: $(cat "$ls_err")"
  rm -f "$ls_err"
  remote=$(printf '%s\n' "$listing" | grep -c '\.gph$' || true)
  [ "$remote" -gt 0 ] ||
    die "aws s3 sync reported success but no .gph object is listed in $s3_uri: check that VALHALLA_S3_ENDPOINT is the storage the bucket belongs to"
  [ "$remote" -eq "$count" ] ||
    die "$remote .gph objects in $s3_uri, $count expected: publication incomplete, run again with --skip-build"
  echo "= $remote tiles published in $s3_uri"
fi

# 4. Anonymous read of a few tiles (first, middle, last), as the service reads
#    them (GET, no S3 signature): a private object or a URL form rejected by
#    the storage would otherwise only show up as "No suitable edges near
#    location" errors of the service.
tile_status() { # URL -> HTTP status (000: no answer)
  local auth=()
  [ -z "$tile_auth" ] || auth=(-u "$tile_auth")
  curl -s -m 30 -r 0-0 -o /dev/null -w '%{http_code}' ${auth[@]+"${auth[@]}"} "$1" || true
}
if ! $dry_run; then
  tiles=$(cd "$workdir/tiles" && find . -name '*.gph' | sed 's|^\./||' | LC_ALL=C sort)
  n=$(printf '%s\n' "$tiles" | wc -l | tr -d ' ')
  samples=$(printf '%s\n' "$tiles" | sed -n "1p;$(( (n + 1) / 2 ))p;${n}p" | LC_ALL=C sort -u)

  # Candidate URLs: VALHALLA_TILE_URL, else virtual-hosted then path-style.
  if [ -n "$tile_url" ]; then
    candidates=$tile_url
  else
    scheme=${endpoint%%://*}
    host=${endpoint#*://}
    host=${host%%/*}
    candidates="$scheme://$bucket.$host/${prefix:+$prefix/}{tilePath}
${endpoint%/}/$bucket/${prefix:+$prefix/}{tilePath}"
  fi

  readable=""
  report=""
  strong=""
  weak=""
  for candidate in $candidates; do
    ok=true
    for t in $samples; do
      url=$(printf '%s' "$candidate" | sed "s|{tilePath}|$t|")
      code=$(tile_status "$url")
      report="$report
  $code $url"
      case $code in
        200|206) continue ;;
        # 400 / no answer: mostly a URL form of the other candidate.
        400|000) [ -n "$weak" ] || weak=$code ;;
        *) [ -n "$strong" ] || strong=$code ;;
      esac
      ok=false
      break
    done
    if $ok; then readable=$candidate; break; fi
  done

  if [ -z "$readable" ]; then
    echo "error: tiles published but NOT readable without credentials, the Valhalla service will not be able to route:$report" >&2
    case ${strong:-$weak} in
      400) echo "  HTTP 400: the storage rejects this URL form; use the virtual-hosted form https://$bucket.<endpoint host>/... in VALHALLA_TILE_URL" >&2 ;;
      401|403) echo "  HTTP $last: the tiles are private. Make the objects public (a public bucket ACL alone only allows listing): set VALHALLA_S3_ACL=public-read and, for tiles already uploaded,
    ${aws_cmd[*]} s3 cp $s3_uri $s3_uri --recursive --acl public-read --metadata-directive REPLACE --endpoint-url $endpoint" >&2 ;;
      404) echo "  HTTP 404: no tile at this URL; check that VALHALLA_TILE_URL points to $s3_uri and ends with /{tilePath}" >&2 ;;
      000) echo "  no HTTP answer: check the host of VALHALLA_TILE_URL (network, DNS, TLS)" >&2 ;;
    esac
    exit 1
  fi
  echo "= tiles readable without credentials at $readable"
  if [ -z "$tile_url" ]; then
    echo
    echo "Tiles published. Configure the Valhalla service in .env with:"
    echo "  VALHALLA_TILE_URL=$readable"
  fi
fi
echo "Then empty ./.valhalla/tiles and restart the service: docker compose --profile valhalla up -d --force-recreate valhalla"
