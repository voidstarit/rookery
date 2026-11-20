package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/kouja"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/store"
)

func TestDiskInUseErrorStatuses(t *testing.T) {
	cases := []struct {
		err  *store.DiskInUseError
		want string
	}{
		{err: &store.DiskInUseError{OwnerName: "app", OwnerKind: "vm"}, want: "disk is attached to vm \"app\"; detach it first"},
		{err: &store.DiskInUseError{OwnerID: "abc", OwnerKind: "lightvm"}, want: "disk is attached to lightvm abc; detach it first"},
		{err: &store.DiskInUseError{OwnerID: "abc"}, want: "disk is attached to workload abc; detach it first"},
		{err: &store.DiskInUseError{}, want: "disk is attached to a workload; detach it first"},
	}
	for _, tc := range cases {
		if got := diskInUseError(tc.err).Error(); got == "" {
			t.Fatalf("empty status for %+v", tc.err)
		}
	}
	if got := diskInUseError(errors.New("other")).Error(); got == "" {
		t.Fatal("unrelated error returned an empty status")
	}
}

func TestDiskInUseMultiWorkloadStatus(t *testing.T) {
	got := diskInUseError(&store.DiskInUseError{AttachmentCount: 3}).Error()
	if !strings.Contains(got, "3 workloads") || !strings.Contains(got, "detach them first") {
		t.Fatalf("multi-workload status = %q", got)
	}
}

func TestDiskInUseFromRecord(t *testing.T) {
	if got := diskInUseFromRecord(store.Disk{}); got == nil || got.AttachmentCount != 0 {
		t.Fatalf("empty disk record = %+v", got)
	}
	record := store.Disk{Attachments: []store.DiskAttachment{
		{WorkloadKind: "vm", WorkloadID: "a", WorkloadName: "app"},
		{WorkloadKind: "lightvm", WorkloadID: "b", WorkloadName: "worker"},
	}}
	got := diskInUseFromRecord(record)
	if got.AttachmentCount != 2 || got.OwnerKind != "vm" || got.OwnerName != "app" {
		t.Fatalf("first owner = %+v", got)
	}
}

func TestLightVMErrorMappings(t *testing.T) {
	cases := []error{
		store.ErrLightVMNotFound,
		store.ErrLightVMNameTaken,
		store.ErrLightVMKernelNotFound,
		store.ErrLightVMInitramfsNotFound,
		store.ErrInterfaceNotFound,
		store.ErrInterfaceInUse,
		store.ErrDiskNotFound,
		store.ErrInvalidDiskIDs,
		store.ErrLightVMDeviceLimit,
		store.ErrInvalidVirtioFSShare,
		store.ErrDiskIsTemplate,
		store.ErrDiskDeleting,
		&store.DiskInUseError{OwnerKind: "vm", OwnerName: "app"},
	}
	for _, input := range cases {
		if err := lightVMError(input, "read"); err == nil {
			t.Fatalf("%v returned no error", input)
		}
	}
	if got := lightVMError(errors.New("other"), "read").Error(); got == "" {
		t.Fatal("unknown error returned an empty status")
	}
	startCases := []error{
		kouja.ErrSessionActive,
		context.Canceled,
		qemu.ErrAlreadyRunning,
		errors.New("other"),
	}
	for _, input := range startCases {
		if err := lightVMStartStatusError(input); err == nil {
			t.Fatalf("%v returned no start error", input)
		}
	}
}

func TestWithLightVMStartCoordinatorOption(t *testing.T) {
	opts := &options{}
	WithLightVMStartCoordinator(nil)(opts)
	if opts.lightvmCoordinator != nil {
		t.Fatal("nil coordinator was not applied")
	}
}
