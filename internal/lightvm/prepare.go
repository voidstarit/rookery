package lightvm

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/store"
)

// PreparedLightVM is an ephemeral container/rootfs prepared for one start.
type PreparedLightVM struct {
	LightVMID        string
	AttemptToken     string
	Container        string
	Rootfs           string
	VirtiofsdSocket  string
	VirtiofsdPID     int
	VirtiofsdStarted bool
	QEMUStarted      bool
	Shares           []PreparedVirtioFSShare
}

// PreparedVirtioFSShare is one started host-directory virtiofs share.
type PreparedVirtioFSShare struct {
	Tag      string
	HostPath string
	ReadOnly bool
	Socket   string
	PID      int
	Started  bool
}

// PrepareLightVM pulls the image, creates an attempt-unique container, and
// writes the guest files. It does not start virtiofsd or QEMU.
func (r *Runtime) PrepareLightVM(ctx context.Context, vm store.LightVM) (PreparedLightVM, error) {
	if strings.TrimSpace(vm.Image) == "" {
		return PreparedLightVM{}, errors.New("lightvm container image is required")
	}
	if r.qemuRunner.RunningLightVM(vm.ID) {
		return PreparedLightVM{}, qemu.ErrAlreadyRunning
	}
	// The fixed pre-attempt socket is safe to remove because no QEMU is alive.
	// Attempt-scoped resources are reaped by Stop/Delete.
	_ = r.stopVirtiofsd(ctx, vm.ID)

	token, err := newAttemptToken(vm.LifecycleGeneration)
	if err != nil {
		return PreparedLightVM{}, err
	}
	socket := filepath.Join(r.runDir, fmt.Sprintf("lightvm-%s-%s.virtiofs.sock", vm.ID, token))
	if len(socket)+1 > 107 {
		return PreparedLightVM{}, fmt.Errorf("virtiofs socket path is too long (%d bytes): shorten -run-dir", len(socket))
	}
	container := lightVMContainerPrefix + vm.ID + "-" + token
	prepared := PreparedLightVM{
		LightVMID:       vm.ID,
		AttemptToken:    token,
		Container:       container,
		VirtiofsdSocket: socket,
	}

	if err := r.ensureImage(ctx, vm.Image); err != nil {
		return PreparedLightVM{}, err
	}
	if _, err := r.runCommand(ctx, buildahCommandTimeout, r.buildahBinary,
		"from", "--name", prepared.Container, vm.Image); err != nil {
		return PreparedLightVM{}, fmt.Errorf("create buildah container: %w", err)
	}
	rootfs, err := r.runCommand(ctx, buildahCommandTimeout, r.buildahBinary, "mount", prepared.Container)
	if err != nil {
		r.cleanupContainerBackgroundByName(prepared.Container)
		return PreparedLightVM{}, fmt.Errorf("mount image rootfs: %w", err)
	}
	rootfs = strings.TrimSpace(rootfs)
	if rootfs == "" || !filepath.IsAbs(rootfs) {
		r.cleanupContainerBackgroundByName(prepared.Container)
		return PreparedLightVM{}, fmt.Errorf("buildah returned an invalid rootfs path %q", rootfs)
	}
	prepared.Rootfs = rootfs
	if err := writeLightVMEnvFile(rootfs, vm.Env); err != nil {
		r.cleanupContainerBackgroundByName(prepared.Container)
		return PreparedLightVM{}, fmt.Errorf("write LightVM .env: %w", err)
	}
	if err := writeLightVMEntrypointFile(rootfs, vm.ExecutablePath, vm.ExecutableArgs); err != nil {
		r.cleanupContainerBackgroundByName(prepared.Container)
		return PreparedLightVM{}, fmt.Errorf("write LightVM .entrypoint: %w", err)
	}
	return prepared, nil
}

