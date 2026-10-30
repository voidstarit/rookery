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

	"github.com/voidstarit/rookery/internal/cgroup"
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
	DefaultRunDir  = "/run/rookery"
	kvmDevice      = "/dev/kvm"
	stopTimeout    = 10 * time.Second
	monitorTimeout = 3 * time.Second
)

// QEMU launches and stops QEMU/KVM processes.
type QEMU struct {
	binary  string
	runDir  string
	cgroups *cgroup.Manager
}

// Option customizes the QEMU manager.
type Option func(*QEMU)

// WithCgroupManager places every QEMU process in the cgroup hierarchy managed
// by manager. When manager is nil or host limits are disabled, QEMU starts
// normally.
func WithCgroupManager(manager *cgroup.Manager) Option {
	return func(q *QEMU) {
		q.cgroups = manager
	}
}

// New returns a QEMU manager. The runtime directory is created if needed.
func New(binary, runDir string, opts ...Option) (*QEMU, error) {
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

	q := &QEMU{binary: binary, runDir: abs}
	for _, option := range opts {
		option(q)
	}
	return q, nil
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

	placement, err := q.cgroupPlacement(vm)
	if err != nil {
		return err
	}

	args := buildArgs(vm, socket, pidfile, monitor)
	out, err := q.run(ctx, args, placement)
	if err != nil {
		q.cleanup(socket, pidfile, monitor)
		message := strings.TrimSpace(string(out))
		note := ""
		if holders := imageLockHolders(disks); len(holders) > 0 {
			refs := make([]string, 0, len(holders))
			for _, holder := range holders {
				refs = append(refs, fmt.Sprintf("pid %d (%s)", holder.pid, holder.name))
			}
			note = "; image is in use by " + strings.Join(refs, ", ")
		}
		if message == "" {
			return fmt.Errorf("start qemu: %w%s", err, note)
		}
		return fmt.Errorf("start qemu: %w: %s%s", err, message, note)
	}
	if !q.Running(vm.ID) {
		q.cleanup(socket, pidfile, monitor)
		return errors.New("qemu exited before creating its pid file")
	}
	if placement.Enabled {
		pids := q.processIDs(vm.ID, pidfile)
		if len(pids) == 0 {
			_ = q.Stop(context.Background(), vm.ID)
			return errors.New("could not find QEMU pid for cgroup placement")
		}
		for _, pid := range pids {
			if err := q.cgroups.Assign(placement, pid); err != nil {
				_ = q.Stop(context.Background(), vm.ID)
				return fmt.Errorf("place QEMU in cgroup: %w", err)
			}
		}
	}
	return nil
}

// ApplyCgroup applies a VM's class to a running QEMU process. It moves the
// process into the class members cgroup, or releases it from the Rookery
// hierarchy when host-wide limits are disabled.
func (q *QEMU) ApplyCgroup(ctx context.Context, vm store.VM) error {
	if q.cgroups == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	placement, err := q.cgroups.Place(vm.CgroupClass)
	if err != nil {
		return fmt.Errorf("apply VM cgroup: %w", err)
	}
	_, pidfile, _, err := q.paths(vm.ID)
	if err != nil {
		return err
	}
	pids := q.processIDs(vm.ID, pidfile)
	if !placement.Enabled {
		for _, pid := range pids {
			if err := q.cgroups.Release(pid); err != nil {
				return fmt.Errorf("remove QEMU from cgroup: %w", err)
			}
		}
		return nil
	}
	for _, pid := range pids {
		if err := q.cgroups.Assign(placement, pid); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				continue
			}
			return fmt.Errorf("move QEMU to cgroup: %w", err)
		}
	}
	return nil
}

// cgroupPlacement returns the cgroup for a VM, if cgroup limits are enabled.
func (q *QEMU) cgroupPlacement(vm store.VM) (cgroup.Placement, error) {
	if q.cgroups == nil {
		return cgroup.Placement{}, nil
	}
	placement, err := q.cgroups.Place(vm.CgroupClass)
	if err != nil {
		return cgroup.Placement{}, fmt.Errorf("prepare cgroup for VM: %w", err)
	}
	return placement, nil
}

