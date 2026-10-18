// Package qemu launches virtual machines with QEMU/KVM and exposes their
// VNC Unix sockets.
package qemu

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/store"
)

var (
	// ErrAlreadyRunning is returned when a VM already has a live QEMU process.
	ErrAlreadyRunning = errors.New("vm is already running")
	// ErrNotRunning is returned when a stop is requested for a VM without a
	// live QEMU process.
	ErrNotRunning = errors.New("vm is not running")
	// ErrMonitorUnavailable is returned when QEMU was started without a
	// monitor socket, typically by an older server version.
	ErrMonitorUnavailable = errors.New("QEMU monitor is unavailable for this VM; stop and start it once")
)

const (
	// DefaultBinary is the QEMU system emulator used for x86 VMs.
	DefaultBinary = "qemu-system-x86_64"
	// DefaultRunDir stores per-VM pid files and VNC sockets.
	DefaultRunDir  = "/run/nerds-vms"
	kvmDevice      = "/dev/kvm"
	stopTimeout    = 10 * time.Second
	monitorTimeout = 3 * time.Second
)

// QEMU launches and stops QEMU/KVM processes.
type QEMU struct {
	binary string
	runDir string
}

// New returns a QEMU manager. The runtime directory is created if needed.
func New(binary, runDir string) (*QEMU, error) {
	if strings.TrimSpace(binary) == "" {
		binary = DefaultBinary
	}
	if strings.TrimSpace(runDir) == "" {
		runDir = DefaultRunDir
	}

	abs, err := filepath.Abs(runDir)
	if err != nil {
		return nil, fmt.Errorf("resolve qemu run directory: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create qemu run directory: %w", err)
	}

	return &QEMU{binary: binary, runDir: abs}, nil
}

// Start launches a QEMU/KVM process for the VM.
func (q *QEMU) Start(ctx context.Context, vm store.VM) error {
	disks := vmDisks(vm)
	if len(disks) == 0 {
		return errors.New("vm has no disk")
	}
	if _, err := os.Stat(kvmDevice); err != nil {
		return fmt.Errorf("KVM is not available: %w", err)
	}
	for _, disk := range disks {
		if _, err := os.Stat(disk.Device); err != nil {
			return fmt.Errorf("disk device %s is not available: %w", disk.Device, err)
		}
	}
	if vm.BootMedia != nil {
		if _, err := os.Stat(vm.BootMedia.Path); err != nil {
			return fmt.Errorf("boot media %s is not available: %w", vm.BootMedia.Path, err)
		}
	}
	for _, iface := range vm.Interfaces {
		if _, err := os.Stat(iface.SocketPath); err != nil {
			return fmt.Errorf("vhost socket %s is not available: %w", iface.SocketPath, err)
		}
	}

	socket, pidfile, monitor, err := q.paths(vm.ID)
	if err != nil {
		return err
	}
	if q.Running(vm.ID) {
		return ErrAlreadyRunning
	}

	// Remove leftovers from an unclean shutdown before starting a new process.
	_ = os.Remove(socket)
	_ = os.Remove(pidfile)
	_ = os.Remove(monitor)

	args := buildArgs(vm, socket, pidfile, monitor)
	out, err := exec.CommandContext(ctx, q.binary, args...).CombinedOutput()
	if err != nil {
		q.cleanup(socket, pidfile, monitor)
		message := strings.TrimSpace(string(out))
		if message == "" {
			return fmt.Errorf("start qemu: %w", err)
		}
		return fmt.Errorf("start qemu: %w: %s", err, message)
	}
	if !q.Running(vm.ID) {
		q.cleanup(socket, pidfile, monitor)
		return errors.New("qemu exited before creating its pid file")
	}
	return nil
}

// Stop terminates a running QEMU process. It is a no-op when the VM is not
// running.
func (q *QEMU) Stop(ctx context.Context, vmID string) error {
	socket, pidfile, monitor, err := q.paths(vmID)
	if err != nil {
		return err
	}

	pid, ok := readPID(pidfile)
	if !ok || !processAlive(pid) || !processMatches(pid, vmID) {
		q.cleanup(socket, pidfile, monitor)
		return nil
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop qemu pid %d: %w", pid, err)
	}

	deadline := time.Now().Add(stopTimeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) || !processMatches(pid, vmID) {
			q.cleanup(socket, pidfile, monitor)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill qemu pid %d: %w", pid, err)
	}
	q.cleanup(socket, pidfile, monitor)
	return nil
}

// Running reports whether the VM has a live QEMU process.
func (q *QEMU) Running(vmID string) bool {
	_, pidfile, _, err := q.paths(vmID)
	if err != nil {
		return false
	}
	pid, ok := readPID(pidfile)
	return ok && processAlive(pid) && processMatches(pid, vmID)
}

// VNCSocket returns the Unix socket path for a VM's VNC server.
func (q *QEMU) VNCSocket(vmID string) string {
	socket, _, _, err := q.paths(vmID)
	if err != nil {
		return ""
	}
	return socket
}

