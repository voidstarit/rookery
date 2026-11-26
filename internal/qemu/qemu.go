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

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/cgroup"
	"github.com/voidstarit/rookery/internal/host"
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
	// ErrNotPaused is returned when Resume is requested for a VM that is not paused.
	ErrNotPaused = errors.New("VM is not paused")
)

const (
	// DefaultBinary is the QEMU system emulator used for x86 VMs.
	DefaultBinary = "qemu-system-x86_64"
	// DefaultRunDir stores per-VM pid files and VNC sockets.
	DefaultRunDir  = "/run/rookery"
	stopTimeout    = 10 * time.Second
	monitorTimeout = 3 * time.Second
)

var kvmDevice = "/dev/kvm"

// QEMU launches and stops QEMU/KVM processes.
type QEMU struct {
	binary  string
	runDir  string
	cgroups *cgroup.Manager
}

// StartPlan carries runtime attachments for remote disks.
type StartPlan struct {
	Attachments map[int64]blockdev.Attachment
	// ExplicitVirtio selects explicit virtio-blk-pci devices even for disks
	// without a serial. This keeps block-device order deterministic when an
	// additional device (such as the LightVM config drive) is attached.
	ExplicitVirtio bool
}

// PreparedStarter is implemented by runtimes that accept a StartPlan.
type PreparedStarter interface {
	StartWithPlan(context.Context, store.VM, StartPlan) error
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
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("create qemu run directory: %w", err)
	}
	if err := os.Chmod(abs, 0o700); err != nil {
		return nil, fmt.Errorf("secure qemu run directory: %w", err)
	}

	q := &QEMU{binary: binary, runDir: abs}
	for _, option := range opts {
		option(q)
	}
	q.cleanupStaleSecrets()
	return q, nil
}

// Start launches a QEMU/KVM process for a VM with only local disks.
func (q *QEMU) Start(ctx context.Context, vm store.VM) error {
	return q.StartWithPlan(ctx, vm, StartPlan{})
}

