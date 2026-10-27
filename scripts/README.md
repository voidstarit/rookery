# Scripts

## `lvm-thin-snapshot-disk.sh`

Creates a disk as a thin-LVM snapshot of a template/source LV, or removes one.
It can also be used as a Rookery storage backend `preparation_script`.

### Rookery storage backend mode

Set the backend's `preparation_script` to the absolute path of this script and
enable `supports_templates`:

```hcl
resource "rookery_storage_backend" "pool" {
  name   = "pool"
  driver = "lvm-thin"

  config = {
    vg                 = "vg0"
    thin_pool          = "rookery"
    supports_templates = true
    preparation_script = "/opt/rookery/scripts/lvm-thin-snapshot-disk.sh"
  }
}
```

When a disk is created with `template_disk_id`, Rookery invokes the script with
`ROOKERY_DISK_JSON` set. The script creates a thin snapshot named after the new
disk, optionally extends it to `disk.size_gb`, and prints the device path.
It also handles the API's `delete` operation for script-created clones.

### Standalone CLI

```bash
sudo ./scripts/lvm-thin-snapshot-disk.sh create \
  --vg vg0 \
  --thin-pool rookery \
  --template debian-template \
  --name web-01 \
  --size-gb 20

sudo ./scripts/lvm-thin-snapshot-disk.sh delete \
  --vg vg0 \
  --thin-pool rookery \
  --name web-01
```

Requirements:

- `lvm2` (`lvs`, `lvcreate`, `lvchange`, `lvextend`, `lvremove`)
- root or equivalent LVM permissions
- `jq` when invoked in API mode

The source/template (also accepted as `--source` or `--snapshot`) must be a
thin LV in the selected thin pool, including another thin snapshot. The target
LV must not already exist and is activated explicitly because LVM creates thin
snapshots with the activation-skip flag by default. Expanding `--size-gb`
extends the LV's virtual size; it does not resize a filesystem inside the
snapshot. The template provisioning script's `resize_disk` option can grow the
root filesystem within its current root partition after the clone is created.
A clone shares the template's filesystem UUIDs, so reset filesystem or machine
IDs if multiple clones will run together.

## `template-provision.sh`

Example `provisioning_script` for a Rookery template disk. After a clone is
created by the backend `preparation_script`, Rookery runs this script with
`ROOKERY_PROVISION_JSON` set to a JSON object:

```json
{
  "operation": "provision",
  "request_id": "...",
  "disk": { "name": "web-01", "device": "/dev/vg0/web-01", "...": "..." },
  "template": { "name": "debian-template", "...": "..." },
  "backend": { "name": "pool", "...": "..." },
  "properties": {
    "hostname": "web-01",
    "timezone": "UTC",
    "username": "deploy",
    "ssh_authorized_keys": ["ssh-ed25519 AAAA... user@example"],
    "resize_disk": true
  }
}
```

Set the template's `provisioning_script` to this file's absolute path and
optionally set `provisioning_timeout` (default `5m`). In the Terraform provider:

```hcl
resource "rookery_disk" "template" {
  name                 = "debian-template"
  is_template          = true
  provisioning_script  = "/opt/rookery/scripts/template-provision.sh"
  provisioning_timeout = "5m"
}

resource "rookery_disk" "web" {
  name             = "web-01"
  template_disk_id = rookery_disk.template.id
  provisioning_json = jsonencode({
    hostname            = "web-01"
    timezone            = "UTC"
    username            = "deploy"
    ssh_authorized_keys = ["ssh-ed25519 AAAA... user@example"]
    resize_disk         = true
  })
}
```

The example mounts the clone, sets the hostname and timezone, creates the
requested user if needed, appends SSH authorized keys, and can resize the root
filesystem. It handles both a filesystem directly on the device and partitioned
images. The root partition is selected by a `root`/`rootfs`/`linux-root`
filesystem or partition label, or by probing each partition for
`/etc/os-release` or `/etc/passwd`. LVM logical volumes are commonly created
with partition scanning disabled, so the script reads the partition table with
`partx`, then exposes the partitions with `kpartx` when available or with
`dmsetup` linear mappings otherwise. Those temporary mappings are removed after
unmounting.

When `properties.resize_disk` is `true`, the script grows the root filesystem to
the size of its root block device: `resize2fs` for ext2/ext3/ext4,
`xfs_growfs` for XFS, or `btrfs filesystem resize max` for Btrfs. It does **not**
rewrite the partition table. If the cloned LV is larger than the root
partition, the filesystem can only grow within that partition; use a template
whose root partition reaches the end of the disk, expand the partition first,
or add a swap-aware repartitioning step.

Every stage is logged to stderr and Rookery captures the combined script output
in its logs. The complete `ROOKERY_PROVISION_JSON` payload is logged at the
start of a run, including custom properties. Avoid putting secrets in
provisioning properties if the API logs are visible to a wider audience. The
script always attempts to unmount the clone on exit, including on errors and
interrupts, and retries with a lazy unmount if necessary.

If the clone's `provisioning` object is omitted or empty, Rookery skips the
provisioning script entirely. The script also exits successfully without
mounting when it is invoked manually with an empty `properties` object.

Requirements: `bash`, `jq`, `mount`, `umount`, `lsblk`, and `blkid`; `partx`
is additionally required for partitioned images. For partitioned LVM logical
volumes, `kpartx` or `dmsetup` is required to create temporary partition
mappings; `dmsetup` is included with `lvm2`. `resize_disk` also requires the
resize tool for the guest filesystem (`resize2fs` from `e2fsprogs`,
`xfs_growfs` from `xfsprogs`, or `btrfs` from `btrfs-progs`). The target image
must contain `useradd`/`chown` for non-root users. The script runs as the API
user with the same privileges as other Rookery startup/disk scripts; only use
trusted commands.
