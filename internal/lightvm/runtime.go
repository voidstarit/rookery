package lightvm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/store"
)

const (
	// RootfsTag is the virtio-fs tag exposed to LightVM guests.
	RootfsTag = "rootfs"

	buildahPullTimeout     = 15 * time.Minute
	buildahCommandTimeout  = 5 * time.Minute
	virtiofsdStartTimeout  = 10 * time.Second
	virtiofsdStopTimeout   = 5 * time.Second
	virtiofsdPollInterval  = 50 * time.Millisecond
	lightVMContainerPrefix = "rookery-lightvm-"
)

// QEMURunner is the QEMU subset used by the LightVM runtime.
type QEMURunner interface {
	StartLightVMWithVirtioFS(ctx context.Context, vm store.LightVM, rootfs qemu.LightVMVirtioFS) error
	StopLightVM(ctx context.Context, id string) error
	RunningLightVM(id string) bool
	LightVMConsoleSocket(id string) string
}

// Runtime orchestrates the ephemeral buildah container, virtiofsd, and QEMU
// process for a LightVM. Each start creates a fresh writable container rootfs.
type Runtime struct {
	qemuRunner      QEMURunner
	buildahBinary   string
	virtiofsdBinary string
	runDir          string
	logger          zerolog.Logger
}

// NewRuntime builds the LightVM runtime. The virtiofsd binary is normally
// resolved next to the rookery executable by the caller.
func NewRuntime(qemuRunner QEMURunner, buildahBinary, virtiofsdBinary, runDir string, logger zerolog.Logger) (*Runtime, error) {
	if qemuRunner == nil {
		return nil, errors.New("lightvm runtime requires a qemu runner")
	}
	buildahBinary = strings.TrimSpace(buildahBinary)
	if buildahBinary == "" {
		buildahBinary = "buildah"
	}
	virtiofsdBinary = strings.TrimSpace(virtiofsdBinary)
	if virtiofsdBinary == "" {
		virtiofsdBinary = DefaultVirtiofsdBinary()
	}
	runDir = strings.TrimSpace(runDir)
	if runDir == "" {
		runDir = qemu.DefaultRunDir
	}
	abs, err := filepath.Abs(runDir)
	if err != nil {
		return nil, fmt.Errorf("resolve LightVM run directory: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create LightVM run directory: %w", err)
	}
	return &Runtime{
		qemuRunner:      qemuRunner,
		buildahBinary:   buildahBinary,
		virtiofsdBinary: virtiofsdBinary,
		runDir:          abs,
		logger:          logger,
	}, nil
}

// DefaultVirtiofsdBinary resolves virtiofsd next to the running rookery binary.
func DefaultVirtiofsdBinary() string {
	executable, err := os.Executable()
	if err != nil {
		return "virtiofsd"
	}
	return filepath.Join(filepath.Dir(executable), "virtiofsd")
}

// StartLightVM pulls the image if needed, creates an ephemeral buildah
// container, starts virtiofsd, and then launches QEMU with the virtio-fs
// rootfs share.
func (r *Runtime) StartLightVM(ctx context.Context, vm store.LightVM) error {
	if strings.TrimSpace(vm.Image) == "" {
		return errors.New("lightvm container image is required")
	}
	if r.qemuRunner.RunningLightVM(vm.ID) {
		return qemu.ErrAlreadyRunning
	}
	// Remove leftovers from a crashed or unclean previous run. QEMU is already
	// confirmed stopped above, so it is safe to release the rootfs.
	r.stopVirtiofsd(ctx, vm.ID)
	r.cleanupContainer(ctx, vm.ID)

	if err := r.ensureImage(ctx, vm.Image); err != nil {
		return err
	}
	container := lightVMContainerName(vm.ID)
	if _, err := r.runCommand(ctx, buildahCommandTimeout, r.buildahBinary,
		"from", "--name", container, vm.Image); err != nil {
		return fmt.Errorf("create buildah container: %w", err)
	}
	rootfs, err := r.runCommand(ctx, buildahCommandTimeout, r.buildahBinary, "mount", container)
	if err != nil {
		r.cleanupContainerBackground(vm.ID)
		return fmt.Errorf("mount image rootfs: %w", err)
	}
	rootfs = strings.TrimSpace(rootfs)
	if rootfs == "" || !filepath.IsAbs(rootfs) {
		r.cleanupContainerBackground(vm.ID)
		return fmt.Errorf("buildah returned an invalid rootfs path %q", rootfs)
	}
	if err := writeLightVMEnvFile(rootfs, vm.Env); err != nil {
		r.cleanupContainerBackground(vm.ID)
		return fmt.Errorf("write LightVM .env: %w", err)
	}
	if err := writeLightVMEntrypointFile(rootfs, vm.ExecutablePath, vm.ExecutableArgs); err != nil {
		r.cleanupContainerBackground(vm.ID)
		return fmt.Errorf("write LightVM entrypoint: %w", err)
	}

	socket, err := r.startVirtiofsd(ctx, vm.ID, rootfs)
	if err != nil {
		r.cleanupContainerBackground(vm.ID)
		return err
	}
	if err := r.qemuRunner.StartLightVMWithVirtioFS(ctx, vm, qemu.LightVMVirtioFS{
		SocketPath: socket,
		Tag:        RootfsTag,
	}); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		r.stopVirtiofsd(cleanupCtx, vm.ID)
		r.cleanupContainer(cleanupCtx, vm.ID)
		return err
	}
	return nil
}

