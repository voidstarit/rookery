package qemu

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/store"
)

func testLightVM() store.LightVM {
	return store.LightVM{
		ID:            uuid.NewString(),
		Name:          "app-01",
		VCPUs:         2,
		MemoryMB:      1024,
		KernelPath:    "/var/lib/rookery/lightvm/kernels/vmlinuz",
		InitramfsPath: "/var/lib/rookery/lightvm/initramfs/initramfs.img",
		Append:        "console=hvc0 rdinit=/init",
		Interfaces: []store.VMInterface{{
			SocketPath: "/run/vpp/app-01.sock",
			MAC:        "02:00:00:00:00:01",
		}},
	}
}

func buildLightVMArgsForTest(t *testing.T, vm store.LightVM, consoles []lightVMConsoleArg, pidfile, monitor, qmp string, rootfs LightVMVirtioFS, configDrive LightVMConfigDrive) string {
	t.Helper()
	args, err := buildLightVMArgs(vm, consoles, pidfile, monitor, qmp, rootfs, nil, configDrive, StartPlan{}, nil)
	if err != nil {
		t.Fatalf("buildLightVMArgs: %v", err)
	}
	return strings.Join(args, " ")
}

func TestBuildLightVMArgs(t *testing.T) {
	vm := testLightVM()
	args := buildLightVMArgsForTest(
		t,
		vm,
		[]lightVMConsoleArg{{
			SocketPath: "/run/rookery/" + vm.ID + ".console.1",
			PortName:   "org.example.console",
		}},
		"/run/rookery/"+vm.ID+".lightvm.pid",
		"/run/rookery/"+vm.ID+".lightvm.monitor",
		"/run/rookery/"+vm.ID+".lightvm.qmp",
		LightVMVirtioFS{SocketPath: "/run/rookery/" + vm.ID + ".virtiofs", Tag: "rootfs"},
		LightVMConfigDrive{},
	)

	for _, want := range []string{
		"-name app-01",
		"-uuid " + vm.ID,
		"-accel kvm",
		"-cpu host",
		"-smp 2",
		"-m 1024",
		"-kernel " + vm.KernelPath,
		"-initrd " + vm.InitramfsPath,
		"-append console=hvc0 rdinit=/init APP_HOSTNAME=app-01",
		"-display none",
		"-device virtio-serial-pci",
		"-chardev socket,id=console0,path=/run/rookery/" + vm.ID + ".console.1,server=on,wait=off",
		"-device virtconsole,chardev=console0,name=org.example.console",
		"-pidfile /run/rookery/" + vm.ID + ".lightvm.pid",
		"-monitor unix:/run/rookery/" + vm.ID + ".lightvm.monitor,server,nowait",
		"-qmp unix:/run/rookery/" + vm.ID + ".lightvm.qmp,server,nowait",
		"-object memory-backend-memfd,id=mem,size=1024M,share=on",
		"-numa node,memdev=mem",
		"-chardev socket,id=char0,path=/run/vpp/app-01.sock,reconnect=1",
		"-netdev type=vhost-user,id=net0,chardev=char0,vhostforce=on",
		"-device virtio-net-pci,netdev=net0,mac=02:00:00:00:00:01",
		"-chardev socket,id=fs0,path=/run/rookery/" + vm.ID + ".virtiofs",
		"-device vhost-user-fs-pci,chardev=fs0,tag=rootfs",
		"-nic none",
		"-daemonize",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %q do not contain %q", args, want)
		}
	}
	if strings.Contains(args, "-drive") {
		t.Fatalf("args %q should not contain a block device", args)
	}
	if strings.Contains(args, "-machine") {
		t.Fatalf("args %q should not override the QEMU machine type", args)
	}
}

