# LightVM kernel + initramfs builder.
#
# Kernel configuration is split into fragments under configs/:
#   common.config   base options shared by every variant
#   virtio.config   minimal virtio guest kernel (virtio-fs, -pci, -net, ...)
#   storage.config  virtio plus LVM, iSCSI initiator/target, NBD, and ublk
#
# Two variants are built independently:
#   virtio   common + virtio
#   storage  common + virtio + storage (a superset so it still boots the same
#            LightVM images)
# Each variant has its own object directory and artifact name, so the two
# builds never share a .config or overwrite each other. See configs/README.md.
#
# Init scripts and extra initramfs files live in initramfs/ (copied into both
# images) and initramfs-virtiofs/ (overlay for the virtiofs switch_root image).
# The virtiofs image brings up Ethernet interfaces, runs DHCP, then mounts the
# rootfs virtio-fs tag and switch_roots into it. It also leaves a static
# BusyBox/udhcpc renewal helper in /.rookery inside the guest (disable with
# lightvm.dhcp_renew=0).
# Add scripts there instead of editing shell fragments in this Makefile.
# All build state lives in a Docker volume and all artifacts are written to
# OUT_DIR (default: ../../dist/lightvm, i.e. the repository dist directory).
# Nothing is written back into this directory.
#
# The host only needs GNU make and Docker. The kernel, initramfs, and the
# complete toolchain are built inside a disposable Debian container with a
# read-only root filesystem.
#
# Usage:
#   make                 build both kernels and both initramfs images
#   make kernel          build both kernel variants
#   make kernel-virtio   build only the virtio kernel
#   make kernel-storage  build only the storage kernel
#   make initramfs       build the debug/test initramfs
#   make initramfs-virtiofs  build the initramfs that switch_root's into virtiofs
#   make menuconfig      edit one kernel config (KERNEL_VARIANT=..., default virtio)
#   make reconfigure     reset all kernel configs to the checked-in fragments
#                        (KERNEL_VARIANT=storage resets only that variant)
#   make test            boot the debug artifacts in QEMU (virtio variant)
#   make test-virtiofs   boot the virtiofs/switch_root initramfs in QEMU
#   make shell           interactive shell in the builder container
#   make clean           remove the build volume and current artifacts
#   make distclean       also remove the builder image
#
# Overridable variables:
#   KERNEL_VERSION=6.12.111
#   KERNEL_VARIANT=virtio   variant used by menuconfig and the QEMU tests
#   KERNEL_VARIANTS="virtio storage"  variants built by `make kernel`/`all`
#   KERNEL_URL=https://cdn.kernel.org/pub/linux/kernel/v6.x
#   JOBS=16
#   OUT_DIR=/path/to/artifacts
#   BUILD_VOLUME=rookery-lightvm-kernel-build-<uid>
#   DOCKER=docker
#   DOCKER_IMAGE=rookery-lightvm-kernel-builder:1
#   INITRAMFS_OVERLAY=/path/to/dir   extra files copied into the initramfs
#
# Artifacts:
#   OUT_DIR/kernel-<version>-virtio
#   OUT_DIR/kernel-<version>-storage
#   OUT_DIR/initramfs-lightvm-<version>.cpio.gz
#   OUT_DIR/initramfs-lightvm-virtiofs-<version>.cpio.gz
#   plus stable kernel-lightvm-<variant> and initramfs symlinks without the
#   version suffix.

SHELL := /bin/sh
.DEFAULT_GOAL := all

EXTRA_DIR := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
HOST_UID := $(shell id -u)
HOST_GID := $(shell id -g)

ARCH ?= x86_64
KERNEL_VERSION ?= 6.12.111
KERNEL_MAJOR := $(firstword $(subst ., ,$(KERNEL_VERSION)))
KERNEL_URL ?= https://cdn.kernel.org/pub/linux/kernel/v$(KERNEL_MAJOR).x
JOBS ?= $(shell nproc 2>/dev/null || echo 4)

# Kernel variants. Each variant is a set of config fragments merged after
# common.config; add an entry here and in KERNEL_FRAGMENTS_<variant> /
# KERNEL_REQUIRED_CONFIG_<variant> to introduce another kernel.
KERNEL_VARIANTS ?= virtio storage
# Variant used by single-variant targets such as menuconfig and the QEMU tests.
KERNEL_VARIANT ?= virtio
KERNEL_CONFIG_DIR := $(EXTRA_DIR)/configs

KERNEL_FRAGMENTS_virtio := virtio.config
KERNEL_FRAGMENTS_storage := virtio.config storage.config

