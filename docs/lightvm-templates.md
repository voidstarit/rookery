# LightVM templates and managed groups

LightVM templates make image-backed LightVM fleets repeatable. A template holds
the specification used to create each member; a group pins one template version
and maintains an operator-set number of members.

This first release is intentionally narrow:

* Group members are **image-backed LightVMs**. The template image must be
  digest-qualified (`@sha256:<64 hex characters>`).
* Group templates create **blank data disks** from a storage backend and fresh
  vhost-user interfaces. Disk-template cloning and host passthrough are rejected
  in group templates.
* VM templates, VM groups, and disk-backed LightVM groups are deferred.
* Readiness is QEMU-level. Rookery does not verify guest health.

## Concepts

| Record | Purpose |
| --- | --- |
| `lightvm_templates` | Named template metadata. |
| `lightvm_template_versions` | Immutable specifications. Editing a spec creates a version. |
| `lightvm_groups` | Desired count, pinned version, pause/delete state, last reconcile/error. |
| `lightvm_group_members` | Stable member identity, lifecycle state, power intent, retry state. |

A group pins one version for **new** members. Existing members keep the version
that created them. Repair uses the member's version, not the group's current
pin.

## Member lifecycle

Member states are `provisioning`, `active`, and `removing`.

* A failed create or repair stays `provisioning` with error/backoff. The row is
  not deleted, so a stopped member stays stopped when it is retried.
* An active member whose LightVM disappears is repaired in place with fresh
  disks and interfaces and the same member name.
* An explicit stop updates member intent. The fleet does not restart a stopped
  member.
* Scale-in removes failed provisioning members with no workload first, then the
  newest active members.
* Group deletion marks all remaining members `removing`, cleans their owned
  resources, then deletes the group.
* Force deletion uses the existing stop path. Rookery refuses to clear
  ownership or delete the group while QEMU is still alive. If QEMU stopped but
  LightVM cleanup fails, force leaves a stopped, unowned LightVM in inventory
  with its attachments and deletes the member/group rows.

Counts are computed from member rows and stored LightVM status:

* `actual_count` — members with a LightVM.
* `running_count` — members whose LightVM status is `running`.
* `provisioning_count`, `removing_count` — member lifecycle states.
* `drift` — `desired_count - actual_count`.
* `converged` — actual equals desired and no provisioning/removing members
  remain.

A group is **ready** when `running_count == desired_count`. `converged` is
separate: a stopped member can be converged but not running.

## API

| Method | Path | Result |
| --- | --- | --- |
| `GET` | `/api/lightvm/templates` | List templates |
| `POST` | `/api/lightvm/templates` | Create template + version 1 |
| `GET` | `/api/lightvm/templates/{id}` | Get template |
| `PUT` | `/api/lightvm/templates/{id}` | Update name/description only |
| `DELETE` | `/api/lightvm/templates/{id}` | Delete an unpinned template |
| `GET` | `/api/lightvm/templates/{id}/versions` | List versions |
| `POST` | `/api/lightvm/templates/{id}/versions` | Create a version |
| `GET` | `/api/lightvm/templates/{id}/versions/{version_id}` | Read a version |
| `GET` | `/api/lightvm/groups` | List groups |
| `POST` | `/api/lightvm/groups` | Create group (asynchronous members) |
| `GET` | `/api/lightvm/groups/{id}` | Group with members and counts |
| `PUT` | `/api/lightvm/groups/{id}` | Update count, pause, version, metadata |
| `DELETE` | `/api/lightvm/groups/{id}` | 204 if empty, 202 if members remain |
| `DELETE` | `/api/lightvm/groups/{id}?force=true` | Force deletion |
| `POST` | `/api/lightvm/groups/{id}/members/{member_id}/start` | Set member intent running |
| `POST` | `/api/lightvm/groups/{id}/members/{member_id}/stop` | Set member intent stopped |

Member start/stop actions are asynchronous: they update intent and wake the
fleet worker. They do not block on an image pull.

## Terraform

```hcl
resource "rookery_lightvm_template" "web" {
  name        = "web"
  description = "web fleet"

  spec = {
    vcpus       = 2
    memory_mb   = 2048
    kernel_id   = rookery_lightvm_kernel.kernel.id
    initramfs_id = rookery_lightvm_initramfs.initramfs.id
    image       = "registry.example.com/web@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
    append      = "console=hvc0 rdinit=/init"

    executable_script = <<-EOT
      #!/bin/sh
      exec /usr/local/bin/web
    EOT

    env {
      name  = "APP_ENV"
      value = "production"
    }

    disks {
      storage_backend_id = rookery_storage_backend.default.id
      size_gb            = 20
      serial             = "data"
    }

    interfaces {
      network_id = rookery_network.lab.id
    }
  }
}

resource "rookery_lightvm_group" "web" {
  name                = "web-group"
  template_version_id = rookery_lightvm_template.web.current_version_id
  desired_count       = 3
  paused              = false
  wait_for_ready      = true
  timeout             = "2h"
  force_destroy       = false
  destroy_timeout     = "6h"
}
```

* Changing the template `spec` creates a new version and updates
  `current_version_id`.
* Changing only name/description updates metadata without creating a version.
* The group pins `template_version_id` explicitly. Moving it affects new
  members only.
* Import a template by numeric id; import binds to the latest version unless a
  current version is already in state. Import a group by numeric id.
* `wait_for_ready` waits for `converged` and `running_count == desired_count`.
* `force_destroy` passes `force=true`; deletion still polls until the group is
  gone, bounded by `destroy_timeout`.

## Operations

Use the LightVM **Templates** and **Groups** subtabs to create templates, create
new versions, create groups, scale, pause, and delete. The group detail page
polls every five seconds and exposes member start/stop actions.

Recommended flow:

1. Create the kernel, initramfs, storage backend, network, and ACLs.
2. Create a template with a digest-qualified image.
3. Create a group with a desired count.
4. Watch `actual_count`, `running_count`, and `converged`.
5. Scale by changing `desired_count`.
6. Stop individual members when needed; stop intent survives repair.
7. Delete the group before deleting a pinned template.

## Limitations

* No capacity admission: a large desired count can exhaust the thin pool or
  host memory. Failures are retried with backoff.
* Fleet work is sequential; a slow image pull delays later groups.
* Interface deletion requires VPP. Cleanup backs off while VPP is rebuilding;
  force deletion may leave owned interfaces in inventory.
* No guest-level health checks.
* The first UI template editor exposes the core image, compute, data-disk, and
  interface fields. Virtiofs shares and ordered ACL assignments remain
  API/Terraform-managed.
