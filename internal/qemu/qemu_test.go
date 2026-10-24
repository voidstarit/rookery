package qemu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/store"
)

func testVM() store.VM {
	return store.VM{
		ID:       uuid.NewString(),
		Name:     "web-01",
		VCPUs:    2,
		MemoryMB: 2048,
		Disk: &store.VMDisk{
			Device: "/dev/vg0/web-01-root",
		},
	}
}

func TestBuildArgsWithBootMedia(t *testing.T) {
	vm := testVM()
	vm.BootMedia = &store.VMMedia{Path: "/srv/isos/debian.iso"}
	vm.Interfaces = []store.VMInterface{{
		SocketPath: "/run/vpp/web-01-eth0.sock",
		MAC:        "02:00:00:00:00:01",
	}}
	socket := "/run/rookery/" + vm.ID + ".vnc"
	pidfile := "/run/rookery/" + vm.ID + ".pid"
	monitor := "/run/rookery/" + vm.ID + ".monitor"

	args := strings.Join(buildArgs(vm, socket, pidfile, monitor), " ")

	for _, want := range []string{
		"-accel kvm",
		"-cpu host",
		"-uuid " + vm.ID,
		"-smp 2",
		"-m 2048",
		"-object memory-backend-memfd,id=mem,size=2048M,share=on",
		"-numa node,memdev=mem",
		"-chardev qemu-vdagent,id=vdagent,clipboard=on,name=vdagent",
		"-device virtio-serial-pci",
		"-device virtserialport,chardev=vdagent,name=com.redhat.spice.0",
		"-drive file=/dev/vg0/web-01-root,format=raw,if=virtio,cache=none,aio=native",
		"-vnc unix:" + socket,
		"-pidfile " + pidfile,
		"-monitor unix:" + monitor + ",server,nowait",
		"-chardev socket,id=char0,path=/run/vpp/web-01-eth0.sock,reconnect=1",
		"-netdev type=vhost-user,id=net0,chardev=char0,vhostforce=on",
		"-device virtio-net-pci,netdev=net0,mac=02:00:00:00:00:01",
		"-drive file=/srv/isos/debian.iso,media=cdrom,readonly=on",
		"-boot order=dc",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %q do not contain %q", args, want)
		}
	}
}

func TestBuildArgsWithoutBootMedia(t *testing.T) {
	vm := testVM()
	args := strings.Join(buildArgs(vm, "/tmp/vm.vnc", "/tmp/vm.pid", "/tmp/vm.monitor"), " ")

	if !strings.Contains(args, "-boot order=c") {
		t.Fatalf("args = %q, want disk boot order", args)
	}
	if strings.Contains(args, "media=cdrom") {
		t.Fatalf("args = %q, unexpected cdrom", args)
	}
	if strings.Contains(args, "memory-backend-memfd") {
		t.Fatalf("args = %q, unexpected vhost-user memory backend", args)
	}
}

func TestBuildArgsWithMultipleDisks(t *testing.T) {
	vm := testVM()
	vm.Disk = nil
	vm.Disks = []store.VMDisk{
		{Device: "/dev/vg0/web-01-root"},
		{Device: "/dev/vg0/web-01-data"},
	}
	args := strings.Join(buildArgs(vm, "/tmp/vm.vnc", "/tmp/vm.pid", "/tmp/vm.monitor"), " ")

	for _, want := range []string{
		"-drive file=/dev/vg0/web-01-root,format=raw,if=virtio,cache=none,aio=native",
		"-drive file=/dev/vg0/web-01-data,format=raw,if=virtio,cache=none,aio=native",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %q do not contain %q", args, want)
		}
	}
	if got := strings.Count(args, "-drive "); got != 2 {
		t.Fatalf("drive count = %d, want 2: %q", got, args)
	}
}

func TestPathRequiresValidUUID(t *testing.T) {
	q, err := New(DefaultBinary, t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got := q.VNCSocket("not-a-uuid"); got != "" {
		t.Fatalf("VNCSocket(invalid) = %q, want empty", got)
	}
	if got := q.VNCSocket(uuid.NewString()); got == "" {
		t.Fatal("VNCSocket(valid) is empty")
	}
}

func TestRunningFalseWithoutPIDFile(t *testing.T) {
	q, err := New(DefaultBinary, t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if q.Running(uuid.NewString()) {
		t.Fatal("Running = true without a pid file")
	}
}

func TestStopCleansStaleFiles(t *testing.T) {
	runDir := t.TempDir()
	q, err := New(DefaultBinary, runDir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	vmID := uuid.NewString()
	socket := filepath.Join(runDir, vmID+".vnc")
	pidfile := filepath.Join(runDir, vmID+".pid")
	monitor := filepath.Join(runDir, vmID+".monitor")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatalf("write socket placeholder: %v", err)
	}
	if err := os.WriteFile(pidfile, []byte("999999"), 0o600); err != nil {
		t.Fatalf("write pid file: %v", err)
	}
	if err := os.WriteFile(monitor, nil, 0o600); err != nil {
		t.Fatalf("write monitor placeholder: %v", err)
	}

	if err := q.Stop(context.Background(), vmID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("socket still exists: %v", err)
	}
	if _, err := os.Stat(pidfile); !os.IsNotExist(err) {
		t.Fatalf("pid file still exists: %v", err)
	}
	if _, err := os.Stat(monitor); !os.IsNotExist(err) {
		t.Fatalf("monitor still exists: %v", err)
	}
}
