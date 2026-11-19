#!/bin/sh
# Boot-test extra/linux-kernel/initramfs-for-overlay-rootfs in QEMU.
#
# Builds two lower ext4 layers and two empty upper ext4 filesystems, then boots
# the storage kernel and the overlay-root initramfs with three virtio-blk disks:
#
#   overlay=1, overlay=2   lower layers (2 overrides 1)
#   overlay=3              writable upper layer
#
# The second boot repeats the test with overlay:1/2/3 serials, the form that is
# valid through Rookery's disk-serial validation. The upper image is inspected
# after each boot to prove that a guest write landed in upper/ on the overlay
# upper filesystem. A third boot attaches a rookery-config drive and proves its
# env is loaded and startup-cmd becomes PID 1.
#
# Required environment:
#   KERNEL, INITRAMFS, WORK_DIR, DOCKER_IMAGE
# Optional:
#   DOCKER (docker), DOCKER_PLATFORM (linux/amd64), QEMU
#   (qemu-system-x86_64), BOOT_TIMEOUT (180)
set -eu

: "${KERNEL:?KERNEL is required}"
: "${INITRAMFS:?INITRAMFS is required}"
: "${WORK_DIR:?WORK_DIR is required}"
: "${DOCKER_IMAGE:?DOCKER_IMAGE is required}"

DOCKER=${DOCKER:-docker}
DOCKER_PLATFORM=${DOCKER_PLATFORM:-linux/amd64}
QEMU=${QEMU:-qemu-system-x86_64}
BOOT_TIMEOUT=${BOOT_TIMEOUT:-180}

for file in "$KERNEL" "$INITRAMFS"; do
	if [ ! -f "$file" ]; then
		echo "error: missing test input $file" >&2
		exit 1
	fi
done
command -v "$QEMU" >/dev/null || {
	echo "error: $QEMU is required for the overlay-root test" >&2
	exit 1
}

mkdir -p "$WORK_DIR"
WORK_DIR=$(cd "$WORK_DIR" && pwd)

# Build the layer images in the builder container. The container runs as the
# invoking user so QEMU can read the images, and its root is read-only; only
# /test (WORK_DIR) is writable.
cat > "$WORK_DIR/make-image.sh" <<'SCRIPT'
set -eu
L1=/test/lower1-tree
L2=/test/lower2-tree
rm -rf "$L1" "$L2" /test/lower1.img /test/lower2.img /test/upper.img /test/upper-colon.img
mkdir -p "$L1/bin" "$L1/dev" "$L1/proc" "$L1/sys" "$L1/run" "$L1/sys/fs/cgroup"
mkdir -p "$L2"
cp /bin/busybox "$L1/bin/busybox"
chmod 0755 "$L1/bin/busybox"

cat > "$L1/init" <<'EOF'
#!/bin/busybox sh
echo "LIGHTVM-OVERLAY-ROOTFS-OK"
echo "lightvm: shared=$(/bin/busybox cat /shared)"
echo "lightvm: marker1=$(/bin/busybox cat /marker1)"
echo "lightvm: marker2=$(/bin/busybox cat /marker2)"
if /bin/busybox grep -q ' / overlay ' /proc/mounts 2>/dev/null; then
	echo "lightvm: overlay mount present"
	/bin/busybox grep ' / overlay ' /proc/mounts
fi
# index=off and metacopy=off are the kernel defaults when the corresponding
# features are not configured, so /proc/mounts omits them; the initramfs log
# below proves they were passed. redirect_dir and uuid are non-default and
# visible in the guest mount table.
for opt in redirect_dir=nofollow uuid=null; do
	if /bin/busybox grep -q "$opt" /proc/mounts 2>/dev/null; then
		echo "lightvm: overlay option $opt"
	fi
done
if /bin/busybox grep -q ' /dev/pts devpts ' /proc/mounts 2>/dev/null; then
	echo "lightvm: devpts mounted"
fi
if /bin/busybox grep -q ' /dev/shm tmpfs ' /proc/mounts 2>/dev/null; then
	echo "lightvm: devshm mounted"
fi
# The handoff must leave PID 1 in its own session; a console shell's Ctrl+C
# then has a foreground process group to signal. /proc/1/stat fields 5 and 6
# are the process group and session.
set -- $(/bin/busybox cat /proc/1/stat)
echo "lightvm: console session pgrp=$5 sid=$6"
/bin/busybox touch /written-by-guest
/bin/busybox sync
/bin/busybox poweroff -f
EOF
chmod 0755 "$L1/init"
printf '%s\n' layer-one > "$L1/marker1"
printf '%s\n' from1 > "$L1/shared"
printf '%s\n' layer-two > "$L2/marker2"
printf '%s\n' from2 > "$L2/shared"