// StartWithPlan launches a QEMU/KVM process for the VM using the supplied
// runtime attachments for remote disks.
func (q *QEMU) StartWithPlan(ctx context.Context, vm store.VM, plan StartPlan) error {
	disks := vmDisks(vm)
	if len(disks) == 0 {
		return errors.New("vm has no disk")
	}
	if _, err := os.Stat(kvmDevice); err != nil {
		return fmt.Errorf("KVM is not available: %w", err)
	}
	if err := validateVMDisks(ctx, disks); err != nil {
		return err
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

	secretPaths, err := q.prepareSecrets(vm, plan)
	if err != nil {
		return err
	}
	defer q.removeSecretFiles(secretPaths)
	defer q.cleanupSecrets(vm.ID)

	// Remove leftovers from an unclean shutdown before starting a new process.
	_ = os.Remove(socket)
	_ = os.Remove(pidfile)
	_ = os.Remove(monitor)
	_ = os.Remove(qmpPathFromSocket(socket))

	placement, err := q.cgroupPlacement(vm)
	if err != nil {
		return err
	}

	args, err := buildArgsWithPlan(vm, socket, pidfile, monitor, plan, secretPaths)
	if err != nil {
		return err
	}
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
		pids := q.processIDs(vm.ID, pidfile, matchingVMPIDs)
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
// process into the leaf class cgroup, or releases it from the Rookery hierarchy
// when host-wide limits are disabled.
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
	pids := q.processIDs(vm.ID, pidfile, matchingVMPIDs)
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
func (q *QEMU) processIDs(vmID, pidfile string, matcher func(string) []int) []int {
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
	if pid, ok := readPID(pidfile); ok && processAlive(pid) && processMatches(pid, vmID, pidfile) {
		add(pid)
	}
	for _, pid := range matcher(vmID) {
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
	defer q.cleanupSecrets(vmID)

	targets := make([]int, 0, 1)
	if pid, ok := readPID(pidfile); ok && processAlive(pid) && processMatches(pid, vmID, pidfile) {
		targets = append(targets, pid)
	} else {
		// The pid file can be lost (for example when the run directory was
		// cleaned while QEMU kept running). Find the process by its -uuid and
		// VM pidfile basename.
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
			if processAlive(pid) && processMatchesBasename(pid, vmID, filepath.Base(pidfile)) {
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
	if ok && processAlive(pid) && processMatches(pid, vmID, pidfile) {
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

// RunState returns the QEMU HMP run state, or an empty string for a stopped VM.
func (q *QEMU) RunState(ctx context.Context, vmID string) (string, error) {
	if !q.Running(vmID) {
		return "", nil
	}
	output, err := q.monitorQuery(ctx, vmID, "info status")
	if err != nil {
		return "", err
	}
	switch {
	case strings.Contains(output, "paused (io-error)"):
		return "paused (io-error)", nil
	case strings.Contains(output, "paused"):
		return "paused", nil
	case strings.Contains(output, "running"):
		return "running", nil
	default:
		return "", nil
	}
}

// Paused reports whether QEMU is paused, including an I/O error pause.
func (q *QEMU) Paused(ctx context.Context, vmID string) (bool, error) {
	state, err := q.RunState(ctx, vmID)
	if err != nil {
		return false, err
	}
	return strings.HasPrefix(state, "paused"), nil
}

// Resume sends cont to a paused VM and verifies the transition.
func (q *QEMU) Resume(ctx context.Context, vmID string) (string, error) {
	state, err := q.RunState(ctx, vmID)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(state, "paused") {
		return "", ErrNotPaused
	}
	if err := q.monitorCommand(ctx, vmID, "cont"); err != nil {
		return "", err
	}
	return q.RunState(ctx, vmID)
}

// monitorCommand sends one HMP command to a running VM's monitor socket,
// treating an unreadable or refused socket as ErrMonitorUnavailable and a
// stopped VM as ErrNotRunning. A read timeout after the write is ignored.
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

// monitorQuery sends an HMP query to a running VM and returns the raw monitor
// output, reporting ErrNotRunning when the VM is stopped.
func (q *QEMU) monitorQuery(ctx context.Context, vmID, command string) (string, error) {
	if !q.Running(vmID) {
		return "", ErrNotRunning
	}
	_, _, monitor, err := q.paths(vmID)
	if err != nil {
		return "", err
	}
	return q.monitorQueryPath(ctx, monitor, command)
}

// monitorQueryPath writes an HMP command to the given monitor socket and reads
// until the 300ms read deadline, returning whatever output arrived as a string.
func (q *QEMU) monitorQueryPath(ctx context.Context, monitor, command string) (string, error) {
	dialer := net.Dialer{Timeout: monitorTimeout}
	conn, err := dialer.DialContext(ctx, "unix", monitor)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return "", fmt.Errorf("%w: %v", ErrMonitorUnavailable, err)
		}
		return "", fmt.Errorf("connect qemu monitor: %w", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(monitorTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)
	if _, err := fmt.Fprintf(conn, "%s\n", command); err != nil {
		return "", fmt.Errorf("send qemu monitor command: %w", err)
	}
	var output strings.Builder
	buf := make([]byte, 1024)
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			output.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return output.String(), nil
}

// paths derives the VNC, pidfile, and monitor socket paths for a VM from its
// UUID, returning an error for malformed ids.
func (q *QEMU) paths(vmID string) (socket, pidfile, monitor string, err error) {
	parsed, err := uuid.Parse(vmID)
	if err != nil {
		return "", "", "", errors.New("invalid vm id")
	}
	base := filepath.Join(q.runDir, parsed.String())
	return base + ".vnc", base + ".pid", base + ".monitor", nil
}

// cleanup removes a VM's VNC, pidfile, monitor, and QMP files, ignoring
// errors because the process may already have removed them.
func (q *QEMU) cleanup(socket, pidfile, monitor string) {
	_ = os.Remove(socket)
	_ = os.Remove(pidfile)
	_ = os.Remove(monitor)
	_ = os.Remove(qmpPathFromSocket(socket))
}

// qmpPathFromSocket returns the QMP socket path paired with a VNC socket by
// replacing its .vnc suffix with .qmp.
func qmpPathFromSocket(socket string) string {
	return strings.TrimSuffix(socket, ".vnc") + ".qmp"
}

// prepareSecrets writes transient credential files for a VM's remote disks
// using the attachments in plan and returns them keyed by disk ID.
func (q *QEMU) prepareSecrets(vm store.VM, plan StartPlan) (map[int64]string, error) {
	return q.prepareDiskSecrets(vm.ID, vmDisks(vm), plan)
}

// prepareDiskSecrets writes 0600 secret files for iSCSI credentials and
// NVMe/TCP PSKs in plan, returning their paths by disk ID; RBD and local disks
// are skipped. On write failure it removes the files already written.
func (q *QEMU) prepareDiskSecrets(secretPrefix string, disks []store.VMDisk, plan StartPlan) (map[int64]string, error) {
	paths := make(map[int64]string)
	for _, disk := range disks {
		attachment, ok := plan.Attachments[disk.ID]
		if !ok {
			continue
		}
		var secret string
		switch attachment.Protocol {
		case blockdev.ProtocolISCSI:
			if attachment.ISCSI == nil || attachment.ISCSI.Credential == "" {
				return nil, fmt.Errorf("disk %d has no iSCSI credential", disk.ID)
			}
			secret = attachment.ISCSI.Credential
		case blockdev.ProtocolNVMeTCP:
			if attachment.NVMeTCP == nil || attachment.NVMeTCP.PSK == "" {
				return nil, fmt.Errorf("disk %d has no NVMe PSK", disk.ID)
			}
			secret = attachment.NVMeTCP.PSK
		case blockdev.ProtocolRBD:
			continue
		default:
			continue
		}
		path := q.secretPath(secretPrefix, disk.ID, attachment.Protocol)
		if err := q.writeSecret(path, secret); err != nil {
			q.removeSecretFiles(paths)
			return nil, err
		}
		paths[disk.ID] = path
	}
	return paths, nil
}

// secretPath builds the run-directory path for one disk's secret file from
// the VM or LightVM prefix, disk ID, and protocol.
func (q *QEMU) secretPath(vmID string, diskID int64, protocol blockdev.Protocol) string {
	return filepath.Join(q.runDir, fmt.Sprintf("%s.disk%d.%s.secret", vmID, diskID, protocol))
}

// writeSecret replaces any existing file with a new 0600 file containing
// secret, removing the file again if writing or closing fails.
func (q *QEMU) writeSecret(path, secret string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove existing disk secret: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create disk secret: %w", err)
	}
	if _, err := file.WriteString(secret); err != nil {
		file.Close()
		os.Remove(path)
		return fmt.Errorf("write disk secret: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return fmt.Errorf("close disk secret: %w", err)
	}
	return nil
}

// removeSecretFiles removes every secret file in paths on a best-effort
// basis, ignoring failures.
func (q *QEMU) removeSecretFiles(paths map[int64]string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

// cleanupSecrets removes all per-disk secret files whose names start with the
// given VM or LightVM prefix, ignoring glob and removal errors.
func (q *QEMU) cleanupSecrets(vmID string) {
	matches, err := filepath.Glob(filepath.Join(q.runDir, vmID+".disk*.secret"))
	if err != nil {
		return
	}
	for _, path := range matches {
		_ = os.Remove(path)
	}
}

// cleanupStaleSecrets deletes .secret files in the run directory that have not
// been modified for an hour, leaving newer files for in-flight starts.
func (q *QEMU) cleanupStaleSecrets() {
	entries, err := os.ReadDir(q.runDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-time.Hour)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".secret") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(q.runDir, entry.Name()))
	}
}

// readPID parses a pidfile, reporting false when it cannot be read, is not an
// integer, or holds a non-positive PID.
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

// processAlive reports whether a process exists, treating EPERM as alive
// because the PID belongs to a process this user cannot signal.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// processArgs returns the full argv of a process from /proc, or nil when the
// command line cannot be read or is empty.
func processArgs(pid int) []string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
}

// processMatches reports whether a process's command line carries both the
// expected -uuid and the expected -pidfile, accepting separate and = forms.
func processMatches(pid int, expectedUUID, expectedPidfile string) bool {
	args := processArgs(pid)
	uuidMatches, pidfileMatches := false, false
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-uuid":
			uuidMatches = args[i+1] == expectedUUID
		case "-pidfile":
			pidfileMatches = filepath.Clean(args[i+1]) == filepath.Clean(expectedPidfile)
		}
	}
	for _, arg := range args {
		if arg == "-uuid="+expectedUUID {
			uuidMatches = true
		}
		if arg == "-pidfile="+expectedPidfile {
			pidfileMatches = true
		}
	}
	return uuidMatches && pidfileMatches
}

// processMatchesBasename reports whether a process carries the expected UUID
// and a pidfile whose basename is one of pidfileBases, accepting separate and
// = forms.
func processMatchesBasename(pid int, expectedUUID string, pidfileBases ...string) bool {
	args := processArgs(pid)
	uuidMatches := false
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-uuid":
			uuidMatches = args[i+1] == expectedUUID
		case "-pidfile":
			base := filepath.Base(args[i+1])
			for _, want := range pidfileBases {
				if base == want {
					return uuidMatches
				}
			}
		}
	}
	for _, arg := range args {
		if arg == "-uuid="+expectedUUID {
			uuidMatches = true
		}
		if strings.HasPrefix(arg, "-pidfile=") {
			base := filepath.Base(strings.TrimPrefix(arg, "-pidfile="))
			for _, want := range pidfileBases {
				if base == want {
					return uuidMatches
				}
			}
		}
	}
	return false
}

