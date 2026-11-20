package store_test

import (
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func TestDiskAttachedToAnyWorkloadUsesAttachmentsAndLegacyOwners(t *testing.T) {
	vmID := "11111111-1111-1111-1111-111111111111"
	lightVMID := "22222222-2222-2222-2222-222222222222"
	cases := []struct {
		name string
		disk store.Disk
		want bool
	}{
		{
			name: "attachment list",
			disk: store.Disk{Attachments: []store.DiskAttachment{{WorkloadKind: "vm", WorkloadID: vmID, WorkloadName: "vm-one"}}},
			want: true,
		},
		{
			name: "legacy VM owner",
			disk: store.Disk{AttachedVMID: &vmID, AttachedVMName: "vm-one"},
			want: true,
		},
		{
			name: "legacy LightVM owner",
			disk: store.Disk{AttachedLightVMID: &lightVMID, AttachedLightVMName: "light-one"},
			want: true,
		},
		{
			name: "unattached",
			disk: store.Disk{},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := store.DiskAttachedToAnyWorkload(tc.disk); got != tc.want {
				t.Fatalf("DiskAttachedToAnyWorkload = %v, want %v", got, tc.want)
			}
		})
	}
}
