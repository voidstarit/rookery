package gateway

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/voidstarit/rookery/internal/diskthrottle"
	"github.com/voidstarit/rookery/internal/store"
)

func TestDiskThrottleErrorMappings(t *testing.T) {
	tests := []struct {
		err  error
		code codes.Code
	}{
		{store.ErrDiskThrottleClassNotFound, codes.NotFound},
		{store.ErrDiskThrottleClassNameTaken, codes.AlreadyExists},
		{store.ErrDiskThrottleClassInUse, codes.Aborted},
		{store.ErrDiskThrottleClassOnTemplate, codes.InvalidArgument},
		{store.ErrDiskThrottleClassInvalid, codes.InvalidArgument},
		{store.ErrDiskThrottleClassInDeletion, codes.Aborted},
		{errors.New("other"), codes.Internal},
	}
	for _, test := range tests {
		if got := status.Code(diskThrottleClassError(test.err)); got != test.code {
			t.Fatalf("diskThrottleClassError(%v) = %s, want %s", test.err, got, test.code)
		}
	}
}

func TestDiskAssignmentErrorMappings(t *testing.T) {
	if diskAssignmentError(errors.New("other")) != nil {
		t.Fatal("unrelated error should not be mapped")
	}
	tests := []struct {
		err  error
		code codes.Code
	}{
		{store.ErrDiskThrottleClassOnTemplate, codes.InvalidArgument},
		{store.ErrDiskThrottleClassNotFound, codes.InvalidArgument},
		{store.ErrDiskThrottleClassInvalid, codes.InvalidArgument},
		{store.ErrDiskThrottleClassInDeletion, codes.Aborted},
	}
	for _, test := range tests {
		if got := status.Code(diskAssignmentError(test.err)); got != test.code {
			t.Fatalf("diskAssignmentError(%v) = %s, want %s", test.err, got, test.code)
		}
	}
}

func TestDiskUpdateErrorMappings(t *testing.T) {
	tests := []struct {
		err  error
		code codes.Code
	}{
		{store.ErrDiskNotFound, codes.NotFound},
		{store.ErrDiskInUse, codes.Aborted},
		{store.ErrDiskThrottleClassOnTemplate, codes.InvalidArgument},
		{store.ErrDiskThrottleClassNotFound, codes.InvalidArgument},
		{store.ErrDiskThrottleClassInvalid, codes.InvalidArgument},
		{store.ErrDiskThrottleClassInDeletion, codes.Aborted},
		{errors.New("other"), codes.Internal},
	}
	for _, test := range tests {
		if got := status.Code(diskUpdateError(test.err)); got != test.code {
			t.Fatalf("diskUpdateError(%v) = %s, want %s", test.err, got, test.code)
		}
	}
}

func TestLimitsProtoConversion(t *testing.T) {
	if got := limitsFromProto(nil); got != (diskthrottle.Limits{}) {
		t.Fatalf("nil limits = %+v", got)
	}
	limits := diskthrottle.Limits{BPSRead: 1, IOPSTotal: 2, IOPSSize: 3}
	proto := limitsToProto(limits)
	if proto.GetBpsRead() != 1 || proto.GetIopsTotal() != 2 || proto.GetIopsSize() != 3 {
		t.Fatalf("proto limits = %+v", proto)
	}
	if got := limitsFromProto(proto); got != limits {
		t.Fatalf("round trip = %+v, want %+v", got, limits)
	}
}

func TestVMDiskToProtoThrottleFields(t *testing.T) {
	classID := int64(9)
	disk := vmDiskToProto(store.VMDisk{
		ID: 1, Name: "data", ThrottleClassID: &classID, ThrottleClassName: "fast",
	})
	if disk.GetThrottleClassId() != 9 || disk.GetThrottleClassName() != "fast" {
		t.Fatalf("disk = %+v", disk)
	}
	noClass := vmDiskToProto(store.VMDisk{ID: 2})
	if noClass.GetThrottleClassId() != 0 {
		t.Fatalf("no-class disk = %+v", noClass)
	}
}