// matchingProcessIDs returns PIDs whose command line carries this QEMU UUID.
func matchingProcessIDs(vmID string) []int {
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

// matchingVMPIDs returns PIDs whose command line carries this VM's UUID and
// VM pidfile basename.
func matchingVMPIDs(vmID string) []int {
	return matchingPIDsWithPidfile(vmID, vmID+".pid")
}

// matchingLightVMPIDs returns PIDs whose command line carries this LightVM's
// UUID and a LightVM pidfile basename.
func matchingLightVMPIDs(id string) []int {
	return matchingPIDsWithPidfile(id, "lightvm-"+id+".lightvm.pid", id+".lightvm.pid")
}

// matchingPIDsWithPidfile returns the PIDs whose command line contains the
// given UUID plus a pidfile with any of the given basenames.
func matchingPIDsWithPidfile(id string, bases ...string) []int {
	wanted := make(map[string]struct{}, len(bases))
	for _, base := range bases {
		wanted[base] = struct{}{}
	}
	return cmdlinePIDs(func(args []string) bool {
		uuidMatches, pidfileMatches := false, false
		for i := 0; i+1 < len(args); i++ {
			switch args[i] {
			case "-uuid":
				uuidMatches = args[i+1] == id
			case "-pidfile":
				_, pidfileMatches = wanted[filepath.Base(args[i+1])]
			}
		}
		for _, arg := range args {
			if arg == "-uuid="+id {
				uuidMatches = true
			}
			if strings.HasPrefix(arg, "-pidfile=") {
				_, pidfileMatches = wanted[filepath.Base(strings.TrimPrefix(arg, "-pidfile="))]
			}
		}
		return uuidMatches && pidfileMatches
	})
}

// lightVMQEMUProcesses finds QEMU processes with LightVM-specific evidence:
// a LightVM pidfile basename or a LightVM virtiofs socket. A bare -uuid is not
// enough because a VM may share the UUID.
func lightVMQEMUProcesses(id string) []int {
	seen := make(map[int]struct{})
	pids := make([]int, 0)
	add := func(items []int) {
		for _, pid := range items {
			if _, ok := seen[pid]; ok {
				continue
			}
			seen[pid] = struct{}{}
			pids = append(pids, pid)
		}
	}
	add(matchingLightVMPIDs(id))
	add(cmdlinePIDs(func(args []string) bool {
		return processArgsReferenceLightVMSocket(args, id)
	}))
	return pids
}

// processArgsReferenceLightVMSocket reports whether argv points at the
// LightVM's virtiofs socket, checking -chardev/-fsdev values and bare
// arguments while ignoring virtiofsd helper processes.
func processArgsReferenceLightVMSocket(args []string, id string) bool {
	if len(args) == 0 || strings.Contains(filepath.Base(args[0]), "virtiofsd") {
		return false
	}
	prefixes := []string{"lightvm-" + id, id}
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "-chardev" && args[i] != "-fsdev" {
			continue
		}
		for _, prefix := range prefixes {
			if strings.Contains(args[i+1], prefix) && strings.Contains(args[i+1], ".virtiofs.sock") {
				return true
			}
		}
	}
	for _, arg := range args {
		for _, prefix := range prefixes {
			if strings.Contains(arg, prefix) && strings.Contains(arg, ".virtiofs.sock") {
				return true
			}
		}
	}
	return false
}

