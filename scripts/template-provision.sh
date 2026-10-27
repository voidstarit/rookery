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
# kpartx or dmsetup. When properties.resize_disk is true it extends the root
# partition when the layout is safe (DOS with root first and a trailing
# extended/swap partition, or a single root partition), recreates the swap
# signature with its original UUID/label, and grows the root filesystem. Other
# layouts are left unchanged and only the filesystem is resized. It only
# modifies an existing user's home directory; adjust the user creation/chroot
# commands for your images.
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

partition_number() {
  local part=$1 number
  number=$(lsblk -no PARTN "$part" 2>/dev/null | head -n1 | tr -d '[:space:]')
  if [ -z "$number" ]; then
    number=$(printf '%s' "$part" | sed -n 's/.*p\([0-9][0-9]*\)$/\1/p')
  fi
  if [ -z "$number" ]; then
    number=$(printf '%s' "$part" | sed -n 's/.*[^0-9]\([0-9][0-9]*\)$/\1/p')
  fi
  printf '%s\n' "$number"
}

find_partition_path_by_number() {
  local wanted=$1 part
  for part in "${mapped_partitions[@]}"; do
    if [ "$(partition_number "$part")" = "$wanted" ]; then
      printf '%s\n' "$part"
      return 0
    fi
  done
  return 1
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
    root_partition_nr=0
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
  mapped_partitions=("${partitions[@]}")
  log "found ${#partitions[@]} partition(s) on $device"

  local part label partlabel
  for part in "${partitions[@]}"; do
    label=$("$blkid_cmd" -s LABEL -o value "$part" 2>/dev/null || true)
    partlabel=$("$blkid_cmd" -s PARTLABEL -o value "$part" 2>/dev/null || true)
    case "$(printf '%s' "$label" | tr '[:upper:]' '[:lower:]')" in
      root|rootfs|linux-root|linux_root|linux\ root)
        root_device=$part
        root_partition_nr=$(partition_number "$part")
        return 0
        ;;
    esac
    case "$(printf '%s' "$partlabel" | tr '[:upper:]' '[:lower:]')" in
      root|rootfs|linux-root|linux_root|linux\ root)
        root_device=$part
        root_partition_nr=$(partition_number "$part")
        return 0
        ;;
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
        root_partition_nr=$(partition_number "$part")
        return 0
      fi
      umount "$probe" 2>/dev/null || log "WARNING: could not unmount probe for $part"
    fi
  done
  rmdir "$probe" 2>/dev/null || true
  return 1
}