// StartPreparedLightVM starts virtiofsd and QEMU for a prepared rootfs.
func (r *Runtime) StartPreparedLightVM(ctx context.Context, prepared *PreparedLightVM, vm store.LightVM, plan qemu.StartPlan) error {
	if prepared == nil {
		return errors.New("prepared lightvm handle is required")
	}
	if r.qemuRunner.RunningLightVM(vm.ID) {
		return qemu.ErrAlreadyRunning
	}
	socket, pid, err := r.startVirtiofsdAt(ctx, vm.ID, prepared.AttemptToken, prepared.Rootfs, false)
	if err != nil {
		return err
	}
	prepared.VirtiofsdSocket = socket
	prepared.VirtiofsdPID = pid
	prepared.VirtiofsdStarted = true

	shares := make([]qemu.LightVMVirtioFS, 0, len(vm.VirtioFSShares))
	for index, share := range vm.VirtioFSShares {
		info, err := os.Stat(share.HostPath)
		if err != nil || !info.IsDir() {
			if err == nil {
				err = errors.New("not a directory")
			}
			return fmt.Errorf("virtiofs share %q host path %s: %w", share.Tag, share.HostPath, err)
		}
		shareSocket, sharePID, err := r.startVirtiofsdAt(ctx, vm.ID,
			fmt.Sprintf("%s-s%d", prepared.AttemptToken, index), share.HostPath, share.ReadOnly)
		if err != nil {
			return fmt.Errorf("start virtiofs share %q: %w", share.Tag, err)
		}
		prepared.Shares = append(prepared.Shares, PreparedVirtioFSShare{
			Tag: share.Tag, HostPath: share.HostPath, ReadOnly: share.ReadOnly,
			Socket: shareSocket, PID: sharePID, Started: true,
		})
		shares = append(shares, qemu.LightVMVirtioFS{
			SocketPath: shareSocket,
			Tag:        share.Tag,
			ReadOnly:   share.ReadOnly,
		})
	}

	started, err := r.qemuRunner.StartLightVMWithVirtioFSAndPlan(ctx, vm, qemu.LightVMVirtioFS{
		SocketPath: socket,
		Tag:        RootfsTag,
	}, shares, plan)
	prepared.QEMUStarted = started
	return err
}