// run executes QEMU, using clone3's cgroup FD support when available so the
// daemonized child is constrained from its very first instruction. Kernels
// without CLONE_INTO_CGROUP fall back to moving the process into the cgroup as
// soon as its pid file appears.
func (q *QEMU) run(ctx context.Context, args []string, placement cgroup.Placement) ([]byte, error) {
	command := func() *exec.Cmd {
		return exec.CommandContext(ctx, q.binary, args...)
	}
	if !placement.Enabled || q.cgroups == nil {
		return command().CombinedOutput()
	}

	dir, err := os.Open(placement.Path)
	if err != nil {
		return nil, fmt.Errorf("open VM cgroup %s: %w", placement.Path, err)
	}
	defer dir.Close()
	cmd := command()
	cmd.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: int(dir.Fd())}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return out, nil
	}
	if !errors.Is(err, syscall.ENOSYS) && !errors.Is(err, syscall.EOPNOTSUPP) && !errors.Is(err, syscall.EINVAL) {
		return out, err
	}
	return command().CombinedOutput()
}

// processIDs returns every live QEMU pid for a VM, preferring the pid file and
// falling back to /proc for a process that lost its pid file.
func (q *QEMU) processIDs(vmID, pidfile string) []int {
	seen := make(map[int]struct{}, 1)
	pids := make([]int, 0, 1)
	add := func(pid int) {
		if pid < 1 {
			return
		}
		if _, ok := seen[pid]; ok {
			return
		}
		seen[pid] = struct{}{}
		pids = append(pids, pid)
	}
	if pid, ok := readPID(pidfile); ok && processAlive(pid) && processMatches(pid, vmID) {
		add(pid)
	}
	for _, pid := range matchingVMPIDs(vmID) {
		add(pid)
	}
	return pids
}

// Stop terminates a running QEMU process. It is a no-op when the VM is not
// running.
func (q *QEMU) Stop(ctx context.Context, vmID string) error {
	socket, pidfile, monitor, err := q.paths(vmID)
	if err != nil {
		return err
	}

	targets := make([]int, 0, 1)
	if pid, ok := readPID(pidfile); ok && processAlive(pid) && processMatches(pid, vmID) {
		targets = append(targets, pid)
	} else {
		// The pid file can be lost (for example when the run directory was
		// cleaned while QEMU kept running). Find the process by its -uuid.
		targets = matchingVMPIDs(vmID)
	}
	if len(targets) == 0 {
		q.cleanup(socket, pidfile, monitor)
		return nil
	}

	for _, pid := range targets {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("stop qemu pid %d: %w", pid, err)
		}
	}

	deadline := time.Now().Add(stopTimeout)
	for time.Now().Before(deadline) {
		alive := false
		for _, pid := range targets {
			if processAlive(pid) && processMatches(pid, vmID) {
				alive = true
				break
			}
		}
		if !alive {
			q.cleanup(socket, pidfile, monitor)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	for _, pid := range targets {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("kill qemu pid %d: %w", pid, err)
		}
	}
	q.cleanup(socket, pidfile, monitor)
	return nil
}

// Running reports whether the VM has a live QEMU process. The pid file is the
// fast path; when it is missing or stale, a QEMU process started with
// "-uuid <vmID>" is treated as running so an API restart or a cleaned run
// directory cannot launch a second QEMU against the same disk.
func (q *QEMU) Running(vmID string) bool {
	_, pidfile, _, err := q.paths(vmID)
	if err != nil {
		return false
	}
	pid, ok := readPID(pidfile)
	if ok && processAlive(pid) && processMatches(pid, vmID) {
		return true
	}
	return len(matchingVMPIDs(vmID)) > 0
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

// matchingVMPIDs returns PIDs whose command line carries this VM's QEMU UUID.
func matchingVMPIDs(vmID string) []int {
	return cmdlinePIDs(func(args []string) bool {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-uuid" && args[i+1] == vmID {
				return true
			}
		}
		for _, arg := range args {
			if arg == "-uuid="+vmID {
				return true
			}
		}
		return false
	})
}

type processRef struct {
	pid  int
	name string
}

// imageLockHolders finds live processes whose command line references one of
// the VM disks. It is used to explain QEMU "Failed to get write lock" errors.
func imageLockHolders(disks []store.VMDisk) []processRef {
	paths := make(map[string]struct{}, len(disks))
	for _, disk := range disks {
		paths[disk.Device] = struct{}{}
	}
	if len(paths) == 0 {
		return nil
	}
	holders := make([]processRef, 0, 1)
	for _, pid := range cmdlinePIDs(func(args []string) bool {
		for _, arg := range args {
			if _, ok := paths[arg]; ok {
				return true
			}
		}
		return false
	}) {
		holders = append(holders, processRef{pid: pid, name: processName(pid)})
	}
	return holders
}

func cmdlinePIDs(match func([]string) bool) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	pids := make([]int, 0, 1)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || len(data) == 0 {
			continue
		}
		args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
		if match(args) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func processName(pid int) string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
	if err != nil {
		return "unknown"
	}
	name := strings.TrimSpace(string(data))
	if name == "" {
		return "unknown"
	}
	return name
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
