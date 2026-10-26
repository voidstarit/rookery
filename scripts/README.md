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
    "ssh_authorized_keys": ["ssh-ed25519 AAAA... user@example"]
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
  })
}
```

The example mounts the clone, sets the hostname and timezone, creates the
requested user if needed, and appends SSH authorized keys. It assumes a simple
single-filesystem image; for partitioned images, adapt the mount logic (for
example with `guestfish` or `virt-customize`).

Every stage is logged to stderr and Rookery captures the combined script output
in its logs. The complete `ROOKERY_PROVISION_JSON` payload is logged at the
start of a run, including custom properties. Avoid putting secrets in
provisioning properties if the API logs are visible to a wider audience. The
script always attempts to unmount the clone on exit, including on errors and
interrupts, and retries with a lazy unmount if necessary.

If the clone's `provisioning` object is omitted or empty, Rookery skips the
provisioning script entirely. The script also exits successfully without
mounting when it is invoked manually with an empty `properties` object.

Requirements: `bash`, `jq`, `mount`, `umount`, and the target image must contain
`useradd`/`chown` for non-root users. The script runs as the API user with the
same privileges as other Rookery startup/disk scripts; only use trusted
commands.