# Options that must be =y in the final .config. merge_config.sh silently drops
# options with unmet Kconfig dependencies, so fail the build instead of
# producing a kernel that is missing the features its variant promises.
KERNEL_REQUIRED_CONFIG_virtio := \
	CONFIG_PCI CONFIG_VIRTIO CONFIG_VIRTIO_PCI CONFIG_VIRTIO_BLK \
	CONFIG_VIRTIO_NET CONFIG_VIRTIO_CONSOLE CONFIG_VIRTIO_FS CONFIG_FUSE_FS \
	CONFIG_FILE_LOCKING CONFIG_AIO CONFIG_IO_URING CONFIG_IO_WQ
KERNEL_REQUIRED_CONFIG_storage := \
	$(KERNEL_REQUIRED_CONFIG_virtio) \
	CONFIG_MD CONFIG_BLK_DEV_DM CONFIG_DM_THIN_PROVISIONING \
	CONFIG_SCSI_ISCSI_ATTRS CONFIG_ISCSI_TCP CONFIG_TARGET_CORE \
	CONFIG_CONFIGFS_FS CONFIG_TCM_FILEIO CONFIG_TCM_IBLOCK \
	CONFIG_ISCSI_TARGET CONFIG_BLK_DEV_NBD CONFIG_BLK_DEV_UBLK \
	CONFIG_BLKDEV_UBLK_LEGACY_OPCODES

ifeq ($(strip $(KERNEL_VARIANT)),)
$(error KERNEL_VARIANT must not be empty)
endif
ifeq ($(origin KERNEL_FRAGMENTS_$(KERNEL_VARIANT)),undefined)
$(error unknown KERNEL_VARIANT '$(KERNEL_VARIANT)'; known variants: $(KERNEL_VARIANTS))
endif

KERNEL_FRAGMENTS := $(addprefix $(KERNEL_CONFIG_DIR)/,common.config $(KERNEL_FRAGMENTS_$(KERNEL_VARIANT)))
KERNEL_REQUIRED_CONFIG := $(KERNEL_REQUIRED_CONFIG_$(KERNEL_VARIANT))

OUT_DIR ?= $(EXTRA_DIR)/../../dist/lightvm
ifeq ($(strip $(OUT_DIR)),)
$(error OUT_DIR must not be empty)
endif
OUT_DIR := $(abspath $(OUT_DIR))

DOCKER ?= docker
DOCKER_IMAGE ?= rookery-lightvm-kernel-builder:1
DOCKER_PLATFORM ?= linux/amd64
DOCKER_RUN_FLAGS ?=
BUILD_VOLUME ?= rookery-lightvm-kernel-build-$(HOST_UID)

# Initramfs source trees. The common tree is copied into both images; the
# virtiofs tree is overlaid on top for the switch_root variant. Add scripts and
# files here instead of editing shell fragments in this Makefile.
INITRAMFS_DIR := $(EXTRA_DIR)/initramfs
INITRAMFS_VIRTIOFS_DIR := $(EXTRA_DIR)/initramfs-virtiofs

# Paths inside the builder container. The object directory is variant-specific
# so the two kernels never share a .config or build tree; the kernel source
# tarball and its extracted tree are shared because they are read-only inputs.
BUILD_DIR := /build
DIST_DIR := /out
ROOTFS_DIR := $(BUILD_DIR)/initramfs-root
SRC_DIR := $(BUILD_DIR)/linux-$(KERNEL_VERSION)
OBJ_DIR := $(BUILD_DIR)/obj-$(KERNEL_VERSION)-$(ARCH)-$(KERNEL_VARIANT)
LINUX_TARBALL := $(BUILD_DIR)/linux-$(KERNEL_VERSION).tar.xz
KERNEL_IMAGE := $(OBJ_DIR)/arch/x86/boot/bzImage

# The variant is part of the artifact name and symlink so the virtio and
# storage kernels can coexist in OUT_DIR.
KERNEL_NAME := kernel-$(KERNEL_VERSION)-$(KERNEL_VARIANT)
KERNEL_NAMES := $(foreach variant,$(KERNEL_VARIANTS),kernel-$(KERNEL_VERSION)-$(variant))
KERNEL_LINK_NAMES := $(foreach variant,$(KERNEL_VARIANTS),kernel-lightvm-$(variant))
INITRAMFS_NAME := initramfs-lightvm-$(KERNEL_VERSION).cpio.gz
INITRAMFS_VIRTIOFS_NAME := initramfs-lightvm-virtiofs-$(KERNEL_VERSION).cpio.gz
DIST_KERNEL := $(DIST_DIR)/$(KERNEL_NAME)
DIST_INITRAMFS := $(DIST_DIR)/$(INITRAMFS_NAME)
DIST_INITRAMFS_VIRTIOFS := $(DIST_DIR)/$(INITRAMFS_VIRTIOFS_NAME)
DIST_KERNEL_LINK := $(DIST_DIR)/kernel-lightvm-$(KERNEL_VARIANT)
DIST_INITRAMFS_LINK := $(DIST_DIR)/initramfs-lightvm.cpio.gz
DIST_INITRAMFS_VIRTIOFS_LINK := $(DIST_DIR)/initramfs-lightvm-virtiofs.cpio.gz

