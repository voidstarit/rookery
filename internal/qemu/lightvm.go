package qemu

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/cgroup"
	"github.com/voidstarit/rookery/internal/store"
)

// LightVMVirtioFS describes a virtio-fs share attached to a LightVM.
type LightVMVirtioFS struct {
	SocketPath string
	Tag        string
}

// StartLightVM launches a QEMU process that boots the LightVM kernel and
// initramfs. No block device is attached; the virtio console is exposed as a
// Unix socket under the QEMU run directory.
func (q *QEMU) StartLightVM(ctx context.Context, vm store.LightVM) error {
	return q.StartLightVMWithVirtioFS(ctx, vm, LightVMVirtioFS{})
}

// StartLightVMWithVirtioFS launches a LightVM and attaches an optional
// vhost-user-fs device backed by a virtiofsd socket.
func (q *QEMU) StartLightVMWithVirtioFS(ctx context.Context, vm store.LightVM, rootfs LightVMVirtioFS) error {
	if strings.TrimSpace(vm.KernelPath) == "" || strings.TrimSpace(vm.InitramfsPath) == "" {
		return errors.New("lightvm is missing its kernel or initramfs path")
	}
	if _, err := os.Stat(kvmDevice); err != nil {
		return fmt.Errorf("KVM is not available: %w", err)
	}
	if _, err := os.Stat(vm.KernelPath); err != nil {
		return fmt.Errorf("kernel %s is not available: %w", vm.KernelPath, err)
	}
	if _, err := os.Stat(vm.InitramfsPath); err != nil {
		return fmt.Errorf("initramfs %s is not available: %w", vm.InitramfsPath, err)
	}
	if rootfs.SocketPath != "" {
		if _, err := os.Stat(rootfs.SocketPath); err != nil {
			return fmt.Errorf("virtiofs socket %s is not available: %w", rootfs.SocketPath, err)
		}
	}
	for _, iface := range vm.Interfaces {
		if _, err := os.Stat(iface.SocketPath); err != nil {
			return fmt.Errorf("vhost socket %s is not available: %w", iface.SocketPath, err)
		}
	}

	_, pidfile, monitor, err := q.lightVMPaths(vm.ID)
	if err != nil {
		return err
	}
	if q.RunningLightVM(vm.ID) {
		return ErrAlreadyRunning
	}
	q.cleanupLightVM(vm.ID)

	placement, err := q.lightVMCgroupPlacement(vm)
	if err != nil {
		return err
	}
	args := buildLightVMArgs(vm, q.lightVMConsoleArgs(vm), pidfile, monitor, rootfs)
	out, err := q.run(ctx, args, placement)
	if err != nil {
		q.cleanupLightVM(vm.ID)
		message := strings.TrimSpace(string(out))
		if message == "" {
			return fmt.Errorf("start lightvm qemu: %w", err)
		}
		return fmt.Errorf("start lightvm qemu: %w: %s", err, message)
	}
	if !q.RunningLightVM(vm.ID) {
		q.cleanupLightVM(vm.ID)
		return errors.New("lightvm qemu exited before creating its pid file")
	}
	if placement.Enabled && q.cgroups != nil {
		pids := q.processIDs(vm.ID, pidfile)
		if len(pids) == 0 {
			_ = q.StopLightVM(context.Background(), vm.ID)
			return errors.New("could not find LightVM QEMU pid for cgroup placement")
		}
		for _, pid := range pids {
			if err := q.cgroups.Assign(placement, pid); err != nil {
				_ = q.StopLightVM(context.Background(), vm.ID)
				return fmt.Errorf("place LightVM QEMU in cgroup: %w", err)
			}
		}
	}
	return nil
}

// StopLightVM terminates a running LightVM QEMU process.
func (q *QEMU) StopLightVM(ctx context.Context, id string) error {
	_, pidfile, _, err := q.lightVMPaths(id)
	if err != nil {
		return err
	}

	targets := make([]int, 0, 1)
	if pid, ok := readPID(pidfile); ok && processAlive(pid) && processMatches(pid, id) {
		targets = append(targets, pid)
	} else {
		targets = matchingVMPIDs(id)
	}
	if len(targets) == 0 {
		q.cleanupLightVM(id)
		return nil
	}
	for _, pid := range targets {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("stop lightvm qemu pid %d: %w", pid, err)
		}
	}
	deadline := time.Now().Add(stopTimeout)
	for time.Now().Before(deadline) {
		alive := false
		for _, pid := range targets {
			if processAlive(pid) && processMatches(pid, id) {
				alive = true
				break
			}
		}
		if !alive {
			q.cleanupLightVM(id)
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
			return fmt.Errorf("kill lightvm qemu pid %d: %w", pid, err)
		}
	}
	q.cleanupLightVM(id)
	return nil
}

// RunningLightVM reports whether the LightVM has a live QEMU process.
func (q *QEMU) RunningLightVM(id string) bool {
	_, pidfile, _, err := q.lightVMPaths(id)
	if err != nil {
		return false
	}
	if pid, ok := readPID(pidfile); ok && processAlive(pid) && processMatches(pid, id) {
		return true
	}
	return len(matchingVMPIDs(id)) > 0
}

// LightVMConsoleSocket returns the virtio console Unix socket for a running
// LightVM console. Console ID 0 addresses the legacy/default console.
func (q *QEMU) LightVMConsoleSocket(id string, consoleID int64) string {
	if consoleID > 0 {
		base, err := q.lightVMBase(id)
		if err != nil {
			return ""
		}
		return fmt.Sprintf("%s.console.%d", base, consoleID)
	}
	console, _, _, err := q.lightVMPaths(id)
	if err != nil {
		return ""
	}
	return console
}

