#!/bin/sh
# Boot-test extra/linux-kernel/initramfs-for-disk-rootfs in QEMU.
#
# The test builds three tiny ext4 images inside the builder container (a
# directly formatted disk, a two-partition disk, and a blank-first-partition
# disk) plus a virtio-fs root, then boots the storage kernel and the disk-root
# initramfs with:
#
#   * root=/dev/vda and init=/sbin/alt-init (explicit device and init=)
#   * root=LABEL=... (findfs resolution without udev)
#   * root=/dev/vda1 that cannot be mounted, then a serial=rootfs fallback
#   * no root=, a virtio-blk disk with serial=rootfs (serial fallback)
#   * no root=, a partitioned disk with serial=rootfs (partition search)
#   * a virtio-fs tag "root" when virtiofsd is available
#
# Required environment:
#   KERNEL, INITRAMFS, WORK_DIR, DOCKER_IMAGE
# Optional:
#   DOCKER (docker), DOCKER_PLATFORM (linux/amd64), QEMU
#   (qemu-system-x86_64), VIRTIOFSD, BOOT_TIMEOUT (180)
set -eu

: "${KERNEL:?KERNEL is required}"
: "${INITRAMFS:?INITRAMFS is required}"
: "${WORK_DIR:?WORK_DIR is required}"
: "${DOCKER_IMAGE:?DOCKER_IMAGE is required}"

DOCKER=${DOCKER:-docker}
DOCKER_PLATFORM=${DOCKER_PLATFORM:-linux/amd64}
QEMU=${QEMU:-qemu-system-x86_64}
VIRTIOFSD=${VIRTIOFSD:-}
BOOT_TIMEOUT=${BOOT_TIMEOUT:-180}

for file in "$KERNEL" "$INITRAMFS"; do
	if [ ! -f "$file" ]; then
		echo "error: missing test input $file" >&2
		exit 1
	fi
done
command -v "$QEMU" >/dev/null || {
	echo "error: $QEMU is required for the disk-root test" >&2
	exit 1
}

mkdir -p "$WORK_DIR"
WORK_DIR=$(cd "$WORK_DIR" && pwd)
ROOT_DIR=$WORK_DIR/root
VIRTIOFS_ROOT_DIR=$WORK_DIR/virtiofs-root

# Build the test root directory and both disk images in the builder container.
# The container runs as the invoking user so QEMU can read the images, and its
# root is read-only; only /test (WORK_DIR) is writable.
cat > "$WORK_DIR/make-image.sh" <<'SCRIPT'
set -eu
ROOT=/test/root
rm -rf "$ROOT" /test/disk.img /test/part.img /test/fallback.img
mkdir -p "$ROOT/bin" "$ROOT/sbin" "$ROOT/dev" "$ROOT/proc" "$ROOT/sys" \
	"$ROOT/run" "$ROOT/tmp" "$ROOT/sys/fs/cgroup"
cp /bin/busybox "$ROOT/bin/busybox"
chmod 0755 "$ROOT/bin/busybox"
ln -sf /bin/busybox "$ROOT/bin/sh"

cat > "$ROOT/init" <<'EOF'
#!/bin/busybox sh
echo "LIGHTVM-DISK-ROOTFS-OK"
echo "lightvm: disk root init pid=$$"
[ -r /proc/cmdline ] && echo "lightvm: proc mounted"
[ -r /sys/kernel ] && echo "lightvm: sys mounted"
[ -c /dev/console ] && echo "lightvm: console present"
[ -d /run ] && echo "lightvm: run present"
if /bin/busybox grep -q ' /dev/pts devpts ' /proc/mounts 2>/dev/null; then
	echo "lightvm: devpts mounted"
fi
if /bin/busybox grep -q ' /dev/shm tmpfs ' /proc/mounts 2>/dev/null; then
	echo "lightvm: devshm mounted"
fi
if /bin/busybox grep -q ' /sys/fs/cgroup cgroup2 ' /proc/mounts 2>/dev/null; then
	echo "lightvm: cgroup2 mounted in root"
fi
if [ -f /marker ]; then
	echo "lightvm: marker $(/bin/busybox cat /marker)"
fi
/bin/busybox poweroff -f
EOF
chmod 0755 "$ROOT/init"

cat > "$ROOT/sbin/alt-init" <<'EOF'
#!/bin/busybox sh
echo "LIGHTVM-DISK-ROOTFS-ALT-OK"
/bin/busybox poweroff -f
EOF
chmod 0755 "$ROOT/sbin/alt-init"
printf '%s\n' disk > "$ROOT/marker"

# A directly formatted disk: root=/dev/vda and the serial fallback both use it.
dd if=/dev/zero of=/test/disk.img bs=1M count=32 status=none
mke2fs -q -F -t ext4 -L rookery-root -d "$ROOT" /test/disk.img