HOST_KERNEL := $(OUT_DIR)/$(KERNEL_NAME)
HOST_INITRAMFS := $(OUT_DIR)/$(INITRAMFS_NAME)
HOST_INITRAMFS_VIRTIOFS := $(OUT_DIR)/$(INITRAMFS_VIRTIOFS_NAME)
HOST_KERNEL_LINK := $(OUT_DIR)/kernel-lightvm-$(KERNEL_VARIANT)
HOST_INITRAMFS_LINK := $(OUT_DIR)/initramfs-lightvm.cpio.gz
HOST_INITRAMFS_VIRTIOFS_LINK := $(OUT_DIR)/initramfs-lightvm-virtiofs.cpio.gz

# Artifacts from the previous single-kernel builder, removed by `make clean` so
# an upgrade does not leave stale kernels behind.
LEGACY_KERNEL_FILES := \
	vmlinuz-lightvm vmlinuz-lightvm-$(KERNEL_VERSION) \
	vmlinuz-lightvm-$(KERNEL_VERSION).sha256 \
	kernel-lightvm kernel-$(KERNEL_VERSION)-vfs-iscsi-nbd \
	kernel-$(KERNEL_VERSION)-vfs-iscsi-nbd.sha256

QEMU_TEST_DIR ?= $(if $(TMPDIR),$(TMPDIR),/tmp)/rookery-lightvm-test-$(HOST_UID)
QEMU_LOG := $(QEMU_TEST_DIR)/qemu-test.log
QEMU_DISK := $(QEMU_TEST_DIR)/qemu-test-disk.img
VIRTIOFSD ?= $(EXTRA_DIR)/../virtiofsd/virtiofsd
VIRTIOFS_TEST_SHARE := $(QEMU_TEST_DIR)/virtiofs-rootfs
VIRTIOFS_TEST_SOCKET := $(QEMU_TEST_DIR)/virtiofs.sock
VIRTIOFSD_LOG := $(QEMU_TEST_DIR)/virtiofsd.log
VIRTIOFS_TEST_LOG := $(QEMU_TEST_DIR)/qemu-virtiofs-test.log

ifneq ($(ARCH),x86_64)
$(error this builder currently supports ARCH=x86_64 only)
endif

DOCKER_OVERLAY :=
ifneq ($(strip $(INITRAMFS_OVERLAY)),)
DOCKER_OVERLAY := -v "$(abspath $(INITRAMFS_OVERLAY)):/overlay:ro"
endif

# The container root is read-only. The Makefile is mounted read-only, build
# state goes into a named volume, and only OUT_DIR is writable from the host.
DOCKER_RUN_BASE := $(DOCKER) run --rm --platform $(DOCKER_PLATFORM) \
	--user $(HOST_UID):$(HOST_GID) \
	--read-only --cap-drop=ALL --security-opt=no-new-privileges \
	--tmpfs /tmp:rw,exec,nosuid,nodev,mode=1777,size=2g \
	-e HOME=/tmp \
	-v "$(EXTRA_DIR):/work:ro" \
	-v "$(BUILD_VOLUME):/build:rw" \
	-v "$(OUT_DIR):/out:rw" \
	$(DOCKER_OVERLAY) \
	-w /work $(DOCKER_RUN_FLAGS)

DOCKER_MAKE_ARGS := ARCH="$(ARCH)" KERNEL_VERSION="$(KERNEL_VERSION)" \
	KERNEL_URL="$(KERNEL_URL)" JOBS="$(JOBS)" KERNEL_VARIANT="$(KERNEL_VARIANT)"
DOCKER_MAKE := $(DOCKER_RUN_BASE) $(DOCKER_IMAGE) make -C /work --no-print-directory $(DOCKER_MAKE_ARGS)
DOCKER_MAKE_TTY := $(DOCKER_RUN_BASE) -it $(DOCKER_IMAGE) make -C /work --no-print-directory $(DOCKER_MAKE_ARGS)

# Init scripts and filesystem additions live in initramfs/ (common) and
# initramfs-virtiofs/ (overlay for the virtiofs switch_root variant).

.PHONY: all kernel $(addprefix kernel-,$(KERNEL_VARIANTS)) \
	initramfs initramfs-virtiofs docker-image docker-volume menuconfig reconfigure shell \
	test test-virtiofs clean distclean help _configure _kernel _kernel-variant _reconfigure \
	_initramfs _initramfs_common _initramfs_virtiofs _initramfs_rootfs _menuconfig _shell

# Kernel builds are memory-hungry and share one downloaded source tree; never
# run two variant builds at once, even with `make -j ... kernel-virtio kernel-storage`.
# The two initramfs targets share $(ROOTFS_DIR), so they must not overlap either.
.NOTPARALLEL: $(addprefix kernel-,$(KERNEL_VARIANTS)) initramfs initramfs-virtiofs