func TestBuildLightVMArgsWithConfigDrive(t *testing.T) {
	vm := testLightVM()
	vm.Interfaces = nil
	vm.Disks = []store.VMDisk{{ID: 7, Name: "root", Device: "/dev/vg0/root"}}
	configPath := "/run/rookery/" + vm.ID + ".rookery-config.tar"
	args := buildLightVMArgsForTest(t, vm, []lightVMConsoleArg{{SocketPath: "/tmp/console", PortName: "org.example.console"}},
		"/tmp/pid", "/tmp/monitor", "/tmp/qmp", LightVMVirtioFS{}, LightVMConfigDrive{Path: configPath})

	for _, want := range []string{
		"-drive file=/dev/vg0/root,format=raw,if=none,id=rookery-disk-7",
		"-device virtio-blk-pci,drive=rookery-disk-7",
		"-drive file=" + configPath + ",format=raw,if=none,id=rookery-config,readonly=on",
		"-device virtio-blk-pci,drive=rookery-config,serial=rookery-config",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %q do not contain %q", args, want)
		}
	}
	diskIndex := strings.Index(args, "drive=rookery-disk-7")
	configIndex := strings.Index(args, "drive=rookery-config")
	if diskIndex < 0 || configIndex < 0 || diskIndex > configIndex {
		t.Fatalf("config drive must follow workload disks: %q", args)
	}
}

func TestBuildLightVMArgsWithVirtioFShares(t *testing.T) {
	vm := testLightVM()
	args, err := buildLightVMArgs(vm, []lightVMConsoleArg{{SocketPath: "/tmp/console", PortName: "org.example.console"}},
		"/tmp/pid", "/tmp/monitor", "/tmp/qmp", LightVMVirtioFS{SocketPath: "/tmp/rootfs", Tag: "rootfs"},
		[]LightVMVirtioFS{
			{SocketPath: "/tmp/share1", Tag: "tag1", ReadOnly: true},
			{SocketPath: "/tmp/share2", Tag: "tag2"},
		}, LightVMConfigDrive{}, StartPlan{}, nil)
	if err != nil {
		t.Fatalf("buildLightVMArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-chardev socket,id=fs0,path=/tmp/rootfs",
		"-device vhost-user-fs-pci,chardev=fs0,tag=rootfs",
		"-chardev socket,id=fs1,path=/tmp/share1",
		"-device vhost-user-fs-pci,chardev=fs1,tag=tag1",
		"-chardev socket,id=fs2,path=/tmp/share2",
		"-device vhost-user-fs-pci,chardev=fs2,tag=tag2",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q do not contain %q", joined, want)
		}
	}
}

func TestBuildLightVMArgsWithMultipleConsoles(t *testing.T) {
	vm := testLightVM()
	args := buildLightVMArgsForTest(t, vm, []lightVMConsoleArg{
		{SocketPath: "/tmp/console-one", PortName: "org.example.console"},
		{SocketPath: "/tmp/console-two", PortName: "org.example.console.2"},
	}, "/tmp/pid", "/tmp/monitor", "/tmp/qmp", LightVMVirtioFS{}, LightVMConfigDrive{})

	for _, want := range []string{
		"-chardev socket,id=console0,path=/tmp/console-one,server=on,wait=off",
		"-device virtconsole,chardev=console0,name=org.example.console",
		"-chardev socket,id=console1,path=/tmp/console-two,server=on,wait=off",
		"-device virtconsole,chardev=console1,name=org.example.console.2",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %q do not contain %q", args, want)
		}
	}
	if got := strings.Count(args, "-device virtconsole"); got != 2 {
		t.Fatalf("virtconsole count = %d, want 2: %q", got, args)
	}
}

func TestBuildLightVMArgsWithoutAppendOrInterfaces(t *testing.T) {
	vm := testLightVM()
	vm.Append = ""
	vm.Interfaces = nil
	args := buildLightVMArgsForTest(t, vm, []lightVMConsoleArg{{SocketPath: "/tmp/console", PortName: "org.example.console"}}, "/tmp/pid", "/tmp/monitor", "/tmp/qmp", LightVMVirtioFS{}, LightVMConfigDrive{})
	if !strings.Contains(args, "-append APP_HOSTNAME=app-01") {
		t.Fatalf("args %q should contain the default APP_HOSTNAME", args)
	}
	if strings.Contains(args, "memory-backend-memfd") || strings.Contains(args, "vhost-user") {
		t.Fatalf("args %q should omit interface devices", args)
	}
}

