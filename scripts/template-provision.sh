#!/usr/bin/env bash
#
# Example Rookery template provisioning script.
#
# Rookery runs this after the backend preparation script has created and
# activated a clone. The API supplies one JSON object in
# ROOKERY_PROVISION_JSON:
#
# {
#   "operation": "provision",
#   "request_id": "...",
#   "disk": { "name": "...", "device": "/dev/vg/clone", ... },
#   "template": { ... },
#   "backend": { ... },
#   "properties": {
#     "hostname": "web-01",
#     "timezone": "UTC",
#     "username": "deploy",
#     "ssh_authorized_keys": ["ssh-ed25519 AAAA... user@example"],
#     "resize_disk": true
#   }
# }
#
# The script detects whether the device is a filesystem or has a partition
# table, then finds the root partition by the root/rootfs/Linux root label or
# by probing partitions for /etc/os-release or /etc/passwd. LVM logical volumes
# are often created with partition scanning disabled; in that case the script
# reads the table with partx and exposes the partitions temporarily through
# kpartx or dmsetup. When properties.resize_disk is true it grows the root
# filesystem to the size of its root block device. It does not rewrite the
# partition table. It only modifies an existing user's home directory; adjust
# the user creation/chroot commands for your images.
#
# The script logs each stage and always attempts to unmount the clone on exit,
# including on failure or interruption. Rookery captures the combined output.
set -euo pipefail

log() {
  printf 'template-provision: [%s] %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*" >&2
}

die() {
  log "ERROR: $*"
  exit 1
}

find_command() {
  local name=$1
  local cmd
  cmd=$(command -v "$name" 2>/dev/null || true)
  if [ -z "$cmd" ]; then
    local dir
    for dir in /usr/sbin /sbin; do
      if [ -x "$dir/$name" ]; then
        cmd="$dir/$name"
        break
      fi
    done
  fi
  [ -n "$cmd" ] || return 1
  printf '%s\n' "$cmd"
}

list_partitions() {
  lsblk -rpno NAME,TYPE "$1" 2>/dev/null | awk '$2 == "part" { print $1 }'
}