// Reset asks QEMU to reset the virtual machine.
func (q *QEMU) Reset(ctx context.Context, vmID string) error {
	return q.monitorCommand(ctx, vmID, "system_reset")
}

// SendCtrlAltDel injects Ctrl+Alt+Del through QEMU's monitor.
func (q *QEMU) SendCtrlAltDel(ctx context.Context, vmID string) error {
	return q.monitorCommand(ctx, vmID, "sendkey ctrl-alt-delete")
}

func (q *QEMU) monitorCommand(ctx context.Context, vmID, command string) error {
	if !q.Running(vmID) {
		return ErrNotRunning
	}
	_, _, monitor, err := q.paths(vmID)
	if err != nil {
		return err
	}

	dialer := net.Dialer{Timeout: monitorTimeout}
	conn, err := dialer.DialContext(ctx, "unix", monitor)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return fmt.Errorf("%w: %v", ErrMonitorUnavailable, err)
		}
		return fmt.Errorf("connect qemu monitor: %w", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(monitorTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)

	if _, err := fmt.Fprintf(conn, "%s\n", command); err != nil {
		return fmt.Errorf("send qemu monitor command: %w", err)
	}

	// HMP replies with the command result and a prompt. We only need to make
	// sure QEMU consumed the command; a read timeout here is not fatal.
	buf := make([]byte, 512)
	_, _ = conn.Read(buf)
	return nil
}

func (q *QEMU) paths(vmID string) (socket, pidfile, monitor string, err error) {
	parsed, err := uuid.Parse(vmID)
	if err != nil {
		return "", "", "", errors.New("invalid vm id")
	}
	base := filepath.Join(q.runDir, parsed.String())
	return base + ".vnc", base + ".pid", base + ".monitor", nil
}

func (q *QEMU) cleanup(socket, pidfile, monitor string) {
	_ = os.Remove(socket)
	_ = os.Remove(pidfile)
	_ = os.Remove(monitor)
}

func readPID(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid < 1 {
		return 0, false
	}
	return pid, true
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func processMatches(pid int, vmID string) bool {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), vmID)
}

func vmDisks(vm store.VM) []store.VMDisk {
	if len(vm.Disks) > 0 {
		return vm.Disks
	}
	if vm.Disk != nil {
		return []store.VMDisk{*vm.Disk}
	}
	return nil
}

func buildArgs(vm store.VM, socket, pidfile, monitor string) []string {
	args := []string{
		"-name", vm.Name,
		"-uuid", vm.ID,
		"-machine", "q35",
		"-accel", "kvm",
		"-cpu", "host",
		"-smp", strconv.Itoa(vm.VCPUs),
		"-m", strconv.Itoa(vm.MemoryMB),
	}

	// vhost-user requires shared guest RAM so VPP can map the virtqueues.
	// Anonymous guest memory leaves the vhost interface link down even though
	// QEMU connected to the socket.
	if len(vm.Interfaces) > 0 {
		args = append(args,
			"-object", fmt.Sprintf("memory-backend-memfd,id=mem,size=%dM,share=on", vm.MemoryMB),
			"-numa", "node,memdev=mem",
		)
	}

	// Expose the QEMU vdagent channel so guests with spice-vdagent (or the
	// Windows equivalent) can exchange clipboard contents with VNC clients.
	// Guests without the agent simply ignore the virtio serial port.
	args = append(args,
		"-chardev", "qemu-vdagent,id=vdagent,clipboard=on,name=vdagent",
		"-device", "virtio-serial-pci",
		"-device", "virtserialport,chardev=vdagent,name=com.redhat.spice.0",
	)

	for _, disk := range vmDisks(vm) {
		args = append(args, "-drive", fmt.Sprintf("file=%s,format=raw,if=virtio,cache=none,aio=native", disk.Device))
	}
	args = append(args,
		"-vnc", "unix:"+socket,
		"-pidfile", pidfile,
		"-monitor", "unix:"+monitor+",server,nowait",
		"-nic", "none",
		"-daemonize",
	)

	for i, iface := range vm.Interfaces {
		id := strconv.Itoa(i)
		device := fmt.Sprintf("virtio-net-pci,netdev=net%s", id)
		if iface.MAC != "" {
			device += ",mac=" + iface.MAC
		}
		args = append(args,
			"-chardev", fmt.Sprintf("socket,id=char%s,path=%s,reconnect=1", id, iface.SocketPath),
			"-netdev", fmt.Sprintf("type=vhost-user,id=net%s,chardev=char%s,vhostforce=on", id, id),
			"-device", device,
		)
	}

	if vm.BootMedia != nil {
		args = append(args,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,readonly=on", vm.BootMedia.Path),
			"-boot", "order=dc",
		)
	} else {
		args = append(args, "-boot", "order=c")
	}

	return args
}