$(OBJ_DIR):
	@mkdir -p "$@"

all: kernel initramfs initramfs-virtiofs

# Build every configured variant one at a time. Each recursive make selects a
# single KERNEL_VARIANT so the object directory, config merge, artifact name,
# and symlink are all variant-specific.
kernel:
	@for variant in $(KERNEL_VARIANTS); do \
		echo "==> kernel $$variant"; \
		$(MAKE) --no-print-directory _kernel-variant KERNEL_VARIANT=$$variant || exit 1; \
	done

# Static pattern rule: the variant targets stay explicit (and .PHONY) while
# each one recurses with its own KERNEL_VARIANT.
$(addprefix kernel-,$(KERNEL_VARIANTS)): kernel-%:
	$(MAKE) --no-print-directory _kernel-variant KERNEL_VARIANT=$*

_kernel-variant: docker-image docker-volume
	@mkdir -p "$(OUT_DIR)"
	$(DOCKER_MAKE) _kernel
	@printf 'kernel:    %s\n' "$(HOST_KERNEL)"

initramfs: docker-image docker-volume
	@if [ -n "$(INITRAMFS_OVERLAY)" ] && [ ! -d "$(INITRAMFS_OVERLAY)" ]; then \
		echo "error: INITRAMFS_OVERLAY is not a directory: $(INITRAMFS_OVERLAY)" >&2; \
		exit 1; \
	fi
	@mkdir -p "$(OUT_DIR)"
	$(DOCKER_MAKE) _initramfs
	@printf 'initramfs: %s\n' "$(HOST_INITRAMFS)"

initramfs-virtiofs: docker-image docker-volume
	@if [ -n "$(INITRAMFS_OVERLAY)" ] && [ ! -d "$(INITRAMFS_OVERLAY)" ]; then \
		echo "error: INITRAMFS_OVERLAY is not a directory: $(INITRAMFS_OVERLAY)" >&2; \
		exit 1; \
	fi
	@mkdir -p "$(OUT_DIR)"
	$(DOCKER_MAKE) _initramfs_virtiofs
	@printf 'initramfs: %s\n' "$(HOST_INITRAMFS_VIRTIOFS)"

menuconfig: docker-image docker-volume
	@mkdir -p "$(OUT_DIR)"
	$(DOCKER_MAKE_TTY) _menuconfig

# `make reconfigure` resets every variant. Passing KERNEL_VARIANT explicitly
# limits the reset to that variant (origin is "command line").
ifeq ($(origin KERNEL_VARIANT),command line)
RECONFIGURE_VARIANTS := $(KERNEL_VARIANT)
else
RECONFIGURE_VARIANTS := $(KERNEL_VARIANTS)
endif

reconfigure:
	@for variant in $(RECONFIGURE_VARIANTS); do \
		echo "==> reconfigure $$variant"; \
		$(MAKE) --no-print-directory _reconfigure KERNEL_VARIANT=$$variant || exit 1; \
	done

_reconfigure: docker-image docker-volume
	@mkdir -p "$(OUT_DIR)"
	$(DOCKER_RUN_BASE) $(DOCKER_IMAGE) rm -f "$(OBJ_DIR)/.configured"
	$(DOCKER_MAKE) _configure

shell: docker-image docker-volume
	@mkdir -p "$(OUT_DIR)"
	$(DOCKER_RUN_BASE) -it $(DOCKER_IMAGE) bash

docker-image:
	@command -v $(DOCKER) >/dev/null || { \
		echo "error: docker is required (https://docs.docker.com/engine/install/)" >&2; \
		exit 1; \
	}
	@test -f "$(EXTRA_DIR)/Dockerfile" || { \
		echo "error: missing $(EXTRA_DIR)/Dockerfile" >&2; \
		exit 1; \
	}
	$(DOCKER) build --platform $(DOCKER_PLATFORM) --tag $(DOCKER_IMAGE) \
		--file "$(EXTRA_DIR)/Dockerfile" "$(EXTRA_DIR)"

# Build state is kept in a named volume so no build directory appears next to
# the Makefile. The volume starts root-owned; give its root to the invoking
# user once so unprivileged build containers can write to it.
docker-volume: docker-image
	@$(DOCKER) volume inspect "$(BUILD_VOLUME)" >/dev/null 2>&1 || \
		$(DOCKER) volume create "$(BUILD_VOLUME)" >/dev/null
	@$(DOCKER) run --rm --read-only --platform $(DOCKER_PLATFORM) --user 0:0 \
		-v "$(BUILD_VOLUME):/build" "$(DOCKER_IMAGE)" \
		sh -c 'if [ "$$(stat -c %u /build)" != "$(HOST_UID)" ]; then \
			chown -R $(HOST_UID):$(HOST_GID) /build; \
		fi'