func (q *QEMU) lightVMCgroupPlacement(vm store.LightVM) (cgroup.Placement, error) {
	if q.cgroups == nil {
		return cgroup.Placement{}, nil
	}
	placement, err := q.cgroups.Place(vm.CgroupClass)
	if err != nil {
		return cgroup.Placement{}, fmt.Errorf("prepare cgroup for LightVM: %w", err)
	}
	return placement, nil
}

func (q *QEMU) lightVMPaths(id string) (console, pidfile, monitor string, err error) {
	base, err := q.lightVMBase(id)
	if err != nil {
		return "", "", "", err
	}
	return base + ".console", base + ".lightvm.pid", base + ".lightvm.monitor", nil
}

func (q *QEMU) lightVMBase(id string) (string, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", errors.New("invalid lightvm id")
	}
	return filepath.Join(q.runDir, parsed.String()), nil
}

func (q *QEMU) cleanupLightVM(id string) {
	base, err := q.lightVMBase(id)
	if err != nil {
		return
	}
	_ = os.Remove(base + ".console")
	_ = os.Remove(base + ".lightvm.pid")
	_ = os.Remove(base + ".lightvm.monitor")
	matches, _ := filepath.Glob(base + ".console.*")
	for _, match := range matches {
		_ = os.Remove(match)
	}
}

type lightVMConsoleArg struct {
	SocketPath string
	PortName   string
}

func (q *QEMU) lightVMConsoleArgs(vm store.LightVM) []lightVMConsoleArg {
	consoles := vm.Consoles
	if len(consoles) == 0 {
		consoles = []store.LightVMConsole{{Name: "console", PortName: "org.example.console"}}
	}
	result := make([]lightVMConsoleArg, 0, len(consoles))
	for index, console := range consoles {
		portName := strings.TrimSpace(console.PortName)
		if portName == "" {
			portName = "org.example.console"
			if index > 0 {
				portName = fmt.Sprintf("org.example.console.%d", index+1)
			}
		}
		result = append(result, lightVMConsoleArg{
			SocketPath: q.LightVMConsoleSocket(vm.ID, console.ID),
			PortName:   portName,
		})
	}
	return result
}

const appHostnameParam = "APP_HOSTNAME="

func buildLightVMArgs(vm store.LightVM, consoles []lightVMConsoleArg, pidfile, monitor string, rootfs LightVMVirtioFS) []string {
	args := []string{
		"-name", vm.Name,
		"-uuid", vm.ID,
		"-accel", "kvm",
		"-cpu", "host",
		"-smp", strconv.Itoa(vm.VCPUs),
		"-m", strconv.Itoa(vm.MemoryMB),
		"-kernel", vm.KernelPath,
		"-initrd", vm.InitramfsPath,
	}
	if appendValue := lightVMAppend(vm); appendValue != "" {
		args = append(args, "-append", appendValue)
	}
	if len(vm.Interfaces) > 0 || rootfs.SocketPath != "" {
		args = append(args,
			"-object", fmt.Sprintf("memory-backend-memfd,id=mem,size=%dM,share=on", vm.MemoryMB),
			"-numa", "node,memdev=mem",
		)
	}
	args = append(args,
		"-display", "none",
		"-device", "virtio-serial-pci,max_ports=32",
	)
	for index, console := range consoles {
		portName := strings.TrimSpace(console.PortName)
		if portName == "" {
			portName = "org.example.console"
		}
		id := "console" + strconv.Itoa(index)
		args = append(args,
			"-chardev", fmt.Sprintf("socket,id=%s,path=%s,server=on,wait=off", id, console.SocketPath),
			"-device", fmt.Sprintf("virtconsole,chardev=%s,name=%s", id, portName),
		)
	}
	args = append(args,
		"-pidfile", pidfile,
		"-monitor", "unix:"+monitor+",server,nowait",
		"-nic", "none",
		"-daemonize",
	)
	if rootfs.SocketPath != "" {
		tag := strings.TrimSpace(rootfs.Tag)
		if tag == "" {
			tag = "rootfs"
		}
		args = append(args,
			"-chardev", fmt.Sprintf("socket,id=fs0,path=%s", rootfs.SocketPath),
			"-device", "vhost-user-fs-pci,chardev=fs0,tag="+tag,
		)
	}
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
	return args
}

// lightVMAppend returns the kernel command line with APP_HOSTNAME set to the
// LightVM name unless the caller already supplied one. The guest DHCP client
// uses it as the DHCP hostname/FQDN option so Rookery's DNS can map the lease.
func lightVMAppend(vm store.LightVM) string {
	appendValue := strings.TrimSpace(vm.Append)
	for _, field := range strings.Fields(appendValue) {
		if strings.HasPrefix(field, appHostnameParam) {
			return appendValue
		}
	}
	hostname := lightVMHostname(vm.Name)
	if hostname == "" {
		return appendValue
	}
	if appendValue == "" {
		return appHostnameParam + hostname
	}
	return appendValue + " " + appHostnameParam + hostname
}

// lightVMHostname converts an inventory name into a DHCP-safe hostname.
// Names can contain spaces and other characters that are not valid in a
// kernel command-line parameter or DHCP option.
func lightVMHostname(name string) string {
	var builder strings.Builder
	lastDash := false
	for _, char := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			builder.WriteRune(char)
			lastDash = false
		default:
			if builder.Len() > 0 && !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
		if builder.Len() >= 63 {
			break
		}
	}
	hostname := strings.Trim(builder.String(), "-")
	if hostname == "" {
		return "lightvm"
	}
	return hostname
}