release_partition_mappings() {
  case "$mapping_mode" in
    kpartx)
      log "removing temporary partition mappings from $disk"
      "$kpartx_cmd" -d "$disk" >/dev/null 2>&1 ||
        log "WARNING: could not remove temporary partition mappings from $disk"
      ;;
    dmsetup)
      log "removing temporary partition mappings from $disk"
      local i
      for ((i=${#dm_maps[@]}-1; i>=0; i--)); do
        "$dmsetup_cmd" remove "${dm_maps[i]}" >/dev/null 2>&1 ||
          log "WARNING: could not remove temporary mapping ${dm_maps[i]}"
      done
      ;;
    partx)
      log "removing temporary partition mappings from $disk"
      "$partx_cmd" -d "$disk" >/dev/null 2>&1 ||
        log "WARNING: could not remove temporary partition mappings from $disk"
      ;;
  esac
  mapping_mode=""
  mapped_partitions=()
  dm_maps=()
}

# Extend the root partition when the disk has free space. This handles the
# common DOS layout with root first and a trailing extended partition containing
# swap: the swap partition is moved to the end of the disk (its contents are
# recreated with the same UUID/label), and root grows into the freed space.
# Other layouts are left alone and only the filesystem is resized.
extend_root_partition() {
  local partition_table_type
  [ "$root_partition_nr" -gt 0 ] || return 0
  [ -n "$partx_cmd" ] || return 0
  [ -n "$sfdisk_cmd" ] || { log "WARNING: sfdisk is not installed; cannot extend the root partition"; return 0; }
  [ -n "$blockdev_cmd" ] || return 0

  partition_table_type=$("$blkid_cmd" -p -s PTTYPE -o value "$disk" 2>/dev/null || true)
  if [ "$partition_table_type" != "dos" ]; then
    log "WARNING: resize_disk can only extend the root partition automatically for DOS partition tables (found: ${partition_table_type:-unknown})"
    return 0
  fi

  local -a table=()
  mapfile -t table < <("$partx_cmd" -s -g -o NR,START,SECTORS,TYPE,FLAGS "$disk" 2>/dev/null || true)
  if (( ${#table[@]} == 0 )); then
    log "WARNING: could not read the partition table; leaving the partition layout unchanged"
    return 0
  fi

  local nr start sectors type flags count=0
  local root_start="" root_sectors="" root_type="" root_flags=""
  local ext_nr="" ext_start="" ext_sectors="" ext_type=""
  local swap_nr="" swap_start="" swap_sectors="" swap_type=""
  local line
  for line in "${table[@]}"; do
    read -r nr start sectors type flags <<< "$line"
    [ -n "${nr:-}" ] || continue
    count=$((count + 1))
    if [ "$nr" = "$root_partition_nr" ]; then
      root_start=$start
      root_sectors=$sectors
      root_type=$type
      root_flags=$flags
      continue
    fi
    case "${type,,}" in
      0x5|0xf|5|f)
        ext_nr=$nr; ext_start=$start; ext_sectors=$sectors; ext_type=$type
        ;;
      0x82|82)
        swap_nr=$nr; swap_start=$start; swap_sectors=$sectors; swap_type=$type
        ;;
    esac
  done

  if [ -z "$root_start" ] || [ -z "$root_type" ]; then
    log "WARNING: root partition is not represented in the partition table; leaving it unchanged"
    return 0
  fi
  local disk_end
  disk_end=$("$blockdev_cmd" --getsz "$disk" 2>/dev/null || true)
  [ -n "$disk_end" ] || return 0

  local label_id table_file new_root_size new_ext_start new_ext_size new_swap_start
  label_id=$("$sfdisk_cmd" -d "$disk" 2>/dev/null | awk -F': ' '/^label-id:/ { print $2; exit }')

  if (( count == 1 )); then
    new_root_size=$((disk_end - root_start))
    if (( new_root_size <= root_sectors + 2048 )); then
      log "resize_disk: no meaningful free space after the root partition"
      return 0
    fi
    table_file=$(mktemp)
    {
      printf 'label: dos\n'
      [ -n "$label_id" ] && printf 'label-id: %s\n' "$label_id"
      printf 'unit: sectors\nsector-size: 512\n\n'
      printf 'start=%s, size=%s, type=%s' "$root_start" "$new_root_size" "${root_type#0x}"
      [[ "$root_flags" =~ (0x80|boot) ]] && printf ', bootable'
      printf '\n'
    } > "$table_file"
    log "resize_disk: extending root partition $root_partition_nr by $((new_root_size - root_sectors)) sectors"
  elif (( count == 3 )) && [ "$root_partition_nr" = 1 ] && [ -n "$ext_nr" ] && [ -n "$swap_nr" ] && [ -n "$swap_sectors" ]; then
    if (( ext_start < root_start + root_sectors || swap_start < ext_start || swap_start + swap_sectors > ext_start + ext_sectors )); then
      log "WARNING: root/extended/swap layout is not contiguous; leaving the partition layout unchanged"
      return 0
    fi
    new_swap_start=$(( (disk_end - swap_sectors) / 2048 * 2048 ))
    new_ext_start=$((new_swap_start - 2048))
    new_root_size=$((new_ext_start - root_start))
    new_ext_size=$((disk_end - new_ext_start))
    if (( new_ext_start <= root_start + root_sectors + 2048 )); then
      log "resize_disk: no meaningful free space to move the swap partition and grow root"
      return 0
    fi
    local swap_path swap_uuid swap_label
    swap_path=$(find_partition_path_by_number "$swap_nr" || true)
    if [ -z "$swap_path" ]; then
      log "WARNING: could not find swap partition $swap_nr; leaving the partition layout unchanged"
      return 0
    fi
    if [ -z "$mkswap_cmd" ]; then
      log "WARNING: mkswap is not installed; cannot move the swap partition"
      return 0
    fi
    swap_uuid=$("$blkid_cmd" -s UUID -o value "$swap_path" 2>/dev/null || true)
    swap_label=$("$blkid_cmd" -s LABEL -o value "$swap_path" 2>/dev/null || true)
    table_file=$(mktemp)
    {
      printf 'label: dos\n'
      [ -n "$label_id" ] && printf 'label-id: %s\n' "$label_id"
      printf 'unit: sectors\nsector-size: 512\n\n'
      printf 'start=%s, size=%s, type=%s' "$root_start" "$new_root_size" "${root_type#0x}"
      [[ "$root_flags" =~ (0x80|boot) ]] && printf ', bootable'
      printf '\n'
      printf 'start=%s, size=%s, type=%s\n' "$new_ext_start" "$new_ext_size" "${ext_type#0x}"
      printf 'start=%s, size=%s, type=%s\n' "$new_swap_start" "$swap_sectors" "${swap_type#0x}"
    } > "$table_file"
    log "resize_disk: moving swap partition $swap_nr to sector $new_swap_start and extending root partition $root_partition_nr"
  else
    log "WARNING: resize_disk does not recognize this partition layout; leaving the partition table unchanged"
    return 0
  fi

  if ! "$sfdisk_cmd" --force --no-reread "$disk" < "$table_file"; then
    rm -f "$table_file"
    die "could not write the resized partition table to $disk"
  fi
  rm -f "$table_file"

  release_partition_mappings
  "$partx_cmd" -u "$disk" >/dev/null 2>&1 || true
  "$blockdev_cmd" --rereadpt "$disk" >/dev/null 2>&1 || true

  root_device=""
  root_partition_nr=0
  detect_root_device "$disk" ||
    die "could not find the root filesystem after extending the partition table"
  root_partition_extended=1

  if [ -n "$swap_nr" ]; then
    swap_path=$(find_partition_path_by_number "$swap_nr" || true)
    if [ -z "$swap_path" ]; then
      die "could not find the moved swap partition after extending the partition table"
    fi
    local -a mkswap_args=()
    [ -n "$swap_uuid" ] && mkswap_args+=("-U" "$swap_uuid")
    [ -n "$swap_label" ] && mkswap_args+=("-L" "$swap_label")
    "$mkswap_cmd" "${mkswap_args[@]}" "$swap_path" >/dev/null ||
      die "could not recreate the moved swap partition"
    log "resize_disk: recreated swap partition $swap_nr"
  fi
}

resize_root_filesystem() {
  local fs_type disk_bytes root_bytes
  fs_type=$("$blkid_cmd" -s TYPE -o value "$root_device" 2>/dev/null || true)
  [ -n "$fs_type" ] || die "could not determine the root filesystem type on $root_device"

  if [ -n "$blockdev_cmd" ]; then
    disk_bytes=$("$blockdev_cmd" --getsize64 "$disk" 2>/dev/null || true)
    root_bytes=$("$blockdev_cmd" --getsize64 "$root_device" 2>/dev/null || true)
    if [ -n "$disk_bytes" ] && [ -n "$root_bytes" ] && (( root_bytes < disk_bytes )) && (( root_partition_extended == 0 )); then
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
sfdisk_cmd=$(find_command sfdisk || true)
mkswap_cmd=$(find_command mkswap || true)

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
root_partition_nr=0
root_partition_extended=0
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
  if [ -n "$mapping_mode" ]; then
    release_partition_mappings
  fi
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
if [ "$resize_disk" = true ]; then
  extend_root_partition
fi
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
