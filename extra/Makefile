# LightVM minimal kernel + initramfs builder.
#
# This directory intentionally contains only this Makefile and Dockerfile.
# All build state lives in a Docker volume and all artifacts are written to
# OUT_DIR (default: ../dist/lightvm). Nothing is written back into this
# directory.
#
# The host only needs GNU make and Docker. The kernel, initramfs, and the
# complete toolchain are built inside a disposable Debian container with a
# read-only root filesystem.
#
# Usage:
#   make                 build kernel and initramfs
#   make kernel          build only the kernel
#   make initramfs       build only the initramfs
#   make menuconfig      edit the kernel config
#   make reconfigure     reset the kernel config to the built-in fragment
#   make test            boot the artifacts in QEMU
#   make shell           interactive shell in the builder container
#   make clean           remove the build volume and current artifacts
#   make distclean       also remove the builder image
#
# Overridable variables:
#   KERNEL_VERSION=6.12.111
#   KERNEL_URL=https://cdn.kernel.org/pub/linux/kernel/v6.x
#   JOBS=16
#   OUT_DIR=/path/to/artifacts
#   BUILD_VOLUME=rookery-lightvm-kernel-build-<uid>
#   DOCKER=docker
#   DOCKER_IMAGE=rookery-lightvm-kernel-builder:1
#   INITRAMFS_OVERLAY=/path/to/dir   extra files copied into the initramfs
#
# Artifacts:
#   OUT_DIR/vmlinuz-lightvm-<version>
#   OUT_DIR/initramfs-lightvm-<version>.cpio.gz
#   plus stable symlinks without the version suffix.

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

OUT_DIR ?= $(EXTRA_DIR)/../dist/lightvm
ifeq ($(strip $(OUT_DIR)),)
$(error OUT_DIR must not be empty)
endif
OUT_DIR := $(abspath $(OUT_DIR))

DOCKER ?= docker
DOCKER_IMAGE ?= rookery-lightvm-kernel-builder:1
DOCKER_PLATFORM ?= linux/amd64
DOCKER_RUN_FLAGS ?=
BUILD_VOLUME ?= rookery-lightvm-kernel-build-$(HOST_UID)

# virtiofsd is downloaded next to this Makefile. The default source is the
# upstream virtio-fs publish job for main, which provides a static x86_64
# musl binary. Override VIRTIOFSD_URL and VIRTIOFSD_SHA256 for a pinned build.
VIRTIOFSD_REF ?= main
VIRTIOFSD_URL ?= https://gitlab.com/virtio-fs/virtiofsd/-/jobs/artifacts/$(VIRTIOFSD_REF)/download?job=publish
VIRTIOFSD_SHA256 ?=
VIRTIOFSD_DIR := $(EXTRA_DIR)/virtiofsd
VIRTIOFSD_BIN := $(VIRTIOFSD_DIR)/virtiofsd

# Paths inside the builder container.
BUILD_DIR := /build
DIST_DIR := /out
ROOTFS_DIR := $(BUILD_DIR)/initramfs-root
SRC_DIR := $(BUILD_DIR)/linux-$(KERNEL_VERSION)
OBJ_DIR := $(BUILD_DIR)/obj-$(KERNEL_VERSION)-$(ARCH)
KCONFIG := $(BUILD_DIR)/lightvm.config
LINUX_TARBALL := $(BUILD_DIR)/linux-$(KERNEL_VERSION).tar.xz
KERNEL_IMAGE := $(OBJ_DIR)/arch/x86/boot/bzImage

KERNEL_NAME := vmlinuz-lightvm-$(KERNEL_VERSION)
INITRAMFS_NAME := initramfs-lightvm-$(KERNEL_VERSION).cpio.gz
DIST_KERNEL := $(DIST_DIR)/$(KERNEL_NAME)
DIST_INITRAMFS := $(DIST_DIR)/$(INITRAMFS_NAME)
DIST_KERNEL_LINK := $(DIST_DIR)/vmlinuz-lightvm
DIST_INITRAMFS_LINK := $(DIST_DIR)/initramfs-lightvm.cpio.gz

