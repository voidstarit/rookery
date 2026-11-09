package provider

import (
	"context"
	"testing"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func TestDiskThrottleClassDataSourceLookup(t *testing.T) {
	fake := &fakeDiskThrottleClient{list: []*rookeryv1.DiskThrottleClass{
		{Id: 1, Name: "standard", Limits: &rookeryv1.DiskThrottleLimits{BpsRead: 1_000_000}},
	}}
	client := &Client{DiskThrottleClasses: fake}
	classes, err := client.DiskThrottleClasses.ListDiskThrottleClasses(context.Background(), &rookeryv1.ListDiskThrottleClassesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	found, err := findDiskThrottleClass(classes.GetClasses(), "STANDARD")
	if err != nil || found.GetId() != 1 {
		t.Fatalf("find = %+v, %v", found, err)
	}
	model := diskThrottleClassModelFromProto(found)
	if model.BPSRead.ValueInt64() != 1_000_000 || model.Name.ValueString() != "standard" {
		t.Fatalf("model = %+v", model)
	}
}