dd if=/dev/zero of=/test/lower1.img bs=1M count=32 status=none
mke2fs -q -F -t ext4 -L overlay-lower1 -d "$L1" /test/lower1.img
dd if=/dev/zero of=/test/lower2.img bs=1M count=32 status=none
mke2fs -q -F -t ext4 -L overlay-lower2 -d "$L2" /test/lower2.img
dd if=/dev/zero of=/test/upper.img bs=1M count=32 status=none
mke2fs -q -F -t ext4 -L overlay-upper /test/upper.img
dd if=/dev/zero of=/test/upper-colon.img bs=1M count=32 status=none
mke2fs -q -F -t ext4 -L overlay-upper-colon /test/upper-colon.img
SCRIPT

"$DOCKER" run --rm --platform "$DOCKER_PLATFORM" \
	--user "$(id -u):$(id -g)" \
	--read-only --cap-drop=ALL --security-opt=no-new-privileges \
	-v "$WORK_DIR:/test:rw" \
	"$DOCKER_IMAGE" sh /test/make-image.sh

# A rookery-config tar for the config-drive boot case.
CONFIG_DIR=$WORK_DIR/config
rm -rf "$CONFIG_DIR"
mkdir -p "$CONFIG_DIR/app"
printf '%s\n' 'ROOKERY_CONFIG_ENV=from-config' > "$CONFIG_DIR/env"
printf '%s\n' 'key: value' > "$CONFIG_DIR/app/config.yaml"
cat > "$CONFIG_DIR/startup-cmd" <<'EOF'
#!/bin/busybox sh
echo "LIGHTVM-OVERLAY-CONFIG-OK"
echo "lightvm: config pid=$$"
echo "lightvm: config env=$ROOKERY_CONFIG_ENV"
[ -f /run/rookery/env ] && echo "lightvm: config env file present"
[ -f /run/rookery/startup-cmd ] && echo "lightvm: config startup file present"
[ -f /run/rookery/app/config.yaml ] && echo "lightvm: config user file=$(/bin/busybox cat /run/rookery/app/config.yaml)"
if /bin/busybox grep -q ' / overlay ' /proc/mounts 2>/dev/null; then
	echo "lightvm: config overlay present"
fi
/bin/busybox poweroff -f
EOF
chmod 0755 "$CONFIG_DIR/startup-cmd"
tar -cf "$WORK_DIR/rookery-config.tar" -C "$CONFIG_DIR" env startup-cmd app/config.yaml

ACCEL="-accel tcg"
CPU=""
if [ -w /dev/kvm ]; then
	ACCEL="-accel kvm"
	CPU="-cpu host"
fi

fail() {
	echo "overlay-root test failed: $*" >&2
	tail -80 "$LAST_LOG" >&2 2>/dev/null || true
	exit 1
}

LAST_LOG=
# boot_overlay NAME SEPARATOR UPPER_IMAGE
boot_overlay() {
	name=$1
	sep=$2
	upper=$3
	LAST_LOG=$WORK_DIR/qemu-$name.log
	rm -f "$LAST_LOG"
	echo "==> $name (serial separator '$sep')"
	# shellcheck disable=SC2086
	timeout "$BOOT_TIMEOUT" "$QEMU" \
		-machine q35 -m 256M -smp 2 $ACCEL $CPU \
		-kernel "$KERNEL" \
		-initrd "$INITRAMFS" \
		-append "console=ttyS0,115200 panic=1 overlay-rootfs=true" \
		-drive "file=$WORK_DIR/lower1.img,if=none,id=ovl1,format=raw" \
		-device "virtio-blk-pci,drive=ovl1,serial=overlay${sep}1" \
		-drive "file=$WORK_DIR/lower2.img,if=none,id=ovl2,format=raw" \
		-device "virtio-blk-pci,drive=ovl2,serial=overlay${sep}2" \
		-drive "file=$upper,if=none,id=ovl3,format=raw" \
		-device "virtio-blk-pci,drive=ovl3,serial=overlay${sep}3" \
		-display none -serial stdio -monitor none -no-reboot \
		>"$LAST_LOG" 2>&1 || true
	grep -q 'LIGHTVM-OVERLAY-ROOTFS-OK' "$LAST_LOG" || fail "'$name' did not boot the overlay root"
}