HOST_KERNEL := $(OUT_DIR)/$(KERNEL_NAME)
HOST_INITRAMFS := $(OUT_DIR)/$(INITRAMFS_NAME)
HOST_KERNEL_LINK := $(OUT_DIR)/vmlinuz-lightvm
HOST_INITRAMFS_LINK := $(OUT_DIR)/initramfs-lightvm.cpio.gz
QEMU_TEST_DIR ?= $(if $(TMPDIR),$(TMPDIR),/tmp)/rookery-lightvm-test-$(HOST_UID)
QEMU_LOG := $(QEMU_TEST_DIR)/qemu-test.log
QEMU_DISK := $(QEMU_TEST_DIR)/qemu-test-disk.img

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
	-v "$(EXTRA_DIR)/Makefile:/work/Makefile:ro" \
	-v "$(BUILD_VOLUME):/build:rw" \
	-v "$(OUT_DIR):/out:rw" \
	$(DOCKER_OVERLAY) \
	-w /work $(DOCKER_RUN_FLAGS)

DOCKER_MAKE_ARGS := ARCH="$(ARCH)" KERNEL_VERSION="$(KERNEL_VERSION)" \
	KERNEL_URL="$(KERNEL_URL)" JOBS="$(JOBS)"
DOCKER_MAKE := $(DOCKER_RUN_BASE) $(DOCKER_IMAGE) make -C /work --no-print-directory $(DOCKER_MAKE_ARGS)
DOCKER_MAKE_TTY := $(DOCKER_RUN_BASE) -it $(DOCKER_IMAGE) make -C /work --no-print-directory $(DOCKER_MAKE_ARGS)

# ---------------------------------------------------------------------------
# Embedded build inputs. Keeping the config fragment and init scripts here
# means the source directory needs no generated or auxiliary files.
# ---------------------------------------------------------------------------

define KCONFIG_CONTENT
# Architecture and process basics
CONFIG_64BIT=y
CONFIG_SMP=y
CONFIG_NR_CPUS=16
CONFIG_PRINTK=y
CONFIG_BUG=y
CONFIG_MULTIUSER=y
CONFIG_BINFMT_ELF=y
CONFIG_BINFMT_SCRIPT=y
CONFIG_COREDUMP=y
CONFIG_FUTEX=y
CONFIG_EPOLL=y
CONFIG_SIGNALFD=y
CONFIG_TIMERFD=y
CONFIG_EVENTFD=y
CONFIG_INOTIFY_USER=y
CONFIG_TTY=y
CONFIG_UNIX98_PTYS=y
CONFIG_SERIAL_8250=y
CONFIG_SERIAL_8250_CONSOLE=y
CONFIG_SERIAL_8250_NR_UARTS=4
CONFIG_SERIAL_8250_RUNTIME_UARTS=4
CONFIG_SERIAL_CORE=y
CONFIG_SERIAL_CORE_CONSOLE=y
CONFIG_EARLY_PRINTK=y
CONFIG_DEFAULT_HOSTNAME="lightvm"
CONFIG_LOCALVERSION="-lightvm"

# Initramfs and memory-backed filesystems (no on-disk filesystems)
CONFIG_BLOCK=y
CONFIG_BLK_DEV_INITRD=y
CONFIG_RD_GZIP=y
CONFIG_DEVTMPFS=y
CONFIG_DEVTMPFS_MOUNT=y
CONFIG_PROC_FS=y
CONFIG_PROC_SYSCTL=y
CONFIG_SYSFS=y
CONFIG_SHMEM=y
CONFIG_TMPFS=y

# PCI and virtio
CONFIG_PCI=y
CONFIG_PCI_MSI=y
CONFIG_VIRTIO=y
CONFIG_VIRTIO_MENU=y
CONFIG_VIRTIO_PCI=y
CONFIG_VIRTIO_BLK=y
CONFIG_VIRTIO_NET=y
CONFIG_VIRTIO_CONSOLE=y
CONFIG_VIRTIO_BALLOON=y
CONFIG_VIRTIO_VSOCKETS=y
CONFIG_VSOCKETS=y
CONFIG_FUSE_FS=y
CONFIG_VIRTIO_FS=y
CONFIG_HW_RANDOM=y
CONFIG_HW_RANDOM_VIRTIO=y
CONFIG_SCSI=y
CONFIG_BLK_DEV_SD=y
CONFIG_SCSI_VIRTIO=y
CONFIG_NET_9P=y
CONFIG_NET_9P_VIRTIO=y
CONFIG_9P_FS=y
CONFIG_VIRTIO_MMIO=y
CONFIG_VIRTIO_MMIO_CMDLINE_DEVICES=y