// CleanupPreparedLightVM removes exactly this attempt's virtiofsd and container.
func (r *Runtime) CleanupPreparedLightVM(ctx context.Context, prepared PreparedLightVM) error {
	var errs []error
	if prepared.VirtiofsdStarted {
		if err := r.stopVirtiofsdAt(ctx, prepared.VirtiofsdSocket, prepared.VirtiofsdPID); err != nil {
			errs = append(errs, err)
		}
	}
	for _, share := range prepared.Shares {
		if share.Started {
			if err := r.stopVirtiofsdAt(ctx, share.Socket, share.PID); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if prepared.Container != "" {
		if err := r.cleanupContainerByName(ctx, prepared.Container); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// StopQEMULightVM stops only the QEMU process.
func (r *Runtime) StopQEMULightVM(ctx context.Context, id string) error {
	return r.qemuRunner.StopLightVM(ctx, id)
}

// CleanupStoppedLightVM removes all attempt and legacy resources after QEMU is
// confirmed stopped.
func (r *Runtime) CleanupStoppedLightVM(ctx context.Context, id string) error {
	if r.qemuRunner.RunningLightVM(id) {
		return fmt.Errorf("refusing to clean up lightvm %s while QEMU is running", id)
	}
	var errs []error
	sockets := make(map[string]struct{})
	for _, pattern := range []string{
		filepath.Join(r.runDir, "lightvm-"+id+"*.virtiofs.sock"),
		filepath.Join(r.runDir, id+".virtiofs.sock"),
	} {
		matches, _ := filepath.Glob(pattern)
		for _, socket := range matches {
			sockets[socket] = struct{}{}
		}
	}
	for socket := range sockets {
		pid := 0
		if data, err := os.ReadFile(strings.TrimSuffix(socket, ".sock") + "d.pid"); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		if err := r.stopVirtiofsdAt(ctx, socket, pid); err != nil {
			errs = append(errs, err)
		}
	}
	_ = r.stopVirtiofsd(ctx, id)

	containers := map[string]struct{}{
		lightVMContainerName(id): {},
	}
	if output, err := r.runCommand(ctx, 30*time.Second, r.buildahBinary, "containers", "--format", "{{.ContainerName}}"); err != nil {
		errs = append(errs, fmt.Errorf("enumerate buildah containers: %w", err))
	} else {
		for _, line := range strings.Split(output, "\n") {
			name := strings.TrimSpace(line)
			if name == "" {
				continue
			}
			if strings.HasPrefix(name, lightVMContainerPrefix+id+"-") || name == lightVMContainerName(id) {
				containers[name] = struct{}{}
			}
		}
	}
	for container := range containers {
		if err := r.cleanupContainerByName(ctx, container); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// RunState returns the observed QEMU run state for a LightVM.
func (r *Runtime) RunState(ctx context.Context, id string) (string, error) {
	return r.qemuRunner.LightVMRunState(ctx, id)
}

func newAttemptToken(generation int64) (string, error) {
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate lightvm attempt token: %w", err)
	}
	return fmt.Sprintf("%016x%08x", generation, binary.BigEndian.Uint32(random[:])), nil
}

func (r *Runtime) startVirtiofsdAt(ctx context.Context, id, token, rootfs string, readOnly bool) (string, int, error) {
	socket := filepath.Join(r.runDir, fmt.Sprintf("lightvm-%s-%s.virtiofs.sock", id, token))
	if len(socket)+1 > 107 {
		return "", 0, fmt.Errorf("virtiofs socket path is too long (%d bytes): shorten -run-dir", len(socket))
	}
	pidfile := strings.TrimSuffix(socket, ".sock") + "d.pid"
	logPath := strings.TrimSuffix(socket, ".sock") + "d.log"
	_ = os.Remove(socket)
	_ = os.Remove(pidfile)

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", 0, fmt.Errorf("open virtiofsd log: %w", err)
	}
	args := []string{
		"--socket-path=" + socket,
		"--shared-dir=" + rootfs,
		"--cache=auto",
		"--sandbox=none",
		"--announce-submounts",
	}
	if readOnly {
		args = append(args, "--readonly")
	}
	cmd := exec.Command(r.virtiofsdBinary, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return "", 0, fmt.Errorf("start virtiofsd: %w", err)
	}
	pid := cmd.Process.Pid
	if err := os.WriteFile(pidfile, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		_ = cmd.Wait()
		_ = logFile.Close()
		return "", 0, fmt.Errorf("write virtiofsd pid file: %w", err)
	}
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
	}()
	if err := waitForVirtiofsSocket(ctx, socket, pid, virtiofsdStartTimeout); err != nil {
		_ = r.stopVirtiofsdAt(context.Background(), socket, pid)
		message := readLogTail(logPath)
		if message != "" {
			return "", 0, fmt.Errorf("virtiofsd did not become ready: %w: %s", err, message)
		}
		return "", 0, fmt.Errorf("virtiofsd did not become ready: %w", err)
	}
	return socket, pid, nil
}

func (r *Runtime) stopVirtiofsdAt(ctx context.Context, socket string, pid int) error {
	if socket == "" {
		return nil
	}
	targets := make(map[int]struct{})
	if pid > 0 && processAlive(pid) && processCommandLineContains(pid, socket) {
		targets[pid] = struct{}{}
	}
	for _, candidate := range matchingSocketPIDs(socket) {
		targets[candidate] = struct{}{}
	}
	if len(targets) == 0 {
		_ = os.Remove(socket)
		_ = os.Remove(strings.TrimSuffix(socket, ".sock") + "d.pid")
		return nil
	}
	for candidate := range targets {
		if err := syscall.Kill(candidate, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("stop virtiofsd pid %d: %w", candidate, err)
		}
	}
	deadline := time.Now().Add(virtiofsdStopTimeout)
	for time.Now().Before(deadline) {
		alive := false
		for candidate := range targets {
			if processAlive(candidate) && processCommandLineContains(candidate, socket) {
				alive = true
				break
			}
		}
		if !alive {
			_ = os.Remove(socket)
			_ = os.Remove(strings.TrimSuffix(socket, ".sock") + "d.pid")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(virtiofsdPollInterval):
		}
	}
	for candidate := range targets {
		if processAlive(candidate) && processCommandLineContains(candidate, socket) {
			if err := syscall.Kill(candidate, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				return fmt.Errorf("kill virtiofsd pid %d: %w", candidate, err)
			}
		}
	}
	_ = os.Remove(socket)
	_ = os.Remove(strings.TrimSuffix(socket, ".sock") + "d.pid")
	return nil
}

func (r *Runtime) cleanupContainerByName(ctx context.Context, container string) error {
	if container == "" {
		return nil
	}
	_, _ = r.runCommand(ctx, buildahCommandTimeout, r.buildahBinary, "umount", container)
	if _, err := r.runCommand(ctx, buildahCommandTimeout, r.buildahBinary, "rm", container); err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "not known") || strings.Contains(message, "no such") {
			return nil
		}
		return fmt.Errorf("remove buildah container %s: %w", container, err)
	}
	return nil
}

func (r *Runtime) cleanupContainerBackgroundByName(container string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_ = r.cleanupContainerByName(ctx, container)
}
