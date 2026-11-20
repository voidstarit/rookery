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
#   * a console session for the chosen init (PID 1's process group is the
#     console's foreground group, which is what lets Ctrl+C reach the guest)
#   * a rookery-config drive extracted to /run/rookery: its env is loaded and
#     startup-cmd becomes PID 1, while an explicit init= still overrides it
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
echo "lightvm: alt-init env=${ROOKERY_CONFIG_ENV:-}"
/bin/busybox poweroff -f
EOF
chmod 0755 "$ROOT/sbin/alt-init"

# The initramfs must hand PID 1 a session whose process group owns the
# console: the tty line discipline signals that group for Ctrl+C. tcgetpgrp(0)
# reads the console's foreground group, so a match proves a console shell can
# interrupt its foreground job. A static binary is used because the test root
# has no libc.
cat > /test/session-init.c <<'EOF'
#include <stdio.h>
#include <unistd.h>
#include <sys/reboot.h>
int main(void) {
	printf("LIGHTVM-CONSOLE-SESSION pid=%d pgrp=%d sid=%d fg=%d\n",
	       (int)getpid(), (int)getpgrp(), (int)getsid(0),
	       (int)tcgetpgrp(0));
	fflush(stdout);
	sync();
	reboot(RB_POWER_OFF);
	return 0;
}
EOF
gcc -static -O2 -o "$ROOT/sbin/session-init" /test/session-init.c
chmod 0755 "$ROOT/sbin/session-init"
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

# A rookery-config tar for the config-drive boot cases. This is the same layout
# Rookery writes for a disk-backed LightVM with env and a startup command.
CONFIG_DIR=$WORK_DIR/config
rm -rf "$CONFIG_DIR"
mkdir -p "$CONFIG_DIR/app"
printf '%s\n' 'ROOKERY_CONFIG_ENV=from-config' > "$CONFIG_DIR/env"
printf '%s\n' 'key: value' > "$CONFIG_DIR/app/config.yaml"
cat > "$CONFIG_DIR/startup-cmd" <<'EOF'
#!/bin/busybox sh
echo "LIGHTVM-CONFIG-STARTUP-OK"
echo "lightvm: config pid=$$"
echo "lightvm: config env=$ROOKERY_CONFIG_ENV"
[ -f /run/rookery/env ] && echo "lightvm: config env file present"
[ -f /run/rookery/startup-cmd ] && echo "lightvm: config startup file present"
[ -f /run/rookery/app/config.yaml ] && echo "lightvm: config user file=$(/bin/busybox cat /run/rookery/app/config.yaml)"
[ -c /dev/console ] && echo "lightvm: config console present"
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

# 6. A rookery-config drive is extracted to /run/rookery, its environment is
#    loaded, and its startup-cmd becomes PID 1. The disk-root /init must not run.
boot_case config LIGHTVM-CONFIG-STARTUP-OK "root=/dev/vda" \
	-drive "file=$WORK_DIR/disk.img,if=none,id=test-root,format=raw" \
	-device "virtio-blk-pci,drive=test-root" \
	-drive "file=$WORK_DIR/rookery-config.tar,if=none,id=rookery-config,format=raw,readonly=on" \
	-device "virtio-blk-pci,drive=rookery-config,serial=rookery-config"
expect 'lightvm: found rookery-config device /dev/vd'
expect 'lightvm: extracted LightVM config to /run/rookery'
expect 'lightvm: loading LightVM environment from /run/rookery/env'
expect 'lightvm: using LightVM startup-cmd as PID 1'
expect 'lightvm: switch_root /newroot /run/rookery/startup-cmd'
expect 'LIGHTVM-CONFIG-STARTUP-OK'
expect 'lightvm: config pid=1'
expect 'lightvm: config env=from-config'
expect 'lightvm: config env file present'
expect 'lightvm: config startup file present'
expect 'lightvm: config user file=key: value'
expect 'lightvm: config console present'
if grep -q 'LIGHTVM-DISK-ROOTFS-OK' "$LAST_LOG"; then
	fail "the disk-root /init ran even though startup-cmd was present"
fi

# 7. An explicit init= overrides the config startup-cmd, but the config drive
#    is still extracted and its environment is loaded for the chosen init.
boot_case config-explicit LIGHTVM-DISK-ROOTFS-ALT-OK \
	"root=/dev/vda init=/sbin/alt-init" \
	-drive "file=$WORK_DIR/disk.img,if=none,id=test-root,format=raw" \
	-device "virtio-blk-pci,drive=test-root" \
	-drive "file=$WORK_DIR/rookery-config.tar,if=none,id=rookery-config,format=raw,readonly=on" \
	-device "virtio-blk-pci,drive=rookery-config,serial=rookery-config"
expect 'lightvm: extracted LightVM config to /run/rookery'
expect 'lightvm: loading LightVM environment from /run/rookery/env'
expect 'lightvm: alt-init env=from-config'
if grep -q 'LIGHTVM-CONFIG-STARTUP-OK' "$LAST_LOG"; then
	fail "startup-cmd ran even though init= was set"
fi

# 8. The handoff puts the chosen init in its own session with the console as
#    its controlling terminal, so a shell started with init=/bin/sh has job
#    control and receives Ctrl+C. PID 1 is not a process group leader or
#    session leader at boot, so without the handoff all of these read 0 or -1.
boot_case console-session LIGHTVM-CONSOLE-SESSION \
	"root=/dev/vda init=/sbin/session-init" \
	-drive "file=$WORK_DIR/disk.img,if=virtio,format=raw"
expect 'LIGHTVM-CONSOLE-SESSION pid=1 pgrp=1 sid=1 fg=1'
expect 'lightvm: switch_root /newroot /sbin/session-init'

# 9. A virtio-fs tag "root" is the first choice. A serial-tagged disk is also
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