// StopLightVM stops QEMU, virtiofsd, and the ephemeral buildah container.
func (r *Runtime) StopLightVM(ctx context.Context, id string) error {
	qemuErr := r.qemuRunner.StopLightVM(ctx, id)
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	virtioErr := r.stopVirtiofsd(cleanupCtx, id)
	containerErr := r.cleanupContainer(cleanupCtx, id)
	return errors.Join(qemuErr, virtioErr, containerErr)
}

// RunningLightVM reports whether the LightVM QEMU process is alive.
func (r *Runtime) RunningLightVM(id string) bool {
	return r.qemuRunner.RunningLightVM(id)
}

// LightVMConsoleSocket returns the virtio-console Unix socket path.
func (r *Runtime) LightVMConsoleSocket(id string) string {
	return r.qemuRunner.LightVMConsoleSocket(id)
}

// LightVMStatus reports whether the external LightVM tools are available.
func (r *Runtime) LightVMStatus() (ready bool, state, detail string) {
	buildahPath, buildahErr := exec.LookPath(r.buildahBinary)
	if buildahErr != nil {
		return false, "missing", fmt.Sprintf(
			"buildah is required for LightVM image management but %q was not found; install buildah or set -buildah-bin: %v",
			r.buildahBinary, buildahErr)
	}
	virtiofsdPath, virtiofsdErr := exec.LookPath(r.virtiofsdBinary)
	if virtiofsdErr != nil {
		return false, "missing", fmt.Sprintf(
			"virtiofsd is required for LightVM rootfs sharing but %q was not found; place virtiofsd next to the rookery binary or set -virtiofsd-bin: %v",
			r.virtiofsdBinary, virtiofsdErr)
	}
	return true, "ready", fmt.Sprintf("buildah %s and virtiofsd %s are available", buildahPath, virtiofsdPath)
}

func (r *Runtime) ensureImage(ctx context.Context, image string) error {
	if _, err := r.runCommand(ctx, 30*time.Second, r.buildahBinary, "inspect", "--type=image", image); err == nil {
		return nil
	}
	if _, err := r.runCommand(ctx, buildahPullTimeout, r.buildahBinary, "pull", image); err != nil {
		return fmt.Errorf("pull image %s: %w", image, err)
	}
	return nil
}

func (r *Runtime) startVirtiofsd(ctx context.Context, id, rootfs string) (string, error) {
	socket := r.virtiofsdSocket(id)
	pidfile := r.virtiofsdPIDFile(id)
	_ = os.Remove(socket)
	_ = os.Remove(pidfile)

	logPath := filepath.Join(r.runDir, id+".virtiofsd.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", fmt.Errorf("open virtiofsd log: %w", err)
	}
	args := []string{
		"--socket-path=" + socket,
		"--shared-dir=" + rootfs,
		"--cache=auto",
		"--sandbox=none",
		"--announce-submounts",
	}
	cmd := exec.Command(r.virtiofsdBinary, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return "", fmt.Errorf("start virtiofsd: %w", err)
	}
	pid := cmd.Process.Pid
	if err := os.WriteFile(pidfile, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		_ = cmd.Wait()
		_ = logFile.Close()
		return "", fmt.Errorf("write virtiofsd pid file: %w", err)
	}
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
	}()

	if err := waitForVirtiofsSocket(ctx, socket, pid, virtiofsdStartTimeout); err != nil {
		r.stopVirtiofsd(context.Background(), id)
		message := readLogTail(logPath)
		if message != "" {
			return "", fmt.Errorf("virtiofsd did not become ready: %w: %s", err, message)
		}
		return "", fmt.Errorf("virtiofsd did not become ready: %w", err)
	}
	return socket, nil
}

func (r *Runtime) stopVirtiofsd(ctx context.Context, id string) error {
	socket := r.virtiofsdSocket(id)
	pidfile := r.virtiofsdPIDFile(id)
	targets := make([]int, 0, 1)
	seen := make(map[int]struct{}, 1)
	add := func(pid int) {
		if pid < 1 {
			return
		}
		if _, ok := seen[pid]; ok {
			return
		}
		seen[pid] = struct{}{}
		targets = append(targets, pid)
	}
	if pid, ok := readPIDFile(pidfile); ok && processCommandLineContains(pid, socket) {
		add(pid)
	}
	for _, pid := range matchingSocketPIDs(socket) {
		add(pid)
	}
	if len(targets) == 0 {
		_ = os.Remove(pidfile)
		_ = os.Remove(socket)
		return nil
	}
	for _, pid := range targets {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("stop virtiofsd pid %d: %w", pid, err)
		}
	}
	deadline := time.Now().Add(virtiofsdStopTimeout)
	for time.Now().Before(deadline) {
		alive := false
		for _, pid := range targets {
			if processAlive(pid) && processCommandLineContains(pid, socket) {
				alive = true
				break
			}
		}
		if !alive {
			_ = os.Remove(pidfile)
			_ = os.Remove(socket)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(virtiofsdPollInterval):
		}
	}
	for _, pid := range targets {
		if processAlive(pid) && processCommandLineContains(pid, socket) {
			if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				return fmt.Errorf("kill virtiofsd pid %d: %w", pid, err)
			}
		}
	}
	_ = os.Remove(pidfile)
	_ = os.Remove(socket)
	return nil
}