# Deliberately not enabled on this minimal image:
# - DRM_VIRTIO_GPU: needs DRM; LightVM runs with -display none
# - SND_VIRTIO: needs SOUND
# - VIRTIO_INPUT: needs INPUT; no keyboard/tablet
# - I2C_VIRTIO: needs I2C
# - VIRTIO_DMA_SHARED_BUFFER: only useful with virtio-gpu
# - VIRTIO_DEBUG: needs debugfs; debug support is disabled
# - VIRTIO_IOMMU, RPMSG_VIRTIO, CRYPTO_DEV_VIRTIO: not used by LightVM
# - VIRTIO_RTC*: not present in Linux 6.12 (introduced in 6.17)

# Guest integration and ACPI poweroff
CONFIG_HYPERVISOR_GUEST=y
CONFIG_PARAVIRT=y
CONFIG_KVM_GUEST=y
CONFIG_ACPI=y

# Networking (DHCP in the initramfs)
CONFIG_NET=y
CONFIG_UNIX=y
CONFIG_INET=y
CONFIG_PACKET=y
CONFIG_NETDEVICES=y

# Keep the image small: no debug, USB, sound, DRM, or real filesystems
CONFIG_DEBUG_INFO_NONE=y
# CONFIG_DEBUG_KERNEL is not set
# CONFIG_DEBUG_FS is not set
# CONFIG_MAGIC_SYSRQ is not set
# CONFIG_KALLSYMS is not set
# CONFIG_FTRACE is not set
# CONFIG_FUNCTION_TRACER is not set
# CONFIG_PERF_EVENTS is not set
# CONFIG_SOUND is not set
# CONFIG_USB_SUPPORT is not set
# CONFIG_WLAN is not set
# CONFIG_WIRELESS is not set
# CONFIG_VT is not set
# CONFIG_INPUT is not set
# CONFIG_SERIO is not set
# CONFIG_ACPI_BUTTON is not set
# CONFIG_INPUT_JOYSTICK is not set
# CONFIG_HID is not set
# CONFIG_DRM is not set
# CONFIG_NFS_FS is not set
# CONFIG_CIFS is not set
# CONFIG_EXT4_FS is not set
# CONFIG_BTRFS_FS is not set
# CONFIG_XFS_FS is not set
endef

define INITRAMFS_INIT
#!/bin/sh
# LightVM initramfs PID 1.
PATH=/bin:/sbin
export PATH

mount -t proc proc /proc 2>/dev/null || true
mount -t sysfs sysfs /sys 2>/dev/null || true
mount -t devtmpfs devtmpfs /dev 2>/dev/null || true
mkdir -p /dev/pts /dev/shm /run /tmp
mount -t devpts devpts /dev/pts 2>/dev/null || true
mount -t tmpfs -o mode=1777 tmpfs /tmp 2>/dev/null || true
mount -t tmpfs tmpfs /run 2>/dev/null || true

# LightVM attaches the container root filesystem as a virtio-fs device with
# the tag "rootfs". Mount it when present so the workload/hook can use it.
virtiofs_present=0
for dev in /sys/bus/virtio/drivers/virtiofs/* /sys/bus/virtio/drivers/virtio_fs/*; do
	[ -e "$dev" ] || continue
	virtiofs_present=1
	break
done
if [ "$virtiofs_present" = 1 ]; then
	mkdir -p /rootfs
	if mount -t virtiofs rootfs /rootfs 2>/dev/null; then
		echo "lightvm: mounted virtiofs tag rootfs at /rootfs"
	else
		rmdir /rootfs 2>/dev/null || true
	fi
fi

cmdline=" $(cat /proc/cmdline 2>/dev/null) "
hostname=lightvm
shell=auto
test_mode=0
dhcp=0
for arg in $cmdline; do
	case "$arg" in
		lightvm.hostname=*) hostname=${arg#lightvm.hostname=} ;;
		lightvm.shell=*) shell=${arg#lightvm.shell=} ;;
		lightvm.test=1) test_mode=1 ;;
		lightvm.dhcp=1|ip=dhcp) dhcp=1 ;;
	esac
done
hostname "$hostname" 2>/dev/null || true

if [ "$dhcp" = 1 ]; then
	for path in /sys/class/net/*; do
		[ -e "$path" ] || continue
		dev=${path##*/}
		[ "$dev" = lo ] && continue
		[ "$(cat "$path/type" 2>/dev/null)" = 1 ] || continue
		ip link set "$dev" up 2>/dev/null || continue
		echo "lightvm: DHCP on $dev"
		udhcpc -i "$dev" -q -n -t 5 2>/dev/null || echo "lightvm: DHCP failed on $dev"
	done
fi

