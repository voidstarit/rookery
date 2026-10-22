#!/usr/bin/env bash
#
# Create or delete a Rookery disk as a thin-LVM snapshot.
#
# API mode: when ROOKERY_DISK_JSON is set, this script is run by the Rookery
# API as a storage backend preparation_script. The API supplies the operation,
# disk name, VG/thin pool, requested size, and template.
#
# CLI mode:
#   lvm-thin-snapshot-disk.sh create --vg VG --thin-pool POOL \
#     --template TEMPLATE_LV --name NEW_LV [--size-gb N]
#   lvm-thin-snapshot-disk.sh delete --vg VG --thin-pool POOL --name LV
#
# The template/source must be a thin LV in the selected thin pool. Clones are
# created with `lvcreate --snapshot` and therefore share the template's data
# until written. If --size-gb exceeds the source size, the new LV is extended
# with lvextend; the filesystem inside the snapshot is not resized.
set -euo pipefail

# Daemons commonly run without /sbin in PATH. Prefer commands already on PATH
# (this also makes the script testable with mocks), then fall back to sbin.
if ! command -v lvs >/dev/null 2>&1 || ! command -v lvcreate >/dev/null 2>&1 || ! command -v lvremove >/dev/null 2>&1 || ! command -v lvchange >/dev/null 2>&1 || ! command -v lvextend >/dev/null 2>&1; then
  PATH="/usr/sbin:/sbin:${PATH}"
  export PATH
fi

usage() {
  cat <<'USAGE'
Usage:
  lvm-thin-snapshot-disk.sh create --vg VG --thin-pool POOL \
    --template TEMPLATE_LV --name NEW_LV [--size-gb N]
  lvm-thin-snapshot-disk.sh delete --vg VG --thin-pool POOL --name LV

The script also runs in API preparation-script mode when ROOKERY_DISK_JSON is
set; in that mode it takes no command-line arguments.

Requirements: lvm2, root (or LVM permissions), and jq for API mode.
The source/template must be a thin LV in the requested thin pool.
USAGE
}

die() {
  printf 'lvm-thin-snapshot-disk: %s\n' "$*" >&2
  exit 1
}

need() {
  local cmd
  for cmd in "$@"; do
    command -v "$cmd" >/dev/null 2>&1 || die "missing required command: $cmd"
  done
}

valid_lvm_name() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9+_.-]*$ ]]
}

# Print pool_lv|vg_name|segtype for one LV. Empty output means the LV does not
# exist (or lvs failed).
lv_info() {
  local lv=$1
  lvs --noheadings --separator '|' --options pool_lv,vg_name,segtype "$lv" 2>/dev/null |
    head -n 1 | sed 's/ //g'
}

require_vg_access() {
  # Distinguish "LV does not exist" from "we cannot read LVM at all"; otherwise
  # a permissions problem could make delete look successfully idempotent.
  if ! lvs --noheadings --options lv_name "$vg" >/dev/null 2>&1; then
    die "cannot read volume group $vg (missing VG or insufficient LVM permissions)"
  fi
}

require_thin_lv() {
  local lv=$1 expected_vg=$2 expected_pool=$3 description=$4
  local info pool lv_vg segtype
  if ! info=$(lv_info "$lv") || [[ -z "$info" ]]; then
    die "$description LV $lv does not exist"
  fi
  IFS='|' read -r pool lv_vg segtype <<<"$info"
  pool=${pool#[}
  pool=${pool%]}
  [[ "$lv_vg" == "$expected_vg" ]] || die "$description LV $lv is in volume group $lv_vg, not $expected_vg"
  [[ "$pool" == "$expected_pool" ]] || die "$description LV $lv is not in thin pool $expected_pool"
  [[ "$segtype" == thin* ]] || die "$description LV $lv is not a thin LV (segment type: ${segtype:-unknown})"
}