func TestBuildLightVMArgsPreservesAppHostname(t *testing.T) {
	vm := testLightVM()
	vm.Append = "console=hvc0 APP_HOSTNAME=web-07 rdinit=/init"
	args := buildLightVMArgsForTest(t, vm, []lightVMConsoleArg{{SocketPath: "/tmp/console", PortName: "org.example.console"}}, "/tmp/pid", "/tmp/monitor", "/tmp/qmp", LightVMVirtioFS{}, LightVMConfigDrive{})
	if strings.Count(args, "APP_HOSTNAME=") != 1 {
		t.Fatalf("args %q should contain exactly one APP_HOSTNAME", args)
	}
	if !strings.Contains(args, "-append console=hvc0 APP_HOSTNAME=web-07 rdinit=/init") {
		t.Fatalf("args %q did not preserve the supplied APP_HOSTNAME", args)
	}
}

func TestLightVMHostname(t *testing.T) {
	for _, testCase := range []struct {
		name string
		want string
	}{
		{name: "app-01", want: "app-01"},
		{name: "My App_01", want: "my-app-01"},
		{name: "  --  ", want: "lightvm"},
		{name: strings.Repeat("a", 70), want: strings.Repeat("a", 63)},
	} {
		if got := lightVMHostname(testCase.name); got != testCase.want {
			t.Errorf("lightVMHostname(%q) = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}

func TestLightVMPaths(t *testing.T) {
	runDir := t.TempDir()
	q, err := New(DefaultBinary, runDir)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	socket := q.LightVMConsoleSocket(id, 0)
	if socket != filepath.Join(runDir, id+".console") {
		t.Fatalf("console socket = %q", socket)
	}
	if q.LightVMConsoleSocket("not-a-uuid", 0) != "" {
		t.Fatal("invalid LightVM id should not return a socket")
	}
	if q.RunningLightVM(id) {
		t.Fatal("RunningLightVM = true without a pid file")
	}
}

func TestBuildLightVMArgsEphemeral(t *testing.T) {
	vm := testLightVM()
	vm.Ephemeral = true
	args := buildLightVMArgsForTest(t, vm,
		[]lightVMConsoleArg{{SocketPath: "/tmp/console", PortName: "org.example.console"}},
		"/tmp/pid", "/tmp/monitor", "/tmp/qmp", LightVMVirtioFS{}, LightVMConfigDrive{})
	for _, want := range []string{
		"-no-reboot",
		"-append console=hvc0 rdinit=/init panic=-1 APP_HOSTNAME=app-01",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("ephemeral args %q do not contain %q", args, want)
		}
	}
}

func TestBuildLightVMArgsEphemeralPreservesPanicAndOmitsFlagWhenNotEphemeral(t *testing.T) {
	vm := testLightVM()
	vm.Ephemeral = true
	vm.Append = "console=hvc0 panic=10 rdinit=/init"
	args := buildLightVMArgsForTest(t, vm,
		[]lightVMConsoleArg{{SocketPath: "/tmp/console", PortName: "org.example.console"}},
		"/tmp/pid", "/tmp/monitor", "/tmp/qmp", LightVMVirtioFS{}, LightVMConfigDrive{})
	if strings.Contains(args, "panic=-1") {
		t.Fatalf("explicit panic= was overwritten: %q", args)
	}
	if !strings.Contains(args, "-append console=hvc0 panic=10 rdinit=/init APP_HOSTNAME=app-01") {
		t.Fatalf("args %q did not preserve explicit panic", args)
	}

	vm.Ephemeral = false
	vm.Append = ""
	args = buildLightVMArgsForTest(t, vm,
		[]lightVMConsoleArg{{SocketPath: "/tmp/console", PortName: "org.example.console"}},
		"/tmp/pid", "/tmp/monitor", "/tmp/qmp", LightVMVirtioFS{}, LightVMConfigDrive{})
	if strings.Contains(args, "-no-reboot") || strings.Contains(args, "panic=-1") {
		t.Fatalf("non-ephemeral args %q must not contain ephemeral flags", args)
	}
}
