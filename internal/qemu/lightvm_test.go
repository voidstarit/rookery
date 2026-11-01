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

func TestBuildLightVMArgs(t *testing.T) {
	vm := testLightVM()
	args := strings.Join(buildLightVMArgs(
		vm,
		"/run/rookery/"+vm.ID+".console",
		"/run/rookery/"+vm.ID+".lightvm.pid",
		"/run/rookery/"+vm.ID+".lightvm.monitor",
		LightVMVirtioFS{SocketPath: "/run/rookery/" + vm.ID + ".virtiofs", Tag: "rootfs"},
	), " ")

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
		"-chardev socket,id=console0,path=/run/rookery/" + vm.ID + ".console,server=on,wait=off",
		"-device virtconsole,chardev=console0,name=org.example.console",
		"-pidfile /run/rookery/" + vm.ID + ".lightvm.pid",
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

func TestBuildLightVMArgsWithoutAppendOrInterfaces(t *testing.T) {
	vm := testLightVM()
	vm.Append = ""
	vm.Interfaces = nil
	args := strings.Join(buildLightVMArgs(vm, "/tmp/console", "/tmp/pid", "/tmp/monitor", LightVMVirtioFS{}), " ")
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
	args := strings.Join(buildLightVMArgs(vm, "/tmp/console", "/tmp/pid", "/tmp/monitor", LightVMVirtioFS{}), " ")
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
	socket := q.LightVMConsoleSocket(id)
	if socket != filepath.Join(runDir, id+".console") {
		t.Fatalf("console socket = %q", socket)
	}
	if q.LightVMConsoleSocket("not-a-uuid") != "" {
		t.Fatal("invalid LightVM id should not return a socket")
	}
	if q.RunningLightVM(id) {
		t.Fatal("RunningLightVM = true without a pid file")
	}
}