# ---------------------------------------------------------------------------
# Sources and kernel configuration (executed inside the container)
# ---------------------------------------------------------------------------

$(SRC_DIR)/Makefile:
	@mkdir -p "$(BUILD_DIR)"
	@if [ ! -f "$(LINUX_TARBALL)" ]; then \
		echo "downloading linux-$(KERNEL_VERSION).tar.xz"; \
		curl -fL --retry 3 --retry-connrefused \
			-o "$(LINUX_TARBALL).part" \
			"$(KERNEL_URL)/linux-$(KERNEL_VERSION).tar.xz"; \
		mv "$(LINUX_TARBALL).part" "$(LINUX_TARBALL)"; \
	fi
	@echo "extracting $(notdir $(LINUX_TARBALL))"
	@tar -C "$(BUILD_DIR)" --no-same-owner -xJf "$(LINUX_TARBALL)"

# The stamp is touched only after a successful merge, so an interrupted or
# failed configuration is retried instead of leaving a raw tinyconfig behind.
# Each variant merges common.config plus its own fragments into a fresh
# tinyconfig in its own object directory.
$(OBJ_DIR)/.configured: $(SRC_DIR)/Makefile $(KERNEL_FRAGMENTS) $(MAKEFILE_LIST) | $(OBJ_DIR)
	@for fragment in $(KERNEL_FRAGMENTS); do \
		[ -f "$$fragment" ] || { \
			echo "error: missing kernel config fragment $$fragment" >&2; \
			exit 1; \
		}; \
	done
	$(MAKE) -C "$(SRC_DIR)" O="$(OBJ_DIR)" ARCH="$(ARCH)" tinyconfig
	@cd "$(OBJ_DIR)" && "$(SRC_DIR)/scripts/kconfig/merge_config.sh" -m -O "$(OBJ_DIR)" \
		"$(OBJ_DIR)/.config" $(KERNEL_FRAGMENTS)
	$(MAKE) -C "$(SRC_DIR)" O="$(OBJ_DIR)" ARCH="$(ARCH)" olddefconfig
	@for sym in $(KERNEL_REQUIRED_CONFIG); do \
		grep -q "^$$sym=y" "$(OBJ_DIR)/.config" || { \
			echo "error: $$sym is not enabled for KERNEL_VARIANT=$(KERNEL_VARIANT); check $(KERNEL_CONFIG_DIR)" >&2; \
			exit 1; \
		}; \
	done
	@touch "$@"

# ---------------------------------------------------------------------------
# Container-side build targets
# ---------------------------------------------------------------------------

_kernel: $(OBJ_DIR)/.configured
	@test -f "$(OBJ_DIR)/.config" || { echo "error: missing kernel .config" >&2; exit 1; }
	@mkdir -p "$(DIST_DIR)"
	$(MAKE) -C "$(SRC_DIR)" O="$(OBJ_DIR)" ARCH="$(ARCH)" -j"$(JOBS)" bzImage
	install -m 0644 "$(KERNEL_IMAGE)" "$(DIST_KERNEL)"
	@ln -sfn "$(KERNEL_NAME)" "$(DIST_KERNEL_LINK)"
	@cd "$(DIST_DIR)" && sha256sum "$(KERNEL_NAME)" > "$(KERNEL_NAME).sha256"
	@printf 'kernel:    %s (%s bytes, variant %s)\n' "$(DIST_KERNEL)" "$$(stat -c%s "$(DIST_KERNEL)")" "$(KERNEL_VARIANT)"

_configure: $(OBJ_DIR)/.configured

_menuconfig: $(OBJ_DIR)/.configured
	$(MAKE) -C "$(SRC_DIR)" O="$(OBJ_DIR)" ARCH="$(ARCH)" menuconfig

_initramfs_rootfs:
	@test -x /bin/busybox || { \
		echo "error: busybox-static is missing from the builder image" >&2; \
		exit 1; \
	}
	@rm -rf "$(ROOTFS_DIR)"
	@install -d -m 0755 \
		"$(ROOTFS_DIR)/bin" \
		"$(ROOTFS_DIR)/sbin" \
		"$(ROOTFS_DIR)/etc/init.d" \
		"$(ROOTFS_DIR)/etc/udhcpc" \
		"$(ROOTFS_DIR)/proc" \
		"$(ROOTFS_DIR)/sys" \
		"$(ROOTFS_DIR)/dev" \
		"$(ROOTFS_DIR)/tmp" \
		"$(ROOTFS_DIR)/run" \
		"$(ROOTFS_DIR)/root"
	install -m 0755 /bin/busybox "$(ROOTFS_DIR)/bin/busybox"
	@printf '%s\n' 'root:x:0:0:root:/root:/bin/sh' > "$(ROOTFS_DIR)/etc/passwd"
	@printf '%s\n' 'root:x:0:' > "$(ROOTFS_DIR)/etc/group"
	@cd "$(ROOTFS_DIR)" && for app in $$(./bin/busybox --list); do \
		[ "$$app" = busybox ] && continue; \
		ln -sf /bin/busybox "bin/$$app"; \
	done