func (r *Runtime) cleanupContainer(ctx context.Context, id string) error {
	container := lightVMContainerName(id)
	if _, err := r.runCommand(ctx, buildahCommandTimeout, r.buildahBinary, "umount", container); err != nil {
		r.logger.Debug().Err(err).Str("container", container).Msg("could not unmount buildah container")
	}
	if _, err := r.runCommand(ctx, buildahCommandTimeout, r.buildahBinary, "rm", container); err != nil {
		r.logger.Debug().Err(err).Str("container", container).Msg("could not remove buildah container")
	}
	return nil
}

func (r *Runtime) cleanupContainerBackground(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_ = r.cleanupContainer(ctx, id)
}

func writeLightVMEnvFile(rootfs string, values []store.LightVMEnvVar) error {
	var content strings.Builder
	seen := make(map[string]struct{}, len(values))
	for _, item := range values {
		name := strings.TrimSpace(item.Name)
		if !validLightVMEnvName(name) {
			return fmt.Errorf("environment variable %q must match [A-Za-z_][A-Za-z0-9_]*", name)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("environment variable %q is duplicated", name)
		}
		seen[name] = struct{}{}
		if strings.ContainsAny(item.Value, "\r\n\x00") {
			return fmt.Errorf("environment variable %q must be a single line without NUL characters", name)
		}
		content.WriteString(name)
		content.WriteByte('=')
		content.WriteString(item.Value)
		content.WriteByte('\n')
	}
	path := filepath.Join(rootfs, ".env")
	if err := os.WriteFile(path, []byte(content.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func validLightVMEnvName(name string) bool {
	for index, char := range name {
		switch {
		case char == '_':
		case char >= 'A' && char <= 'Z':
		case char >= 'a' && char <= 'z':
		case index > 0 && char >= '0' && char <= '9':
		default:
			return false
		}
	}
	return name != ""
}

// writeLightVMEntrypointFile writes an executable /entrypoint script at the
// root of the mounted image. The LightVM init can execute it directly.
func writeLightVMEntrypointFile(rootfs, executable string, args []string) error {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return errors.New("executable path is required")
	}
	if strings.ContainsAny(executable, "\r\n\x00") {
		return errors.New("executable path must be a single line without NUL characters")
	}
	var content strings.Builder
	content.WriteString("#!/bin/sh\n")
	content.WriteString("# Generated by Rookery from the LightVM definition.\n")
	content.WriteString("exec ")
	content.WriteString(shellQuote(executable))
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return errors.New("executable arguments cannot contain NUL characters")
		}
		content.WriteByte(' ')
		content.WriteString(shellQuote(arg))
	}
	content.WriteByte('\n')
	path := filepath.Join(rootfs, "entrypoint")
	if err := os.WriteFile(path, []byte(content.String()), 0o755); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func (r *Runtime) runCommand(ctx context.Context, timeout time.Duration, binary string, args ...string) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			return "", fmt.Errorf("%s %s: %w", filepath.Base(binary), args[0], err)
		}
		return "", fmt.Errorf("%s %s: %w: %s", filepath.Base(binary), args[0], err, message)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (r *Runtime) virtiofsdSocket(id string) string {
	return filepath.Join(r.runDir, id+".virtiofs.sock")
}

func (r *Runtime) virtiofsdPIDFile(id string) string {
	return filepath.Join(r.runDir, id+".virtiofsd.pid")
}

func lightVMContainerName(id string) string {
	return lightVMContainerPrefix + id
}

func waitForVirtiofsSocket(ctx context.Context, socket string, pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socket); err == nil {
			return nil
		}
		if !processAlive(pid) {
			return errors.New("virtiofsd exited before creating its socket")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(virtiofsdPollInterval):
		}
	}
	return fmt.Errorf("timed out waiting for %s", socket)
}

func readPIDFile(path string) (int, bool) {
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

func processCommandLineContains(pid int, value string) bool {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	return bytes.Contains(data, []byte(value))
}

// matchingSocketPIDs finds processes whose command line references socket.
// This catches an orphaned virtiofsd when its pid file was lost.
func matchingSocketPIDs(socket string) []int {
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
		if processCommandLineContains(pid, socket) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func readLogTail(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	if info, err := file.Stat(); err == nil && info.Size() > 4096 {
		_, _ = file.Seek(-4096, io.SeekEnd)
	}
	data, err := io.ReadAll(io.LimitReader(file, 4096))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
