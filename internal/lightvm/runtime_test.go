package lightvm_test

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/lightvm"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeLightVMQEMU struct {
	running  bool
	fs       qemu.LightVMVirtioFS
	startErr error
}

func (f *fakeLightVMQEMU) StartLightVMWithVirtioFS(_ context.Context, _ store.LightVM, rootfs qemu.LightVMVirtioFS) error {
	_, err := f.StartLightVMWithVirtioFSAndPlan(context.Background(), store.LightVM{}, rootfs, qemu.StartPlan{})
	return err
}
func (f *fakeLightVMQEMU) StartLightVMWithVirtioFSAndPlan(_ context.Context, _ store.LightVM, rootfs qemu.LightVMVirtioFS, _ qemu.StartPlan) (bool, error) {
	if f.startErr != nil {
		return false, f.startErr
	}
	f.running = true
	f.fs = rootfs
	return true, nil
}
func (f *fakeLightVMQEMU) LightVMRunState(context.Context, string) (string, error) {
	if f.running {
		return "running", nil
	}
	return "", nil
}
func (f *fakeLightVMQEMU) StopLightVM(context.Context, string) error {
	f.running = false
	return nil
}
func (f *fakeLightVMQEMU) RunningLightVM(string) bool { return f.running }
func (f *fakeLightVMQEMU) LightVMConsoleSocket(id string, _ int64) string {
	return "/tmp/" + id + ".console"
}

// TestFakeVirtiofsdHelper is used as a stand-in virtiofsd process. It creates
// the requested Unix socket and blocks until killed.
func TestFakeVirtiofsdHelper(t *testing.T) {
	if os.Getenv("GO_WANT_FAKE_VIRTIOFSD") != "1" {
		return
	}
	socket := ""
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--socket-path=") {
			socket = strings.TrimPrefix(arg, "--socket-path=")
		}
	}
	if socket == "" {
		os.Exit(2)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		os.Exit(3)
	}
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func(conn net.Conn) {
			defer conn.Close()
			_, _ = io.Copy(io.Discard, conn)
		}(conn)
	}
}

func shortRuntimeDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rkr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func writeFakeBuildah(t *testing.T, dir string) string {
	t.Helper()
	script := filepath.Join(dir, "buildah")
	content := `#!/bin/sh
printf '%s\n' "$*" >> "$LIGHTVM_FAKE_BUILDAH_LOG"
case "$1" in
  inspect) exit 1 ;;
  pull) exit 0 ;;
  from) printf '%s\n' "$3" >> "$LIGHTVM_FAKE_CONTAINERS"; exit 0 ;;
  mount) mkdir -p "$LIGHTVM_FAKE_ROOTFS"; printf '%s\n' "$LIGHTVM_FAKE_ROOTFS" ;;
  containers) case "$*" in *"{{.ContainerName}}"*) cat "$LIGHTVM_FAKE_CONTAINERS" 2>/dev/null; exit 0 ;; *) echo "unexpected buildah template: $*" >&2; exit 125 ;; esac ;;
  umount|rm) exit 0 ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func writeFakeVirtiofsd(t *testing.T, dir string) string {
	t.Helper()
	script := filepath.Join(dir, "virtiofsd")
	content := `#!/bin/sh
exec "$LIGHTVM_TEST_BINARY" -test.run=TestFakeVirtiofsdHelper -- "$@"
`
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestRuntimeStatusReportsMissingTools(t *testing.T) {
	dir := t.TempDir()
	runtime, err := lightvm.NewRuntime(
		&fakeLightVMQEMU{},
		filepath.Join(dir, "missing-buildah"),
		filepath.Join(dir, "missing-virtiofsd"),
		filepath.Join(dir, "run"),
		zerolog.Nop(),
	)
	if err != nil {
		t.Fatal(err)
	}
	ready, state, detail := runtime.LightVMStatus()
	if ready || state != "missing" || !strings.Contains(detail, "buildah is required") {
		t.Fatalf("missing tools status = ready %v, state %q, detail %q", ready, state, detail)
	}

	runtime, err = lightvm.NewRuntime(
		&fakeLightVMQEMU{},
		"/bin/sh",
		"/bin/sh",
		filepath.Join(dir, "run"),
		zerolog.Nop(),
	)
	if err != nil {
		t.Fatal(err)
	}
	ready, state, detail = runtime.LightVMStatus()
	if !ready || state != "ready" || !strings.Contains(detail, "buildah") || !strings.Contains(detail, "virtiofsd") {
		t.Fatalf("available tools status = ready %v, state %q, detail %q", ready, state, detail)
	}
}

func TestRuntimeBuildsEphemeralRootfs(t *testing.T) {
	dir := t.TempDir()
	rootfs := filepath.Join(dir, "rootfs")
	buildahLog := filepath.Join(dir, "buildah.log")
	if err := os.WriteFile(buildahLog, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIGHTVM_FAKE_BUILDAH_LOG", buildahLog)
	t.Setenv("LIGHTVM_FAKE_ROOTFS", rootfs)
	t.Setenv("LIGHTVM_FAKE_CONTAINERS", filepath.Join(dir, "containers.log"))
	t.Setenv("LIGHTVM_TEST_BINARY", os.Args[0])
	t.Setenv("GO_WANT_FAKE_VIRTIOFSD", "1")

	qemuRunner := &fakeLightVMQEMU{}
	runtime, err := lightvm.NewRuntime(
		qemuRunner,
		writeFakeBuildah(t, dir),
		writeFakeVirtiofsd(t, dir),
		shortRuntimeDir(t),
		zerolog.Nop(),
	)
	if err != nil {
		t.Fatal(err)
	}
	vm := store.LightVM{
		ID:             uuid.NewString(),
		Name:           "runtime-test",
		Image:          "alpine:3.20",
		ExecutablePath: "/usr/local/bin/app",
		ExecutableArgs: []string{"--listen", ":8080"},
		Env: []store.LightVMEnvVar{
			{Name: "APP_ENV", Value: "test"},
			{Name: "WITH_EQUALS", Value: "a=b"},
		},
	}
	if err := runtime.StartLightVM(context.Background(), vm); err != nil {
		t.Fatalf("StartLightVM: %v", err)
	}
	if !qemuRunner.running {
		t.Fatal("QEMU was not started")
	}
	envData, err := os.ReadFile(filepath.Join(rootfs, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if got, want := string(envData), "APP_ENV=test\nWITH_EQUALS=a=b\n"; got != want {
		t.Fatalf(".env = %q, want %q", got, want)
	}
	entrypointData, err := os.ReadFile(filepath.Join(rootfs, ".entrypoint"))
	if err != nil {
		t.Fatalf("read .entrypoint: %v", err)
	}
	wantEntrypoint := "#!/bin/sh\n# Generated by Rookery from the LightVM definition.\nexec '/usr/local/bin/app' '--listen' ':8080'\n"
	if got := string(entrypointData); got != wantEntrypoint {
		t.Fatalf(".entrypoint = %q, want %q", got, wantEntrypoint)
	}
	info, err := os.Stat(filepath.Join(rootfs, ".entrypoint"))
	if err != nil {
		t.Fatalf("stat .entrypoint: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf(".entrypoint mode = %o, want 755", info.Mode().Perm())
	}
	if qemuRunner.fs.Tag != lightvm.RootfsTag {
		t.Fatalf("virtiofs tag = %q", qemuRunner.fs.Tag)
	}
	if _, err := os.Stat(qemuRunner.fs.SocketPath); err != nil {
		t.Fatalf("virtiofs socket was not created: %v", err)
	}
	logData, err := os.ReadFile(buildahLog)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logData)
	for _, command := range []string{"inspect --type=image alpine:3.20", "pull alpine:3.20", "from --name rookery-lightvm-" + vm.ID + "-", "mount rookery-lightvm-" + vm.ID + "-"} {
		if !strings.Contains(logText, command) {
			t.Fatalf("buildah log %q does not contain %q", logText, command)
		}
	}

	if err := runtime.StopLightVM(context.Background(), vm.ID); err != nil {
		t.Fatalf("StopLightVM: %v", err)
	}
	if qemuRunner.running {
		t.Fatal("QEMU is still running")
	}
	if _, err := os.Stat(qemuRunner.fs.SocketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("virtiofs socket still exists: %v", err)
	}
	logData, err = os.ReadFile(buildahLog)
	if err != nil {
		t.Fatal(err)
	}
	logText = string(logData)
	for _, command := range []string{"umount rookery-lightvm-" + vm.ID + "-", "rm rookery-lightvm-" + vm.ID + "-"} {
		if !strings.Contains(logText, command) {
			t.Fatalf("buildah log %q does not contain %q", logText, command)
		}
	}
}

func TestRuntimeTwoPhaseAttemptResources(t *testing.T) {
	dir := t.TempDir()
	rootfs := filepath.Join(dir, "rootfs")
	buildahLog := filepath.Join(dir, "buildah.log")
	if err := os.WriteFile(buildahLog, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIGHTVM_FAKE_BUILDAH_LOG", buildahLog)
	t.Setenv("LIGHTVM_FAKE_ROOTFS", rootfs)
	t.Setenv("LIGHTVM_FAKE_CONTAINERS", filepath.Join(dir, "containers.log"))
	t.Setenv("LIGHTVM_TEST_BINARY", os.Args[0])
	t.Setenv("GO_WANT_FAKE_VIRTIOFSD", "1")

	qemuRunner := &fakeLightVMQEMU{}
	runtime, err := lightvm.NewRuntime(
		qemuRunner, writeFakeBuildah(t, dir), writeFakeVirtiofsd(t, dir),
		shortRuntimeDir(t), zerolog.Nop(),
	)
	if err != nil {
		t.Fatal(err)
	}
	vm := store.LightVM{
		ID: uuid.NewString(), Name: "two-phase", Image: "alpine:3.20",
		ExecutablePath: "/bin/app", LifecycleGeneration: 7,
	}
	prepared, err := runtime.PrepareLightVM(context.Background(), vm)
	if err != nil {
		t.Fatalf("PrepareLightVM: %v", err)
	}
	if !strings.HasPrefix(prepared.Container, "rookery-lightvm-"+vm.ID+"-") ||
		!strings.Contains(prepared.AttemptToken, "0000000000000007") ||
		!strings.HasSuffix(prepared.VirtiofsdSocket, ".virtiofs.sock") ||
		prepared.VirtiofsdStarted || prepared.QEMUStarted {
		t.Fatalf("prepared handle = %+v", prepared)
	}
	if err := runtime.StartPreparedLightVM(context.Background(), &prepared, vm, qemu.StartPlan{}); err != nil {
		t.Fatalf("StartPreparedLightVM: %v", err)
	}
	if !prepared.VirtiofsdStarted || !prepared.QEMUStarted || !qemuRunner.running {
		t.Fatalf("started handle = %+v, running = %v", prepared, qemuRunner.running)
	}
	if err := runtime.StopQEMULightVM(context.Background(), vm.ID); err != nil {
		t.Fatalf("StopQEMULightVM: %v", err)
	}
	if err := runtime.CleanupPreparedLightVM(context.Background(), prepared); err != nil {
		t.Fatalf("CleanupPreparedLightVM: %v", err)
	}
	logData, err := os.ReadFile(buildahLog)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logData)
	if !strings.Contains(logText, "umount "+prepared.Container) || !strings.Contains(logText, "rm "+prepared.Container) {
		t.Fatalf("buildah log %q missing exact cleanup for %s", logText, prepared.Container)
	}
	if strings.Contains(logText, "rm rookery-lightvm-"+vm.ID+"\n") {
		t.Fatalf("cleanup removed the legacy container: %q", logText)
	}
}

func TestRuntimeCleansUpWhenQEMUFails(t *testing.T) {
	dir := t.TempDir()
	rootfs := filepath.Join(dir, "rootfs")
	buildahLog := filepath.Join(dir, "buildah.log")
	if err := os.WriteFile(buildahLog, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIGHTVM_FAKE_BUILDAH_LOG", buildahLog)
	t.Setenv("LIGHTVM_FAKE_ROOTFS", rootfs)
	t.Setenv("LIGHTVM_FAKE_CONTAINERS", filepath.Join(dir, "containers.log"))
	t.Setenv("LIGHTVM_TEST_BINARY", os.Args[0])
	t.Setenv("GO_WANT_FAKE_VIRTIOFSD", "1")

	qemuRunner := &fakeLightVMQEMU{startErr: errors.New("qemu failed")}
	runtime, err := lightvm.NewRuntime(
		qemuRunner,
		writeFakeBuildah(t, dir),
		writeFakeVirtiofsd(t, dir),
		shortRuntimeDir(t),
		zerolog.Nop(),
	)
	if err != nil {
		t.Fatal(err)
	}
	vm := store.LightVM{
		ID: uuid.NewString(), Name: "runtime-test", Image: "alpine:3.20",
		ExecutablePath: "/bin/app",
	}
	if err := runtime.StartLightVM(context.Background(), vm); err == nil {
		t.Fatal("StartLightVM should fail when QEMU fails")
	}
	logData, err := os.ReadFile(buildahLog)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logData)
	for _, command := range []string{"umount rookery-lightvm-" + vm.ID + "-", "rm rookery-lightvm-" + vm.ID + "-"} {
		if !strings.Contains(logText, command) {
			t.Fatalf("buildah log %q does not contain %q", logText, command)
		}
	}
	if qemuRunner.running {
		t.Fatal("QEMU is marked running after a failed start")
	}
}