// processReferencesLightVMSocket reports whether the process with pid
// references the LightVM's virtiofs socket.
func processReferencesLightVMSocket(pid int, id string) bool {
	return processArgsReferenceLightVMSocket(processArgs(pid), id)
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
		// Read-only images take shared locks and can be opened by many guests;
		// they are not a plausible source of a start failure.
		if disk.ReadOnly {
			continue
		}
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

// cmdlinePIDs scans /proc and returns the PIDs whose argv satisfy match,
// skipping the calling process and entries that cannot be read.
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

// processName returns the short command name from /proc, falling back to
// "unknown" when it cannot be read or is empty.
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

// validateVMDisks rechecks that each disk can be handed to QEMU: host
// passthrough devices must still be attachable, and local LVM-backed devices
// are activated when their /dev node is missing. Kouja, Ceph, and remote
// protocol disks are skipped.
func validateVMDisks(ctx context.Context, disks []store.VMDisk) error {
	for _, disk := range disks {
		protocol := blockdev.Protocol(disk.Protocol).Normalize()
		if disk.Source == store.DiskSourceKouja || disk.Source == store.DiskSourceCeph ||
			(disk.Protocol != "" && protocol != blockdev.ProtocolLocal) {
			continue
		}
		if disk.Source == store.DiskSourceHostDevice {
			if _, err := host.ValidateAttachableBlockDevice(disk.Device); err != nil {
				return fmt.Errorf("host device %s is not available: %w", disk.Device, err)
			}
			continue
		}
		if err := ensureDiskDevice(ctx, disk); err != nil {
			return err
		}
	}
	return nil
}

// ensureDiskDevice verifies that a disk's block device exists. LVM thin
// volumes can remain in metadata while inactive (for example after a reboot),
// leaving no /dev node; activate them before giving QEMU the path.
func ensureDiskDevice(ctx context.Context, disk store.VMDisk) error {
	return ensureDiskDeviceWith(ctx, disk, func(ctx context.Context, vg, thinPool, name string) error {
		return host.NewLVM().ActivateThinVolume(ctx, vg, thinPool, name)
	}, 2*time.Second)
}

type thinVolumeActivator func(ctx context.Context, vg, thinPool, name string) error

// ensureDiskDeviceWith returns nil when the disk's device node exists, or
// activates an inactive LVM thin volume and polls for its node until timeout.
// Otherwise it returns an error wrapping the last stat failure.
func ensureDiskDeviceWith(ctx context.Context, disk store.VMDisk, activate thinVolumeActivator, timeout time.Duration) error {
	_, statErr := os.Stat(disk.Device)
	if statErr == nil {
		return nil
	}
	if disk.Source != store.DiskSourceHostDevice && disk.VG != "" && disk.Name != "" {
		if err := activate(ctx, disk.VG, disk.ThinPool, disk.Name); err != nil {
			return fmt.Errorf("disk device %s is not available: %w", disk.Device, err)
		}
		deadline := time.Now().Add(timeout)
		for {
			_, statErr = os.Stat(disk.Device)
			if statErr == nil {
				return nil
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	return fmt.Errorf("disk device %s is not available: %w", disk.Device, statErr)
}

// vmDisks returns the VM's disk list, falling back to the legacy single Disk
// pointer when Disks is empty, or nil when neither is set.
func vmDisks(vm store.VM) []store.VMDisk {
	if len(vm.Disks) > 0 {
		return vm.Disks
	}
	if vm.Disk != nil {
		return []store.VMDisk{*vm.Disk}
	}
	return nil
}

// buildArgs is the plan-less form of buildArgsWithPlan, returning nil when
// argument construction fails.
func buildArgs(vm store.VM, socket, pidfile, monitor string) []string {
	args, _ := buildArgsWithPlan(vm, socket, pidfile, monitor, StartPlan{}, nil)
	return args
}

// buildArgsWithPlan assembles a VM's QEMU arguments from its resources and the
// prepared start plan: CPU and memory, vdagent, disks, VNC/monitor/QMP
// sockets, NICs, and boot order. Disk construction errors are returned.
func buildArgsWithPlan(vm store.VM, socket, pidfile, monitor string, plan StartPlan, secretPaths map[int64]string) ([]string, error) {
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
		diskArgs, err := buildDiskArgs(disk, plan, secretPaths)
		if err != nil {
			return nil, err
		}
		args = append(args, diskArgs...)
	}
	args = append(args,
		"-vnc", "unix:"+socket,
		"-pidfile", pidfile,
		"-monitor", "unix:"+monitor+",server,nowait",
		"-qmp", "unix:"+qmpPathFromSocket(socket)+",server,nowait",
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

	return args, nil
}

// buildDiskArgs renders the -drive/-device arguments for one disk, including
// read-only and throttle options. It rejects unresolved throttling, invalid
// serials, missing attachments, and unsupported NVMe/TCP or RBD protocols.
func buildDiskArgs(disk store.VMDisk, plan StartPlan, secretPaths map[int64]string) ([]string, error) {
	classID := int64(0)
	if disk.ThrottleClassID != nil {
		classID = *disk.ThrottleClassID
	}
	if disk.ThrottleError != "" {
		return nil, fmt.Errorf("disk %q (id %d, throttle class %d): %s", disk.Name, disk.ID, classID, disk.ThrottleError)
	}
	if classID != 0 && disk.Throttle == nil {
		return nil, fmt.Errorf("disk %q (id %d, throttle class %d): class limits are not resolved", disk.Name, disk.ID, classID)
	}
	serial, err := store.NormalizeDiskSerial(disk.Serial)
	if err != nil {
		return nil, fmt.Errorf("disk %q (id %d): %w", disk.Name, disk.ID, err)
	}
	protocol := blockdev.Protocol(disk.Protocol).Normalize()
	explicit := plan.ExplicitVirtio || serial != ""
	if disk.Source != store.DiskSourceKouja && disk.Source != store.DiskSourceCeph &&
		(disk.Protocol == "" || protocol == blockdev.ProtocolLocal) {
		iface := "virtio"
		if explicit {
			iface = "none"
		}
		drive := fmt.Sprintf("file=%s,format=raw,if=%s,id=rookery-disk-%d,cache=none,aio=native%s%s", disk.Device, iface, disk.ID, diskReadOnlyArg(disk), diskThrottleArgs(disk))
		return virtioDiskArgs(drive, disk.ID, serial, explicit), nil
	}
	attachment, ok := plan.Attachments[disk.ID]
	if !ok {
		return nil, fmt.Errorf("disk %d has no prepared attachment", disk.ID)
	}
	if err := attachment.ValidateComplete(); err != nil {
		return nil, fmt.Errorf("disk %d attachment: %w", disk.ID, err)
	}
	switch attachment.Protocol {
	case blockdev.ProtocolISCSI:
		secretPath := secretPaths[disk.ID]
		if secretPath == "" {
			return nil, fmt.Errorf("disk %d has no iSCSI secret file", disk.ID)
		}
		secretID := fmt.Sprintf("rookery-iscsi-%d", disk.ID)
		iface := "virtio"
		if explicit {
			iface = "none"
		}
		drive := fmt.Sprintf("file.driver=iscsi,file.transport=tcp,file.portal=%s,file.target=%s,file.lun=%d,file.user=%s,file.password-secret=%s,file.initiator-name=%s,file.timeout=30,if=%s,id=rookery-disk-%d,format=raw,cache=none,werror=stop,rerror=stop%s%s",
			attachment.ISCSI.Portal, attachment.ISCSI.Target, attachment.ISCSI.LUN,
			attachment.ISCSI.Username, secretID, attachment.ISCSI.Initiator, iface, disk.ID, diskReadOnlyArg(disk), diskThrottleArgs(disk))
		args := []string{
			"-object", fmt.Sprintf("secret,id=%s,file=%s,format=raw", secretID, secretPath),
		}
		return append(args, virtioDiskArgs(drive, disk.ID, serial, explicit)...), nil
	case blockdev.ProtocolNVMeTCP:
		return nil, fmt.Errorf("NVMe/TCP attachments are not implemented")
	case blockdev.ProtocolRBD:
		return nil, fmt.Errorf("RBD attachments are not implemented")
	default:
		return nil, fmt.Errorf("unknown attachment protocol %q", attachment.Protocol)
	}
}

// virtioDiskArgs keeps the existing -drive if=virtio form for disks without a
// serial unless the caller requires explicit devices. A guest-visible serial
// requires an explicit virtio-blk-pci device because -drive has no serial
// option; callers use if=none in that case.
func virtioDiskArgs(drive string, diskID int64, serial string, explicit bool) []string {
	if !explicit && serial == "" {
		return []string{"-drive", drive}
	}
	device := fmt.Sprintf("virtio-blk-pci,drive=rookery-disk-%d", diskID)
	if serial != "" {
		device += ",serial=" + serial
	}
	return []string{
		"-drive", drive,
		"-device", device,
	}
}

// diskReadOnlyArg returns ",readonly=on" for a read-only disk and an empty
// string otherwise.
func diskReadOnlyArg(disk store.VMDisk) string {
	if disk.ReadOnly {
		return ",readonly=on"
	}
	return ""
}

// diskThrottleArgs returns the disk's QEMU throttle options, or an empty
// string when no throttle limits are set.
func diskThrottleArgs(disk store.VMDisk) string {
	if disk.Throttle == nil {
		return ""
	}
	return disk.Throttle.QEMUOptions()
}
