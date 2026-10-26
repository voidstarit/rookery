#!/bin/sh
# Example Rookery template provisioning script.
#
# Rookery runs this as the API user after the backend preparation script has
# created and activated the clone. It receives a JSON object in
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
#     "ssh_authorized_keys": ["ssh-ed25519 AAAA... user@example"]
#   }
# }
#
# This example assumes a simple, single-filesystem image. For images with a
# partition table, use guestfish/virt-customize or mount the root partition
# explicitly. It only modifies an existing user's home directory; adjust the
# user creation/chroot commands for your images.

set -eu

json=${ROOKERY_PROVISION_JSON:?ROOKERY_PROVISION_JSON is required}
command -v jq >/dev/null 2>&1 || {
  echo "jq is required" >&2
  exit 1
}

disk=$(printf '%s' "$json" | jq -r '.disk.device // empty')
hostname=$(printf '%s' "$json" | jq -r '.properties.hostname // empty')
timezone=$(printf '%s' "$json" | jq -r '.properties.timezone // empty')
username=$(printf '%s' "$json" | jq -r '.properties.username // "root"')

[ -n "$disk" ] || {
  echo "provisioning payload has no disk.device" >&2
  exit 1
}

mountpoint=$(mktemp -d)
cleanup() {
  umount "$mountpoint" 2>/dev/null || true
  rmdir "$mountpoint" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

mount "$disk" "$mountpoint"

if [ -n "$hostname" ]; then
  printf '%s\n' "$hostname" > "$mountpoint/etc/hostname"
  printf '127.0.1.1 %s\n' "$hostname" >> "$mountpoint/etc/hosts"
fi

if [ -n "$timezone" ] && [ -f "$mountpoint/usr/share/zoneinfo/$timezone" ]; then
  ln -sfn "/usr/share/zoneinfo/$timezone" "$mountpoint/etc/localtime"
  printf '%s\n' "$timezone" > "$mountpoint/etc/timezone"
fi

case "$username" in
  root) home="$mountpoint/root" ;;
  *[!A-Za-z0-9_-]*)
    echo "invalid username: $username" >&2
    exit 1
    ;;
  *) home="$mountpoint/home/$username" ;;
esac

if [ "$username" != root ] && ! grep -q "^${username}:" "$mountpoint/etc/passwd"; then
  chroot "$mountpoint" /usr/sbin/useradd -m -s /bin/bash "$username"
fi

if [ -d "$home" ]; then
  mkdir -p "$home/.ssh"
  chmod 700 "$home/.ssh"
  printf '%s' "$json" | jq -r '.properties.ssh_authorized_keys[]?' |
    while IFS= read -r key; do
      [ -n "$key" ] && printf '%s\n' "$key" >> "$home/.ssh/authorized_keys"
    done
  if [ -f "$home/.ssh/authorized_keys" ]; then
    chmod 600 "$home/.ssh/authorized_keys"
  fi
fi