# LVM exposes logical volumes as device-mapper devices, and those devices
# commonly have partition scanning disabled. partx can read the table but
# cannot add partitions to a device-mapper device; kpartx or explicit dmsetup
# linear mappings do work. Prefer kpartx when present, then fall back to
# dmsetup, which is part of LVM.
map_partitions() {
  local device=$1
  local -a table=()
  [ -n "$partx_cmd" ] || return 1
  mapfile -t table < <("$partx_cmd" -s -g -o NR,START,SECTORS "$device" 2>/dev/null || true)
  if (( ${#table[@]} == 0 )); then
    return 1
  fi
  log "found ${#table[@]} partition table entr(ies) on $device"

  local nr start sectors name path attempt
  if [ -n "$kpartx_cmd" ]; then
    log "exposing partitions with kpartx"
    mapped_partitions=()
    mapping_mode=kpartx
    if "$kpartx_cmd" -a "$device" >/dev/null 2>&1; then
      mapfile -t mapped_partitions < <(
        "$kpartx_cmd" -l "$device" 2>/dev/null | awk 'NF { print "/dev/mapper/" $1 }'
      )
      if (( ${#mapped_partitions[@]} > 0 )); then
        for path in "${mapped_partitions[@]}"; do
          for attempt in {1..25}; do
            [ -e "$path" ] && break
            sleep 0.2
          done
          [ -e "$path" ] || die "kpartx did not create $path"
        done
        return 0
      fi
      log "WARNING: kpartx reported success but returned no partition mappings"
    else
      log "WARNING: kpartx could not expose partitions; trying dmsetup"
    fi
    "$kpartx_cmd" -d "$device" >/dev/null 2>&1 || true
    mapping_mode=""
  fi

  if [ -n "$dmsetup_cmd" ]; then
    local major_minor
    major_minor=$(lsblk -no MAJ:MIN "$device" 2>/dev/null | head -n1 | tr -d '[:space:]')
    if [ -z "$major_minor" ]; then
      die "could not determine the device-mapper major/minor for $device"
    fi
    local base="rookeryprov-${request_id:-unknown}-$$-${RANDOM}"
    base=${base//[^A-Za-z0-9_.+-]/-}
    base=${base:0:80}
    mapped_partitions=()
    mapping_mode=dmsetup
    local line
    for line in "${table[@]}"; do
      read -r nr start sectors <<< "$line"
      [ -n "${nr:-}" ] || continue
      name="${base}-p${nr}"
      log "mapping partition $nr (start=$start, sectors=$sectors) as /dev/mapper/$name"
      "$dmsetup_cmd" create "$name" --table "0 $sectors linear $major_minor $start" >/dev/null ||
        die "could not create a device-mapper mapping for partition $nr on $device"
      dm_maps+=("$name")
      "$dmsetup_cmd" mknodes "$name" >/dev/null 2>&1 || true
      path="/dev/mapper/$name"
      for attempt in {1..25}; do
        [ -e "$path" ] && break
        sleep 0.2
      done
      [ -e "$path" ] || die "device-mapper did not create $path"
      mapped_partitions+=("$path")
    done
    if (( ${#mapped_partitions[@]} > 0 )); then
      return 0
    fi
    mapping_mode=""
  fi

  # Last resort for non-device-mapper disks whose kernel partition nodes have
  # not appeared. This does not work on device-mapper devices.
  if [ -n "$partx_cmd" ]; then
    log "exposing partitions with partx"
    mapped_partitions=()
    mapping_mode=partx
    if "$partx_cmd" -a "$device" >/dev/null 2>&1; then
      mapfile -t mapped_partitions < <(list_partitions "$device")
      if (( ${#mapped_partitions[@]} > 0 )); then
        return 0
      fi
    fi
    "$partx_cmd" -d "$device" >/dev/null 2>&1 || true
    mapping_mode=""
  fi
  return 1
}

detect_root_device() {
  local device=$1
  local fs_type
  fs_type=$("$blkid_cmd" -s TYPE -o value "$device" 2>/dev/null || true)
  if [ -n "$fs_type" ]; then
    root_device=$device
    return 0
  fi

  local -a partitions=()
  mapfile -t partitions < <(list_partitions "$device")
  if (( ${#partitions[@]} == 0 )); then
    log "no kernel partition nodes visible on $device"
    mapped_partitions=()
    if map_partitions "$device"; then
      partitions=("${mapped_partitions[@]}")
    fi
  fi

  if (( ${#partitions[@]} == 0 )); then
    return 1
  fi
  log "found ${#partitions[@]} partition(s) on $device"

  local part label partlabel
  for part in "${partitions[@]}"; do
    label=$("$blkid_cmd" -s LABEL -o value "$part" 2>/dev/null || true)
    partlabel=$("$blkid_cmd" -s PARTLABEL -o value "$part" 2>/dev/null || true)
    case "$(printf '%s' "$label" | tr '[:upper:]' '[:lower:]')" in
      root|rootfs|linux-root|linux_root|linux\ root) root_device=$part; return 0 ;;
    esac
    case "$(printf '%s' "$partlabel" | tr '[:upper:]' '[:lower:]')" in
      root|rootfs|linux-root|linux_root|linux\ root) root_device=$part; return 0 ;;
    esac
  done

  local probe
  probe=$(mktemp -d)
  for part in "${partitions[@]}"; do
    log "probing partition $part for a root filesystem"
    if mount -o ro "$part" "$probe" 2>/dev/null; then
      if [ -f "$probe/etc/os-release" ] || [ -f "$probe/etc/passwd" ]; then
        umount "$probe" 2>/dev/null || log "WARNING: could not unmount probe for $part"
        rmdir "$probe" 2>/dev/null || true
        root_device=$part
        return 0
      fi
      umount "$probe" 2>/dev/null || log "WARNING: could not unmount probe for $part"
    fi
  done
  rmdir "$probe" 2>/dev/null || true
  return 1
}

resize_root_filesystem() {
  local fs_type disk_bytes root_bytes
  fs_type=$("$blkid_cmd" -s TYPE -o value "$root_device" 2>/dev/null || true)
  [ -n "$fs_type" ] || die "could not determine the root filesystem type on $root_device"

  if [ -n "$blockdev_cmd" ]; then
    disk_bytes=$("$blockdev_cmd" --getsize64 "$disk" 2>/dev/null || true)
    root_bytes=$("$blockdev_cmd" --getsize64 "$root_device" 2>/dev/null || true)
    if [ -n "$disk_bytes" ] && [ -n "$root_bytes" ] && (( root_bytes < disk_bytes )); then
      log "WARNING: root block device $root_device is smaller than $disk; resize_disk does not rewrite the partition table"
      log "WARNING: the filesystem can only grow within its current partition"
    fi
  fi

  log "resize_disk: growing $fs_type filesystem on $root_device"
  case "$fs_type" in
    ext2|ext3|ext4)
      [ -n "$resize2fs_cmd" ] || die "resize2fs is required for resize_disk with $fs_type"
      "$resize2fs_cmd" "$root_device"
      ;;
    xfs)
      [ -n "$xfs_growfs_cmd" ] || die "xfs_growfs is required for resize_disk with xfs"
      "$xfs_growfs_cmd" "$mountpoint"
      ;;
    btrfs)
      [ -n "$btrfs_cmd" ] || die "btrfs is required for resize_disk with btrfs"
      "$btrfs_cmd" filesystem resize max "$mountpoint"
      ;;
    *)
      die "resize_disk is not supported for filesystem type $fs_type"
      ;;
  esac
  log "resize_disk: complete"
  if command -v df >/dev/null 2>&1; then
    df -h "$mountpoint" 2>/dev/null | tail -n 1 | while IFS= read -r line; do
      log "resize_disk: $line"
    done
  fi
}

json=${ROOKERY_PROVISION_JSON:?ROOKERY_PROVISION_JSON is required}
command -v jq >/dev/null 2>&1 || die "jq is required"
command -v mount >/dev/null 2>&1 || die "mount is required"
command -v umount >/dev/null 2>&1 || die "umount is required"
command -v lsblk >/dev/null 2>&1 || die "lsblk is required"
blkid_cmd=$(find_command blkid) || die "blkid is required"
partx_cmd=$(find_command partx || true)
kpartx_cmd=$(find_command kpartx || true)
dmsetup_cmd=$(find_command dmsetup || true)
resize2fs_cmd=$(find_command resize2fs || true)
xfs_growfs_cmd=$(find_command xfs_growfs || true)
btrfs_cmd=$(find_command btrfs || true)
blockdev_cmd=$(find_command blockdev || true)

printf '%s' "$json" | jq -e 'type == "object"' >/dev/null 2>&1 ||
  die "ROOKERY_PROVISION_JSON is not a JSON object"
printf '%s' "$json" | jq -e '(.properties // {}) | type == "object"' >/dev/null 2>&1 ||
  die "properties must be a JSON object"

# Log the complete payload received from Rookery. This includes custom
# properties, so avoid passing secrets unless they are safe to appear in logs.
log "received ROOKERY_PROVISION_JSON=$(printf '%s' "$json" | jq -cS .)"

request_id=$(printf '%s' "$json" | jq -r '.request_id // empty')
property_count=$(printf '%s' "$json" | jq -r '.properties // {} | length')
printf '%s' "$json" | jq -e '(.properties.resize_disk // false) | type == "boolean"' >/dev/null 2>&1 ||
  die "properties.resize_disk must be a boolean"
resize_disk=$(printf '%s' "$json" | jq -r 'if .properties.resize_disk == null then false else .properties.resize_disk end')
case "$resize_disk" in
  true|false) ;;
  *) die "properties.resize_disk must be a boolean" ;;
esac
log "starting provisioning request=${request_id:-unknown} property_count=$property_count resize_disk=$resize_disk"
if [ "$property_count" -eq 0 ] || { [ "$property_count" -eq 1 ] && [ "$resize_disk" = false ]; }; then
  log "no provisioning changes requested; nothing to do"
  exit 0
fi

disk=$(printf '%s' "$json" | jq -r '.disk.device // empty') ||
  die "could not read disk.device from ROOKERY_PROVISION_JSON"
hostname=$(printf '%s' "$json" | jq -r '.properties.hostname // empty') ||
  die "could not read properties.hostname"
timezone=$(printf '%s' "$json" | jq -r '.properties.timezone // empty') ||
  die "could not read properties.timezone"
username=$(printf '%s' "$json" | jq -r '.properties.username // "root"') ||
  die "could not read properties.username"

[ -n "$disk" ] || die "provisioning payload has no disk.device"
[[ "$disk" == /dev/* ]] || die "disk.device must be an absolute /dev path: $disk"
[ -b "$disk" ] || die "disk device does not exist or is not a block device: $disk"
[[ "$username" =~ ^[A-Za-z0-9][A-Za-z0-9_-]*$ ]] || die "invalid username: $username"

log "provisioning $disk (hostname=${hostname:-unchanged}, timezone=${timezone:-unchanged}, user=$username)"

mountpoint=""
mounted=0
mapping_mode=""
mapped_partitions=()
dm_maps=()
cleanup() {
  local status=$?
  if [ -n "$mountpoint" ]; then
    if (( mounted == 1 )); then
      log "unmounting $mountpoint"
      sync || log "WARNING: sync before unmount failed"
      if ! umount "$mountpoint" 2>&1; then
        log "WARNING: normal unmount failed; retrying lazy unmount"
        umount -l "$mountpoint" 2>&1 || log "ERROR: lazy unmount failed for $mountpoint"
      fi
      mounted=0
    fi
    rmdir "$mountpoint" 2>/dev/null || true
  fi
  case "$mapping_mode" in
    kpartx)
      log "removing temporary partition mappings from $disk"
      "$kpartx_cmd" -d "$disk" >/dev/null 2>&1 ||
        log "WARNING: could not remove temporary partition mappings from $disk"
      mapping_mode=""
      ;;
    dmsetup)
      log "removing temporary partition mappings from $disk"
      local i
      for ((i=${#dm_maps[@]}-1; i>=0; i--)); do
        "$dmsetup_cmd" remove "${dm_maps[i]}" >/dev/null 2>&1 ||
          log "WARNING: could not remove temporary mapping ${dm_maps[i]}"
      done
      dm_maps=()
      mapping_mode=""
      ;;
    partx)
      log "removing temporary partition mappings from $disk"
      "$partx_cmd" -d "$disk" >/dev/null 2>&1 ||
        log "WARNING: could not remove temporary partition mappings from $disk"
      mapping_mode=""
      ;;
  esac
  if (( status != 0 )); then
    log "provisioning failed with exit status $status"
  else
    log "provisioning completed successfully"
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'log "interrupted"; exit 130' INT TERM

log "creating temporary mount point"
mountpoint=$(mktemp -d) || die "could not create temporary mount point"
root_device=""
detect_root_device "$disk" ||
  die "could not identify a root filesystem on $disk; inspect the partition layout with lsblk, partx, and blkid"
log "mounting root filesystem $root_device at $mountpoint"
mount "$root_device" "$mountpoint" || die "could not mount root filesystem $root_device"
mounted=1

if [ "$resize_disk" = true ]; then
  resize_root_filesystem
fi

if [ -n "$hostname" ]; then
  log "setting hostname to $hostname"
  printf '%s\n' "$hostname" > "$mountpoint/etc/hostname"
  if grep -q '^127\.0\.1\.1[[:space:]]' "$mountpoint/etc/hosts" 2>/dev/null; then
    sed -i "s/^127\\.0\\.1\\.1[[:space:]].*/127.0.1.1 $hostname/" "$mountpoint/etc/hosts"
  else
    printf '127.0.1.1 %s\n' "$hostname" >> "$mountpoint/etc/hosts"
  fi
fi

if [ -n "$timezone" ]; then
  if [ -f "$mountpoint/usr/share/zoneinfo/$timezone" ]; then
    log "setting timezone to $timezone"
    ln -sfn "/usr/share/zoneinfo/$timezone" "$mountpoint/etc/localtime"
    printf '%s\n' "$timezone" > "$mountpoint/etc/timezone"
  else
    log "WARNING: timezone $timezone is not present in the image; leaving it unchanged"
  fi
fi

case "$username" in
  root)
    home="$mountpoint/root"
    ;;
  *)
    home="$mountpoint/home/$username"
    if ! grep -q "^${username}:" "$mountpoint/etc/passwd"; then
      log "creating user $username"
      chroot "$mountpoint" /usr/sbin/useradd -m -s /bin/bash "$username" ||
        die "could not create user $username"
    fi
    ;;
esac

key_count=$(printf '%s' "$json" | jq -r '.properties.ssh_authorized_keys // [] | length')
log "processing $key_count SSH authorized key(s) for $username"
if [ "$key_count" -gt 0 ]; then
  [ -d "$home" ] || die "home directory for $username does not exist: $home"
  mkdir -p "$home/.ssh"
  chmod 700 "$home/.ssh"
  while IFS= read -r key; do
    [ -n "$key" ] && printf '%s\n' "$key" >> "$home/.ssh/authorized_keys"
  done < <(printf '%s' "$json" | jq -r '.properties.ssh_authorized_keys[]?')
  chmod 600 "$home/.ssh/authorized_keys"
  if [ "$username" = root ]; then
    chown -R 0:0 "$home/.ssh"
  else
    chroot "$mountpoint" chown -R "$username:$username" "/home/$username/.ssh" ||
      die "could not set ownership on /home/$username/.ssh"
  fi
fi

log "all provisioning stages complete; syncing filesystem"
sync
