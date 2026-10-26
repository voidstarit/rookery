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
#     "ssh_authorized_keys": ["ssh-ed25519 AAAA... user@example"]
#   }
# }
#
# This example assumes a simple, single-filesystem image. For images with a
# partition table, use guestfish/virt-customize or mount the root partition
# explicitly. It only modifies an existing user's home directory; adjust the
# user creation/chroot commands for your images.
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

json=${ROOKERY_PROVISION_JSON:?ROOKERY_PROVISION_JSON is required}
command -v jq >/dev/null 2>&1 || die "jq is required"
command -v mount >/dev/null 2>&1 || die "mount is required"
command -v umount >/dev/null 2>&1 || die "umount is required"

printf '%s' "$json" | jq -e 'type == "object"' >/dev/null 2>&1 ||
  die "ROOKERY_PROVISION_JSON is not a JSON object"
printf '%s' "$json" | jq -e '(.properties // {}) | type == "object"' >/dev/null 2>&1 ||
  die "properties must be a JSON object"

# Log the complete payload received from Rookery. This includes custom
# properties, so avoid passing secrets unless they are safe to appear in logs.
log "received ROOKERY_PROVISION_JSON=$(printf '%s' "$json" | jq -cS .)"

request_id=$(printf '%s' "$json" | jq -r '.request_id // empty')
property_count=$(printf '%s' "$json" | jq -r '.properties // {} | length')
log "starting provisioning request=${request_id:-unknown} property_count=$property_count"
if [ "$property_count" -eq 0 ]; then
  log "no provisioning properties supplied; nothing to change"
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
log "mounting $disk at $mountpoint"
mount "$disk" "$mountpoint" || die "could not mount $disk"
mounted=1

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