# A partitioned disk whose first partition is blank and whose second partition
# holds the root. An explicit root=/dev/vda1 therefore exists but cannot be
# mounted, which exercises the fallback to the serial search.
dd if=/dev/zero of=/test/fallback.img bs=1M count=32 status=none
printf 'o\nn\np\n1\n2048\n+2047\nn\np\n2\n4096\n\nw\n' |
	busybox fdisk -u /test/fallback.img >/dev/null 2>&1 || true
busybox fdisk -l /test/fallback.img 2>/dev/null | grep -q '/test/fallback.img2' || {
	echo "error: busybox fdisk did not create the fallback partitions" >&2
	exit 1
}
mke2fs -q -F -t ext4 -L rookery-fallback -E offset=2097152 -d "$ROOT" /test/fallback.img

# A partitioned disk: an MBR with one Linux partition at LBA 2048. The
# filesystem is written at the same offset so the guest sees it as /dev/vda1.
dd if=/dev/zero of=/test/part.img bs=1M count=32 status=none
printf 'o\nn\np\n1\n2048\n\nw\n' | busybox fdisk -u /test/part.img >/dev/null 2>&1 || true
busybox fdisk -l /test/part.img 2>/dev/null | grep -q '/test/part.img1' || {
	echo "error: busybox fdisk did not create the test partition" >&2
	exit 1
}
mke2fs -q -F -t ext4 -L rookery-part -E offset=1048576 -d "$ROOT" /test/part.img

# A virtio-fs root for the tag test. A small static init is used here instead
# of BusyBox: some virtiofsd/QEMU combinations mishandle executable mappings of
# large static binaries, and this case is about the initramfs finding the tag
# and handing over, not about the guest's libc or shell.
VROOT=/test/virtiofs-root
rm -rf "$VROOT"
mkdir -p "$VROOT/dev" "$VROOT/proc" "$VROOT/sys/fs/cgroup" "$VROOT/run"
cat > /test/virtiofs-init.c <<'EOF'
#include <stdio.h>
#include <unistd.h>
#include <sys/reboot.h>
int main(void) {
	printf("LIGHTVM-DISK-ROOTFS-OK\n");
	printf("lightvm: virtiofs root init\n");
	if (access("/proc/cmdline", R_OK) == 0)
		printf("lightvm: proc mounted\n");
	if (access("/sys/kernel", R_OK) == 0)
		printf("lightvm: sys mounted\n");
	if (access("/dev/console", F_OK) == 0)
		printf("lightvm: console present\n");
	fflush(stdout);
	sync();
	reboot(RB_POWER_OFF);
	return 0;
}
EOF
gcc -static -O2 -o "$VROOT/init" /test/virtiofs-init.c
chmod 0755 "$VROOT/init"
SCRIPT

"$DOCKER" run --rm --platform "$DOCKER_PLATFORM" \
	--user "$(id -u):$(id -g)" \
	--read-only --cap-drop=ALL --security-opt=no-new-privileges \
	--tmpfs /tmp:rw,exec,nosuid,nodev,mode=1777 \
	-v "$WORK_DIR:/test:rw" \
	"$DOCKER_IMAGE" sh /test/make-image.sh

ACCEL="-accel tcg"
CPU=""
if [ -w /dev/kvm ]; then
	ACCEL="-accel kvm"
	CPU="-cpu host"
fi

fail() {
	echo "disk-root test failed: $*" >&2
	tail -60 "$LAST_LOG" >&2 2>/dev/null || true
	exit 1
}

LAST_LOG=
# boot_case NAME MARKER APPEND [QEMU ARGS...]
boot_case() {
	name=$1
	marker=$2
	append=$3
	shift 3
	LAST_LOG=$WORK_DIR/qemu-$name.log
	rm -f "$LAST_LOG"
	echo "==> $name"
	# shellcheck disable=SC2086
	timeout "$BOOT_TIMEOUT" "$QEMU" \
		-machine q35 -m 256M -smp 2 $ACCEL $CPU \
		-kernel "$KERNEL" \
		-initrd "$INITRAMFS" \
		-append "console=ttyS0,115200 panic=1 $append" \
		-display none -serial stdio -monitor none -no-reboot \
		"$@" >"$LAST_LOG" 2>&1 || true
	grep -q "$marker" "$LAST_LOG" || fail "'$name' never printed $marker"
}

expect() {
	grep -q "$1" "$LAST_LOG" || fail "'$LAST_LOG' does not contain '$1'"
}

# 1. Explicit root= and init=.
boot_case explicit LIGHTVM-DISK-ROOTFS-ALT-OK \
	"root=/dev/vda init=/sbin/alt-init" \
	-drive "file=$WORK_DIR/disk.img,if=virtio,format=raw"
expect 'lightvm: mounted root device /dev/vda'
expect 'lightvm: switch_root /newroot /sbin/alt-init'
if grep -q 'LIGHTVM-DISK-ROOTFS-OK' "$LAST_LOG"; then
	fail "the default /init ran even though init= was set"