_initramfs_common: _initramfs_rootfs
	@test -d "$(INITRAMFS_DIR)" || { \
		echo "error: missing initramfs source directory $(INITRAMFS_DIR)" >&2; \
		exit 1; \
	}
	@cp -a "$(INITRAMFS_DIR)/." "$(ROOTFS_DIR)/"
	@chmod 0755 "$(ROOTFS_DIR)/init" 2>/dev/null || true
	@if [ -f "$(ROOTFS_DIR)/etc/udhcpc/default.script" ]; then \
		chmod 0755 "$(ROOTFS_DIR)/etc/udhcpc/default.script"; \
	fi

_initramfs: _initramfs_common
	@if [ -d /overlay ]; then \
		echo "applying initramfs overlay from $(INITRAMFS_OVERLAY)"; \
		cp -a /overlay/. "$(ROOTFS_DIR)/"; \
	fi
	@mkdir -p "$(DIST_DIR)"
	@cd "$(ROOTFS_DIR)" && find . -print0 \
		| cpio --null --create --format=newc --owner=0:0 --reproducible --quiet \
		| gzip -9n > "$(DIST_INITRAMFS)"
	@ln -sfn "$(INITRAMFS_NAME)" "$(DIST_INITRAMFS_LINK)"
	@cd "$(DIST_DIR)" && sha256sum "$(INITRAMFS_NAME)" > "$(INITRAMFS_NAME).sha256"
	@printf 'initramfs: %s (%s bytes)\n' "$(DIST_INITRAMFS)" "$$(stat -c%s "$(DIST_INITRAMFS)")"

_initramfs_virtiofs: _initramfs_common
	@test -d "$(INITRAMFS_VIRTIOFS_DIR)" || { \
		echo "error: missing initramfs source directory $(INITRAMFS_VIRTIOFS_DIR)" >&2; \
		exit 1; \
	}
	@cp -a "$(INITRAMFS_VIRTIOFS_DIR)/." "$(ROOTFS_DIR)/"
	@chmod 0755 "$(ROOTFS_DIR)/init"
	@if [ -d /overlay ]; then \
		echo "applying initramfs overlay from $(INITRAMFS_OVERLAY)"; \
		cp -a /overlay/. "$(ROOTFS_DIR)/"; \
	fi
	@mkdir -p "$(DIST_DIR)"
	@cd "$(ROOTFS_DIR)" && find . -print0 \
		| cpio --null --create --format=newc --owner=0:0 --reproducible --quiet \
		| gzip -9n > "$(DIST_INITRAMFS_VIRTIOFS)"
	@ln -sfn "$(INITRAMFS_VIRTIOFS_NAME)" "$(DIST_INITRAMFS_VIRTIOFS_LINK)"
	@cd "$(DIST_DIR)" && sha256sum "$(INITRAMFS_VIRTIOFS_NAME)" > "$(INITRAMFS_VIRTIOFS_NAME).sha256"
	@printf 'initramfs: %s (%s bytes)\n' "$(DIST_INITRAMFS_VIRTIOFS)" "$$(stat -c%s "$(DIST_INITRAMFS_VIRTIOFS)")"

_shell:
	@bash

# ---------------------------------------------------------------------------
# Host-side helpers
# ---------------------------------------------------------------------------

# Boot both artifacts with virtio-blk and virtio-net attached. The initramfs
# requests DHCP, prints markers, and powers the VM off, so this checks the
# virtio devices, networking, and ACPI poweroff at once.
test: kernel-$(KERNEL_VARIANT) initramfs
	@command -v qemu-system-x86_64 >/dev/null || { \
		echo "error: qemu-system-x86_64 is required for make test" >&2; \
		exit 1; \
	}
	@mkdir -p "$(OUT_DIR)" "$(QEMU_TEST_DIR)"
	@truncate -s 16M "$(QEMU_DISK)"
	@accel="-accel tcg"; cpu=""; \
	if [ -w /dev/kvm ]; then accel="-accel kvm"; cpu="-cpu host"; fi; \
	echo "booting $(HOST_KERNEL)"; \
	timeout 120 qemu-system-x86_64 \
		-machine q35 -m 256M -smp 2 $$accel $$cpu \
		-kernel "$(HOST_KERNEL)" \
		-initrd "$(HOST_INITRAMFS)" \
		-append "console=ttyS0,115200 panic=1 lightvm.dhcp=1 lightvm.test=1" \
		-drive "file=$(QEMU_DISK),if=virtio,format=raw" \
		-netdev user,id=net0 -device virtio-net-pci,netdev=net0 \
		-display none -serial stdio -monitor none -no-reboot \
		> "$(QEMU_LOG)" 2>&1 || true
	@if grep -q 'LIGHTVM-INITRAMFS-OK' "$(QEMU_LOG)" \
		&& grep -q '/dev/vda' "$(QEMU_LOG)" \
		&& grep -q 'eth0.*inet 10.0.2.15/24' "$(QEMU_LOG)"; then \
		echo "qemu smoke test passed"; \
	else \
		echo "qemu smoke test failed; last 60 lines:" >&2; \
		tail -60 "$(QEMU_LOG)" >&2; \
		exit 1; \
	fi