source_size_gb() {
  local lv=$1 size
  size=$(lvs --noheadings --nosuffix --units g --options lv_size "$lv" 2>/dev/null |
    awk 'NR == 1 { print $1 }')
  [[ -n "$size" ]] || die "could not determine the size of $lv"
  size=${size#<}
  printf '%s\n' "$size"
}

validate_common() {
  [[ -n "$vg" ]] || die "volume group is required"
  [[ -n "$thin_pool" ]] || die "thin pool is required"
  [[ -n "$name" ]] || die "disk/LV name is required"
  valid_lvm_name "$vg" || die "invalid volume group name: $vg"
  valid_lvm_name "$thin_pool" || die "invalid thin pool name: $thin_pool"
  valid_lvm_name "$name" || die "invalid disk/LV name: $name"
  if [[ -n "$template" ]]; then
    valid_lvm_name "$template" || die "invalid template/source LV name: $template"
  fi
  case "${size_gb:-}" in
    ""|0) size_gb="" ;;
    *[!0-9]*) die "size_gb must be a positive integer" ;;
    *)
      size_gb=$((10#$size_gb))
      (( size_gb > 0 )) || die "size_gb must be a positive integer"
      ;;
  esac
}

create_disk() {
  need lvs lvcreate lvremove lvchange
  [[ -n "$template" ]] || die "template/source LV is required for create"
  require_vg_access
  require_thin_lv "$vg/$template" "$vg" "$thin_pool" "template/source"

  local existing
  if ! existing=$(lv_info "$vg/$name"); then
    existing=""
  fi
  if [[ -n "$existing" ]]; then
    die "target LV $vg/$name already exists"
  fi

  local created=0
  cleanup() {
    local status=$?
    if (( status != 0 && created == 1 )); then
      printf 'lvm-thin-snapshot-disk: create failed; removing partial LV %s/%s\n' "$vg" "$name" >&2
      lvremove --force "$vg/$name" >/dev/null 2>&1 || true
    fi
  }
  trap cleanup EXIT

  # For a thin origin, lvcreate creates a thin snapshot. Do not pass -L here:
  # LVM ignores -L for thin snapshots, and the virtual size is adjusted with
  # lvextend below when a larger disk was requested.
  lvcreate --yes --snapshot --setactivationskip n --name "$name" "$vg/$template" >&2
  created=1
  # Thin snapshots are created with the activation-skip flag by default.
  # Activate explicitly so the device is available to QEMU.
  lvchange -ay "$vg/$name" >&2
  require_thin_lv "$vg/$name" "$vg" "$thin_pool" "created disk"

  local src_size
  src_size=$(source_size_gb "$vg/$template")
  if [[ -n "$size_gb" ]]; then
    if awk -v requested="$size_gb" -v source="$src_size" 'BEGIN { exit !(requested + 0 < source + 0) }'; then
      die "size_gb $size_gb is smaller than the template size ${src_size}G"
    fi
    if awk -v requested="$size_gb" -v source="$src_size" 'BEGIN { exit !(requested + 0 > source + 0) }'; then
      need lvextend
      lvextend -L "${size_gb}G" "$vg/$name" >&2
    fi
  fi

  if command -v udevadm >/dev/null 2>&1; then
    udevadm settle --timeout=10 >/dev/null 2>&1 || true
  fi

  trap - EXIT
  printf '/dev/%s/%s\n' "$vg" "$name"
}

delete_disk() {
  need lvs lvremove
  require_vg_access
  local existing
  if ! existing=$(lv_info "$vg/$name"); then
    existing=""
  fi
  if [[ -z "$existing" ]]; then
    # The API may retry a delete or call it after a partially failed create.
    printf 'LV %s/%s does not exist; nothing to delete\n' "$vg" "$name" >&2
    return 0
  fi
  require_thin_lv "$vg/$name" "$vg" "$thin_pool" "disk"
  if [[ -n "$template" ]]; then
    # The API calls delete with the template when a create attempt failed. Do
    # not remove a pre-existing unrelated LV that happened to use the name.
    local origin
    origin=$(lvs --noheadings --separator '|' --options origin "$vg/$name" 2>/dev/null |
      head -n 1 | sed 's/ //g')
    origin=${origin#[}
    origin=${origin%]}
    origin=${origin#"$vg"/}
    if [[ "$origin" != "$template" ]]; then
      die "refusing to delete $vg/$name: it is not a snapshot of template $template"
    fi
  fi
  lvremove --force "$vg/$name" >&2
  if command -v udevadm >/dev/null 2>&1; then
    udevadm settle --timeout=10 >/dev/null 2>&1 || true
  fi
}

operation=""
vg=""
thin_pool=""
name=""
template=""
size_gb=""

if [[ -n "${ROOKERY_DISK_JSON:-}" ]]; then
  need jq
  (( $# == 0 )) || die "do not pass command-line arguments when ROOKERY_DISK_JSON is set"
  payload=$ROOKERY_DISK_JSON
  if ! jq -e 'type == "object"' >/dev/null 2>&1 <<<"$payload"; then
    die "ROOKERY_DISK_JSON is not a JSON object"
  fi
  operation=$(jq -r '.operation // empty' <<<"$payload") || die "could not read operation from ROOKERY_DISK_JSON"
  vg=$(jq -r '.disk.vg // empty' <<<"$payload") || die "could not read disk.vg from ROOKERY_DISK_JSON"
  thin_pool=$(jq -r '.disk.thin_pool // empty' <<<"$payload") || die "could not read disk.thin_pool from ROOKERY_DISK_JSON"
  name=$(jq -r '.disk.name // empty' <<<"$payload") || die "could not read disk.name from ROOKERY_DISK_JSON"
  template=$(jq -r '.template.name // empty' <<<"$payload") || die "could not read template.name from ROOKERY_DISK_JSON"
  size_gb=$(jq -r '.disk.size_gb // 0' <<<"$payload") || die "could not read disk.size_gb from ROOKERY_DISK_JSON"
else
  operation=${1:-}
  [[ -n "$operation" ]] || { usage >&2; exit 2; }
  shift
  case "$operation" in
    create|delete) ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown operation: $operation" ;;
  esac
  while (( $# > 0 )); do
    case "$1" in
      --vg)
        (( $# >= 2 )) || die "--vg requires a value"
        vg=$2; shift 2 ;;
      --thin-pool)
        (( $# >= 2 )) || die "--thin-pool requires a value"
        thin_pool=$2; shift 2 ;;
      --name)
        (( $# >= 2 )) || die "--name requires a value"
        name=$2; shift 2 ;;
      --template|--source|--snapshot)
        (( $# >= 2 )) || die "$1 requires a value"
        template=$2; shift 2 ;;
      --size-gb)
        (( $# >= 2 )) || die "--size-gb requires a value"
        size_gb=$2; shift 2 ;;
      -h|--help)
        usage; exit 0 ;;
      *)
        die "unknown argument: $1" ;;
    esac
  done
fi

case "$operation" in
  create|delete) ;;
  *) die "operation must be create or delete, got: ${operation:-<empty>}" ;;
esac

validate_common

case "$operation" in
  create) create_disk ;;
  delete) delete_disk ;;
esac
