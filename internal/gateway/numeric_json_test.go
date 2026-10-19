package gateway

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func TestNumericJSONListFields(t *testing.T) {
	marshaler := newNumericJSONMarshaler()
	data, err := marshaler.Marshal([]*rookeryv1.VM{{
		Id: "442c83d0-a95a-4f7d-b7c5-8d3bee55bcf6", Name: "vm-01", DiskId: proto.Int64(7),
	}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"disk_id":7`) || !strings.Contains(text, `"id":"442c83d0-a95a-4f7d-b7c5-8d3bee55bcf6"`) {
		t.Fatalf("list JSON = %s", text)
	}
	empty, err := marshaler.Marshal([]*rookeryv1.PacketCaptureFile{})
	if err != nil {
		t.Fatal(err)
	}
	if string(empty) != "[]" {
		t.Fatalf("empty list JSON = %s", empty)
	}
}