expect() {
	grep -q "$1" "$LAST_LOG" || fail "'$LAST_LOG' does not contain '$1'"
}

check_common() {
	expect 'lightvm: overlay=1: lower at /newroot/.overlay-layers/1'
	expect 'lightvm: overlay=2: lower at /newroot/.overlay-layers/2'
	expect 'lightvm: overlay=3: upper at /newroot/.overlay-layers/3'
	expect 'lightvm: overlay options:'
	expect 'index=off,metacopy=off,redirect_dir=nofollow,uuid=null'
	expect 'lightvm: switch_root /newroot /init'
	expect 'lightvm: shared=from2'
	expect 'lightvm: marker1=layer-one'
	expect 'lightvm: marker2=layer-two'
	expect 'lightvm: overlay mount present'
	expect 'lightvm: overlay option redirect_dir=nofollow'
	expect 'lightvm: overlay option uuid=null'
	expect 'lightvm: devpts mounted'
	expect 'lightvm: devshm mounted'
	expect 'lightvm: console session pgrp=1 sid=1'
}

# A guest write must land in <upper>/written-by-guest on the upper filesystem.
check_upper() {
	upper=$1
	out=$("$DOCKER" run --rm --platform "$DOCKER_PLATFORM" \
		--user "$(id -u):$(id -g)" --read-only \
		-v "$WORK_DIR:/test:ro" "$DOCKER_IMAGE" \
		sh -c "debugfs -R 'ls -l /upper' /test/$(basename "$upper") 2>/dev/null") || true
	case "$out" in
		*written-by-guest*) ;;
		*)
			echo "$out" >&2
			fail "guest write did not land in upper/ on $upper"
			;;
	esac
}

# overlay=1 / overlay=2 / overlay=3
boot_overlay equals '=' "$WORK_DIR/upper.img"
check_common
check_upper "$WORK_DIR/upper.img"

# overlay:1 / overlay:2 / overlay:3 (the form Rookery's serial validation
# currently allows), with a fresh upper layer.
boot_overlay colon ':' "$WORK_DIR/upper-colon.img"
check_common
check_upper "$WORK_DIR/upper-colon.img"

# A rookery-config drive is extracted into /run/rookery and its startup-cmd
# becomes PID 1 instead of the overlay root's /init.
LAST_LOG=$WORK_DIR/qemu-config.log
rm -f "$LAST_LOG"
echo "==> config"
timeout "$BOOT_TIMEOUT" "$QEMU" \
	-machine q35 -m 256M -smp 2 $ACCEL $CPU \
	-kernel "$KERNEL" \
	-initrd "$INITRAMFS" \
	-append "console=ttyS0,115200 panic=1 overlay-rootfs=true" \
	-drive "file=$WORK_DIR/lower1.img,if=none,id=ovl1,format=raw" \
	-device "virtio-blk-pci,drive=ovl1,serial=overlay=1" \
	-drive "file=$WORK_DIR/lower2.img,if=none,id=ovl2,format=raw" \
	-device "virtio-blk-pci,drive=ovl2,serial=overlay=2" \
	-drive "file=$WORK_DIR/upper.img,if=none,id=ovl3,format=raw" \
	-device "virtio-blk-pci,drive=ovl3,serial=overlay=3" \
	-drive "file=$WORK_DIR/rookery-config.tar,if=none,id=rookery-config,format=raw,readonly=on" \
	-device "virtio-blk-pci,drive=rookery-config,serial=rookery-config" \
	-display none -serial stdio -monitor none -no-reboot \
	>"$LAST_LOG" 2>&1 || true
grep -q 'LIGHTVM-OVERLAY-CONFIG-OK' "$LAST_LOG" || fail "config startup-cmd did not run"
expect 'lightvm: found rookery-config device /dev/vd'
expect 'lightvm: extracted LightVM config to /run/rookery'
expect 'lightvm: loading LightVM environment from /run/rookery/env'
expect 'lightvm: using LightVM startup-cmd as PID 1'
expect 'lightvm: switch_root /newroot /run/rookery/startup-cmd'
expect 'lightvm: config pid=1'
expect 'lightvm: config env=from-config'
expect 'lightvm: config env file present'
expect 'lightvm: config startup file present'
expect 'lightvm: config user file=key: value'
expect 'lightvm: config overlay present'
if grep -q 'LIGHTVM-OVERLAY-ROOTFS-OK' "$LAST_LOG"; then
	fail "the overlay root /init ran even though startup-cmd was present"
fi

echo "overlay-root initramfs test passed"