# Boot the switch_root initramfs against a tiny writable virtiofs rootfs.
# The test workload prints a marker, reads /hello, and powers the VM off.
test-virtiofs: kernel-$(KERNEL_VARIANT) initramfs-virtiofs
	@command -v qemu-system-x86_64 >/dev/null || { \
		echo "error: qemu-system-x86_64 is required for make test-virtiofs" >&2; \
		exit 1; \
	}
	@test -x "$(VIRTIOFSD)" || { \
		echo "error: missing virtiofsd at $(VIRTIOFSD)" >&2; \
		echo "run 'make -C $(abspath $(EXTRA_DIR)/../virtiofsd) virtiofsd' or set VIRTIOFSD=..." >&2; \
		exit 1; \
	}
	@mkdir -p "$(OUT_DIR)" "$(QEMU_TEST_DIR)"
	@rm -rf "$(VIRTIOFS_TEST_SHARE)"
	@mkdir -p "$(VIRTIOFS_TEST_SHARE)/bin"
	@printf '%s\n' 'hello-from-virtiofs' > "$(VIRTIOFS_TEST_SHARE)/hello"
	@$(DOCKER) run --rm --entrypoint /bin/cat $(DOCKER_IMAGE) /bin/busybox \
		> "$(VIRTIOFS_TEST_SHARE)/bin/busybox"
	@chmod 0755 "$(VIRTIOFS_TEST_SHARE)/bin/busybox"
	@ln -sf /bin/busybox "$(VIRTIOFS_TEST_SHARE)/bin/sh"
	@printf '%s\n' \
		'GREETING=hello from env' \
		'WITH_EQUALS=a=b' \
		> "$(VIRTIOFS_TEST_SHARE)/.env"
	@cp "$(EXTRA_DIR)/testdata/switch-root-entrypoint" "$(VIRTIOFS_TEST_SHARE)/.entrypoint"
	@chmod 0755 "$(VIRTIOFS_TEST_SHARE)/.entrypoint"
	@rm -f "$(VIRTIOFS_TEST_SOCKET)" "$(VIRTIOFSD_LOG)" "$(VIRTIOFS_TEST_LOG)"
	@accel="-accel tcg"; cpu=""; \
	if [ -w /dev/kvm ]; then accel="-accel kvm"; cpu="-cpu host"; fi; \
	"$(VIRTIOFSD)" --socket-path="$(VIRTIOFS_TEST_SOCKET)" \
		--shared-dir="$(VIRTIOFS_TEST_SHARE)" --sandbox none --cache never \
		> "$(VIRTIOFSD_LOG)" 2>&1 & \
	vfsd=$$!; \
	trap 'kill $$vfsd 2>/dev/null || true' EXIT; \
	i=0; \
	while [ $$i -lt 50 ] && [ ! -S "$(VIRTIOFS_TEST_SOCKET)" ]; do \
		sleep 0.2; i=$$((i+1)); \
	done; \
	if [ ! -S "$(VIRTIOFS_TEST_SOCKET)" ]; then \
		echo "error: virtiofsd did not create $(VIRTIOFS_TEST_SOCKET)" >&2; \
		tail -20 "$(VIRTIOFSD_LOG)" >&2; \
		exit 1; \
	fi; \
	echo "booting $(HOST_KERNEL) with virtiofs rootfs"; \
	timeout 120 qemu-system-x86_64 \
		-machine q35 -m 256M -smp 2 $$accel $$cpu \
		-object memory-backend-memfd,id=mem,size=256M,share=on \
		-numa node,memdev=mem \
		-chardev socket,id=char0,path="$(VIRTIOFS_TEST_SOCKET)" \
		-device vhost-user-fs-pci,chardev=char0,tag=rootfs \
		-kernel "$(HOST_KERNEL)" \
		-initrd "$(HOST_INITRAMFS_VIRTIOFS)" \
		-append "console=ttyS0,115200 panic=1 lightvm.dhcp=1 APP_HOSTNAME='My_App-01'" \
		-netdev user,id=net0 -device virtio-net-pci,netdev=net0 \
		-display none -serial stdio -monitor none -no-reboot \
		> "$(VIRTIOFS_TEST_LOG)" 2>&1 || true; \
	kill $$vfsd 2>/dev/null || true; wait $$vfsd 2>/dev/null || true; \
	if grep -q 'LIGHTVM-ENTRYPOINT-OK' "$(VIRTIOFS_TEST_LOG)" \
		&& grep -q 'GREETING=hello from env' "$(VIRTIOFS_TEST_LOG)" \
		&& grep -q 'WITH_EQUALS=a=b' "$(VIRTIOFS_TEST_LOG)" \
		&& grep -q 'dhcp renewal helper present' "$(VIRTIOFS_TEST_LOG)" \
		&& grep -q 'udhcpc renewal running' "$(VIRTIOFS_TEST_LOG)" \
		&& grep -q 'hello-from-virtiofs' "$(VIRTIOFS_TEST_LOG)" \
		&& grep -q 'eth0.*inet 10.0.2.15/24' "$(VIRTIOFS_TEST_LOG)" \
		&& grep -q 'nameserver 10.0.2.3' "$(VIRTIOFS_TEST_LOG)" \
		&& grep -q 'hostname after switch_root: my-app-01' "$(VIRTIOFS_TEST_LOG)" \
		&& grep -q 'APP_HOSTNAME after switch_root: my-app-01' "$(VIRTIOFS_TEST_LOG)" \
		&& grep -q '/etc/hostname after switch_root: my-app-01' "$(VIRTIOFS_TEST_LOG)" \
		&& grep -q '/dev/tty1 open ok' "$(VIRTIOFS_TEST_LOG)"; then \
		echo "virtiofs switch_root test passed"; \
	else \
		echo "virtiofs switch_root test failed; last 60 lines:" >&2; \
		tail -60 "$(VIRTIOFS_TEST_LOG)" >&2; \
		exit 1; \
	fi

