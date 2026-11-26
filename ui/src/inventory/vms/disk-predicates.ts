import type { Disk, VMDisk } from "../types";

/**
 * diskNeedsThinPool reports whether a disk is backed by the LVM thin pool, which is true for
 * local or unspecified protocols except host-device, kouja, and ceph sources.
 */
export function diskNeedsThinPool(disk: Disk | VMDisk): boolean {
  const source = disk.source ?? "";
  const protocol = disk.protocol ?? "";
  if (source === "host-device" || source === "kouja" || source === "ceph") {
    return false;
  }
  return protocol === "" || protocol === "local";
}

/**
 * diskIsDeleting reports whether a disk has a deletion in progress, indicated by a set
 * deletion_state.
 */
export function diskIsDeleting(disk: Disk): boolean {
  return Boolean(disk.deletion_state);
}