fi

# 2. root= by label, resolved by findfs without udev.
boot_case label LIGHTVM-DISK-ROOTFS-OK \
	"root=LABEL=rookery-root" \
	-drive "file=$WORK_DIR/disk.img,if=virtio,format=raw"
expect 'lightvm: mounted root device /dev/vda'
expect 'lightvm: switch_root /newroot /init'
expect 'lightvm: cgroup2 mounted'
expect 'lightvm: cgroup2 mounted in root'
expect 'lightvm: devpts mounted'
expect 'lightvm: devshm mounted'

# 3. An explicit root= that cannot be mounted falls back to the serial search.
#    /dev/vda1 exists but is blank; the ext4 root is /dev/vda2 on the same
#    serial-tagged disk.
boot_case fallback LIGHTVM-DISK-ROOTFS-OK "root=/dev/vda1" \
	-drive "file=$WORK_DIR/fallback.img,if=none,id=test-fallback,format=raw" \
	-device "virtio-blk-pci,drive=test-fallback,serial=rootfs"
expect 'lightvm: could not mount root device /dev/vda1'
expect 'lightvm: falling back to virtio-blk serial root or rootfs'
expect 'lightvm: found block device /dev/vda with serial rootfs'
expect 'lightvm: mounted /dev/vda2'
expect 'lightvm: switch_root /newroot /init'

# 4. Serial fallback on a directly formatted disk.
boot_case serial LIGHTVM-DISK-ROOTFS-OK "" \
	-drive "file=$WORK_DIR/disk.img,if=none,id=test-disk,format=raw" \
	-device "virtio-blk-pci,drive=test-disk,serial=rootfs"
expect 'lightvm: found block device /dev/vda with serial rootfs'
expect 'lightvm: mounted /dev/vda'
expect 'lightvm: switch_root /newroot /init'

# 5. Serial fallback on a partitioned disk: the whole disk cannot be mounted,
#    so the partition must be found. This is the normal distribution image.
boot_case partition LIGHTVM-DISK-ROOTFS-OK "" \
	-drive "file=$WORK_DIR/part.img,if=none,id=test-part,format=raw" \
	-device "virtio-blk-pci,drive=test-part,serial=rootfs"
expect 'lightvm: found block device /dev/vda with serial rootfs'
expect 'lightvm: mounted /dev/vda1'
expect 'lightvm: switch_root /newroot /init'

# 6. A virtio-fs tag "root" is the first choice. A serial-tagged disk is also
#    attached to prove the block search is never reached. This needs the host
#    virtiofsd used by the other LightVM tests; skip it when it is not
#    installed.
if [ -n "$VIRTIOFSD" ] && [ -x "$VIRTIOFSD" ]; then
	SOCKET=$WORK_DIR/virtiofs.sock
	VIRTIOFSD_LOG=$WORK_DIR/virtiofsd.log
	rm -f "$SOCKET" "$VIRTIOFSD_LOG"
	"$VIRTIOFSD" --socket-path="$SOCKET" \
		--shared-dir="$VIRTIOFS_ROOT_DIR" --sandbox none --cache never \
		>"$VIRTIOFSD_LOG" 2>&1 &
	vfsd=$!
	trap 'kill "$vfsd" 2>/dev/null || true' EXIT
	i=0
	while [ "$i" -lt 50 ] && [ ! -S "$SOCKET" ]; do
		sleep 0.2
		i=$((i + 1))
	done
	if [ ! -S "$SOCKET" ]; then
		tail -20 "$VIRTIOFSD_LOG" >&2
		fail "virtiofsd did not create $SOCKET"
	fi
	boot_case virtiofs LIGHTVM-DISK-ROOTFS-OK "" \
		-object memory-backend-memfd,id=mem,size=256M,share=on \
		-numa node,memdev=mem \
		-chardev "socket,id=char0,path=$SOCKET" \
		-device "vhost-user-fs-pci,chardev=char0,tag=root" \
		-drive "file=$WORK_DIR/disk.img,if=none,id=test-virtiofs,format=raw" \
		-device "virtio-blk-pci,drive=test-virtiofs,serial=rootfs"
	expect 'lightvm: mounted virtio-fs tag root'
	expect 'lightvm: switch_root /newroot /init'
	expect 'lightvm: virtiofs root init'
	if grep -q 'lightvm: found block device' "$LAST_LOG"; then
		fail "the block-device search ran even though a virtio-fs root was mounted"
	fi
	kill "$vfsd" 2>/dev/null || true
	wait "$vfsd" 2>/dev/null || true
	trap - EXIT
else
	echo "skipping virtio-fs tag check: set VIRTIOFSD to the virtiofsd binary to enable it"
fi

echo "disk-root initramfs test passed"