if [ -x /etc/init.d/rcS ]; then
	/etc/init.d/rcS
fi

if [ "$test_mode" = 1 ]; then
	echo "lightvm: uname: $(uname -a)"
	echo "lightvm: virtio block devices:"
	found=0
	for dev in /dev/vd*; do
		[ -e "$dev" ] || continue
		echo "  $dev"
		found=1
	done
	[ "$found" = 1 ] || echo "  (none)"
	echo "lightvm: network interfaces:"
	ip -o link 2>/dev/null | while read -r line; do echo "  $line"; done
	echo "lightvm: network addresses:"
	ip -o addr 2>/dev/null | while read -r line; do echo "  $line"; done
	echo "lightvm: virtio devices:"
	for dev in /sys/bus/virtio/devices/*; do
		[ -e "$dev" ] || continue
		echo "  ${dev##*/} $(cat "$dev/modalias" 2>/dev/null)"
	done
	echo "lightvm: virtio drivers:"
	echo "  $(ls /sys/bus/virtio/drivers 2>/dev/null | tr '\n' ' ')"
	if grep -q ' /rootfs virtiofs ' /proc/mounts 2>/dev/null; then
		echo "lightvm: virtiofs rootfs mounted"
		if [ -f /rootfs/hello ]; then
			echo "  /rootfs/hello: $(cat /rootfs/hello)"
		fi
	else
		echo "lightvm: virtiofs rootfs not attached"
	fi
	echo "LIGHTVM-INITRAMFS-OK"
	sync
	poweroff -f
fi

start_shell=0
case "$shell" in
	0|no|false) ;;
	1|yes|true) start_shell=1 ;;
	*)
		if [ -t 0 ]; then start_shell=1; fi
		;;
esac

if [ "$start_shell" = 1 ]; then
	echo "lightvm: starting shell (exit to power off)"
	/bin/sh
	echo "lightvm: shell exited; powering off"
	sync
	poweroff -f
fi

echo "lightvm: initramfs ready; idling (lightvm.shell=1 for a shell)"
while :; do sleep 3600; done
endef

define UDHCPC_SCRIPT
#!/bin/sh
case "$1" in
	deconfig)
		ip link set "$interface" up 2>/dev/null || true
		ip addr flush dev "$interface" 2>/dev/null || true
		;;
	bound|renew)
		ip link set "$interface" up 2>/dev/null || true
		ip addr flush dev "$interface" 2>/dev/null || true
		ip addr add "${ip}/${mask:-24}" dev "$interface"
		if [ -n "$router" ]; then
			for gateway in $router; do
				ip route add default via "$gateway" dev "$interface" 2>/dev/null || true
				break
			done
		fi
		{
			echo "# generated by udhcpc"
			for dns_server in $dns; do
				echo "nameserver $dns_server"
			done
		} > /etc/resolv.conf
		;;
esac
exit 0
endef

.PHONY: all kernel initramfs docker-image docker-volume menuconfig reconfigure shell test \
	virtiofsd clean distclean help _configure _kernel _initramfs _initramfs_rootfs _menuconfig _shell

$(OBJ_DIR):
	@mkdir -p "$@"

all: kernel initramfs

kernel: docker-image docker-volume
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

menuconfig: docker-image docker-volume
	@mkdir -p "$(OUT_DIR)"
	$(DOCKER_MAKE_TTY) _menuconfig

reconfigure: docker-image docker-volume
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

$(KCONFIG): $(MAKEFILE_LIST)
	$(file >$(KCONFIG).tmp,$(value KCONFIG_CONTENT))
	@if [ -f "$@" ] && cmp -s "$@.tmp" "$@"; then \
		rm -f "$@.tmp"; \
	else \
		mv "$@.tmp" "$@"; \
	fi

# The stamp is touched only after a successful merge, so an interrupted or
# failed configuration is retried instead of leaving a raw tinyconfig behind.
$(OBJ_DIR)/.configured: $(SRC_DIR)/Makefile $(KCONFIG) | $(OBJ_DIR)
	$(MAKE) -C "$(SRC_DIR)" O="$(OBJ_DIR)" ARCH="$(ARCH)" tinyconfig
	@cd "$(OBJ_DIR)" && "$(SRC_DIR)/scripts/kconfig/merge_config.sh" -m -O "$(OBJ_DIR)" \
		"$(OBJ_DIR)/.config" "$(KCONFIG)"
	$(MAKE) -C "$(SRC_DIR)" O="$(OBJ_DIR)" ARCH="$(ARCH)" olddefconfig
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
	@printf 'kernel:    %s (%s bytes)\n' "$(DIST_KERNEL)" "$$(stat -c%s "$(DIST_KERNEL)")"

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

