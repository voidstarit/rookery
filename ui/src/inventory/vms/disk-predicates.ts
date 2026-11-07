import type { Disk, VMDisk } from "../types";

export function diskNeedsThinPool(disk: Disk | VMDisk): boolean {
  const source = disk.source ?? "";
  const protocol = disk.protocol ?? "";
  if (source === "host-device" || source === "kouja" || source === "ceph") {
    return false;
  }
  return protocol === "" || protocol === "local";
}

export function diskIsDeleting(disk: Disk): boolean {
  return Boolean(disk.deletion_state);
}