clean:
	@$(DOCKER) volume rm "$(BUILD_VOLUME)" >/dev/null 2>&1 || true
	rm -f \
		$(addprefix $(OUT_DIR)/,$(KERNEL_NAMES)) \
		$(addprefix $(OUT_DIR)/,$(addsuffix .sha256,$(KERNEL_NAMES))) \
		$(addprefix $(OUT_DIR)/,$(KERNEL_LINK_NAMES)) \
		$(addprefix $(OUT_DIR)/,$(LEGACY_KERNEL_FILES)) \
		"$(HOST_INITRAMFS)" "$(HOST_INITRAMFS).sha256" "$(HOST_INITRAMFS_LINK)" \
		"$(HOST_INITRAMFS_VIRTIOFS)" "$(HOST_INITRAMFS_VIRTIOFS).sha256" \
		"$(HOST_INITRAMFS_VIRTIOFS_LINK)" \
		"$(QEMU_LOG)" "$(QEMU_DISK)" "$(VIRTIOFS_TEST_LOG)" "$(VIRTIOFSD_LOG)"
	rm -rf "$(VIRTIOFS_TEST_SHARE)" "$(VIRTIOFS_TEST_SOCKET)"
	@rmdir "$(QEMU_TEST_DIR)" 2>/dev/null || true

distclean: clean
	-$(DOCKER) image rm -f "$(DOCKER_IMAGE)" >/dev/null 2>&1

help:
	@echo "LightVM kernel builder (builds entirely in Docker)"
	@echo ""
	@echo "  make                    build both kernels and both initramfs images"
	@echo "  make kernel             build both kernel variants"
	@echo "  make kernel-virtio      build the virtio-only kernel"
	@echo "  make kernel-storage     build the virtio+storage kernel"
	@echo "  make initramfs          build the debug/test initramfs"
	@echo "  make initramfs-virtiofs build the virtiofs switch_root initramfs"
	@echo "  make menuconfig         edit one kernel configuration"
	@echo "  make reconfigure        reset kernel configurations to configs/*.config"
	@echo "  make test               boot the debug artifacts in QEMU"
	@echo "  make test-virtiofs      test DHCP/renewal, virtiofs, and switch_root"
	@echo "  make shell              shell inside the builder container"
	@echo "  make clean              remove the build volume and artifacts"
	@echo "  make distclean          also remove the builder image"
	@echo ""
	@echo "  kernel configs: $(KERNEL_CONFIG_DIR)"
	@echo "  variants:       $(KERNEL_VARIANTS)  (selected: $(KERNEL_VARIANT))"
	@echo "  artifacts:      $(addprefix kernel-$(KERNEL_VERSION)-,$(KERNEL_VARIANTS))"
	@echo "  initramfs sources: $(INITRAMFS_DIR)"
	@echo "                     $(INITRAMFS_VIRTIOFS_DIR)"
	@echo "  OUT_DIR=$(OUT_DIR)"
	@echo "  BUILD_VOLUME=$(BUILD_VOLUME)"
	@echo "  KERNEL_VERSION=$(KERNEL_VERSION)  KERNEL_URL=$(KERNEL_URL)"
	@echo "  INITRAMFS_OVERLAY=$(INITRAMFS_OVERLAY)"
