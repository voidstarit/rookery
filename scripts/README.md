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
snapshot. A clone shares the template's filesystem UUIDs, so reset filesystem
or machine IDs if multiple clones will run together.