_initramfs: _initramfs_rootfs
	$(file >$(ROOTFS_DIR)/init,$(value INITRAMFS_INIT))
	$(file >$(ROOTFS_DIR)/etc/udhcpc/default.script,$(value UDHCPC_SCRIPT))
	@chmod 0755 "$(ROOTFS_DIR)/init" "$(ROOTFS_DIR)/etc/udhcpc/default.script"
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

_shell:
	@bash

# ---------------------------------------------------------------------------
# Host-side helpers
# ---------------------------------------------------------------------------

# Download the upstream static x86_64 virtiofsd artifact into extra/virtiofsd.
# The file is not committed; release builds include it next to rookery.
virtiofsd: $(VIRTIOFSD_BIN)

$(VIRTIOFSD_BIN):
	@command -v curl >/dev/null || { echo "error: curl is required to download virtiofsd" >&2; exit 1; }
	@command -v unzip >/dev/null || { echo "error: unzip is required to extract virtiofsd" >&2; exit 1; }
	@mkdir -p "$(VIRTIOFSD_DIR)"
	@tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
		echo "downloading virtiofsd from $(VIRTIOFSD_URL)"; \
		curl -fL --retry 3 --retry-connrefused -o "$$tmp/virtiofsd.zip" "$(VIRTIOFSD_URL)"; \
		unzip -q "$$tmp/virtiofsd.zip" -d "$$tmp/extract"; \
		found=$$(find "$$tmp/extract" -type f -name virtiofsd -print -quit); \
		test -n "$$found" || { echo "error: virtiofsd artifact did not contain a virtiofsd binary" >&2; exit 1; }; \
		if [ -n "$(VIRTIOFSD_SHA256)" ]; then \
			echo "$(VIRTIOFSD_SHA256)  $$found" | sha256sum -c - || exit 1; \
		fi; \
		install -m 0755 "$$found" "$(VIRTIOFSD_BIN)"; \
		version=$$("$(VIRTIOFSD_BIN)" --version 2>/dev/null | head -n 1 || true); \
		echo "virtiofsd: $(VIRTIOFSD_BIN) $${version}"

# Boot both artifacts with virtio-blk and virtio-net attached. The initramfs
# requests DHCP, prints markers, and powers the VM off, so this checks the
# virtio devices, networking, and ACPI poweroff at once.
test: all
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

clean:
	@$(DOCKER) volume rm "$(BUILD_VOLUME)" >/dev/null 2>&1 || true
	rm -f "$(HOST_KERNEL)" "$(HOST_KERNEL).sha256" "$(HOST_KERNEL_LINK)" \
		"$(HOST_INITRAMFS)" "$(HOST_INITRAMFS).sha256" "$(HOST_INITRAMFS_LINK)" \
		"$(QEMU_LOG)" "$(QEMU_DISK)"
	@rmdir "$(QEMU_TEST_DIR)" 2>/dev/null || true

distclean: clean
	-$(DOCKER) image rm -f "$(DOCKER_IMAGE)" >/dev/null 2>&1

help:
	@echo "LightVM minimal kernel builder (builds entirely in Docker)"
	@echo ""
	@echo "  make              build kernel and initramfs"
	@echo "  make kernel       build only the kernel"
	@echo "  make initramfs    build only the initramfs"
	@echo "  make menuconfig   edit the kernel configuration"
	@echo "  make reconfigure  reset the kernel configuration"
	@echo "  make test         boot the artifacts in QEMU"
	@echo "  make virtiofsd    download virtiofsd into extra/virtiofsd"
	@echo "  make shell        shell inside the builder container"
	@echo "  make clean        remove the build volume and artifacts"
	@echo "  make distclean    also remove the builder image"
	@echo ""
	@echo "  OUT_DIR=$(OUT_DIR)"
	@echo "  BUILD_VOLUME=$(BUILD_VOLUME)"
	@echo "  KERNEL_VERSION=$(KERNEL_VERSION)  KERNEL_URL=$(KERNEL_URL)"
	@echo "  VIRTIOFSD_REF=$(VIRTIOFSD_REF)  VIRTIOFSD_URL=$(VIRTIOFSD_URL)"
	@echo "  INITRAMFS_OVERLAY=$(INITRAMFS_OVERLAY)"
