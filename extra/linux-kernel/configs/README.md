# LightVM kernel configuration fragments

The builder produces two independent kernels instead of one kernel with every
feature enabled. Each variant is described by plain Kconfig fragments in this
directory; nothing is embedded in the Makefile.

| Variant   | Fragments merged                              | Purpose |
| --------- | --------------------------------------------- | ------- |
| `virtio`  | `common.config` + `virtio.config`             | Minimal LightVM guest: virtio-pci, virtio-fs, virtio-net, virtio-blk, virtio-scsi, virtio-console, vsock, 9p. |
| `storage` | `common.config` + `virtio.config` + `storage.config` | The virtio kernel plus LVM (device-mapper + dm-thin-pool), the iSCSI initiator/target (open-iscsi, LIO/targetcli), NBD, and ublk. |

`storage` deliberately includes the virtio fragments because it must still boot
the same virtio-fs rootfs and virtio-net images as the `virtio` kernel. It is a
superset, not a replacement.

## Building

From `extra/linux-kernel`:

```sh
make                 # both kernels and both initramfs images
make kernel          # both kernels
make kernel-virtio   # only kernel-<version>-virtio
make kernel-storage  # only kernel-<version>-storage
make menuconfig KERNEL_VARIANT=storage
make reconfigure     # reset both variants to these fragments
```

Artifacts land in `OUT_DIR` (default `dist/lightvm`):

```
kernel-<version>-virtio
kernel-<version>-storage
kernel-lightvm-virtio -> kernel-<version>-virtio
kernel-lightvm-storage -> kernel-<version>-storage
```

Each variant has its own build directory
(`/build/obj-<version>-<arch>-<variant>`) and its own generated `.config`, so
the two builds never share configuration state.

## How a variant is assembled

For each variant the build runs:

1. `make tinyconfig` in the variant's object directory.
2. `scripts/kconfig/merge_config.sh` with `common.config` followed by the
   variant fragments.
3. `make olddefconfig` to resolve dependencies.
4. A check that every symbol listed in `KERNEL_REQUIRED_CONFIG_<variant>` is
   `=y` in the final `.config`; the build fails instead of silently shipping a
   kernel that is missing the features its variant promises.

## Adding another variant

1. Add `configs/<name>.config`.
2. In the Makefile add `KERNEL_FRAGMENTS_<name>` (usually
   `virtio.config <name>.config` if it must boot LightVM images) and
   `KERNEL_REQUIRED_CONFIG_<name>`.
3. Append `<name>` to `KERNEL_VARIANTS`.
4. Build it with `make kernel-<name>`.

## Built-in vs loadable modules

Both variants are built without `CONFIG_MODULES`; the options above are `=y`
and baked into the `bzImage`. LightVM boots a kernel plus an initramfs, so a
self-contained kernel avoids shipping and loading a module tree. If a variant
ever needs loadable modules, it must also package and install the matching
`.ko` files into the initramfs or the virtio-fs rootfs.
