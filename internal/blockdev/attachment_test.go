package blockdev

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReferenceValidateAndLocator(t *testing.T) {
	tests := []struct {
		name    string
		ref     Reference
		wantErr string
		wantLoc string
	}{
		{
			name:    "local",
			ref:     Reference{Protocol: ProtocolLocal},
			wantLoc: "",
		},
		{
			name: "iscsi",
			ref: Reference{Protocol: ProtocolISCSI, ISCSI: &ISCSIReference{
				Portal: "10.0.0.1:3260", Target: "iqn.2026-11.io.example:disk", LUN: 0,
			}},
			wantLoc: "iscsi 10.0.0.1:3260 iqn.2026-11.io.example:disk lun 0",
		},
		{
			name: "nvme",
			ref: Reference{Protocol: ProtocolNVMeTCP, NVMeTCP: &NVMeTCPReference{
				Address: "10.0.0.1:4420", NQN: "nqn.2026-11.io.example:disk", NSID: 1,
			}},
			wantLoc: "nvme-tcp 10.0.0.1:4420 nqn.2026-11.io.example:disk nsid 1",
		},
		{
			name: "rbd",
			ref: Reference{Protocol: ProtocolRBD, RBD: &RBDReference{
				Pool: "rbd", Image: "disk", Snapshot: "snap",
			}},
			wantLoc: "rbd rbd/disk@snap",
		},
		{
			name:    "missing iscsi body",
			ref:     Reference{Protocol: ProtocolISCSI, ISCSI: &ISCSIReference{}},
			wantErr: "iSCSI portal",
		},
		{
			name:    "mismatched body",
			ref:     Reference{Protocol: ProtocolISCSI, RBD: &RBDReference{Pool: "p", Image: "i"}},
			wantErr: "mismatched",
		},
		{
			name:    "unknown",
			ref:     Reference{Protocol: "fc"},
			wantErr: "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Validate() = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() = %v", err)
			}
			if got := tt.ref.Locator(); got != tt.wantLoc {
				t.Fatalf("Locator() = %q, want %q", got, tt.wantLoc)
			}
		})
	}
}

func TestReferenceValidateComplete(t *testing.T) {
	if err := (Reference{Protocol: ProtocolISCSI}).Validate(); err != nil {
		t.Fatalf("protocol-only Validate() = %v", err)
	}
	if err := (Reference{Protocol: ProtocolISCSI}).ValidateComplete(); err == nil {
		t.Fatal("ValidateComplete() accepted a missing iSCSI body")
	}
}

func TestAttachmentReferenceStripsSecrets(t *testing.T) {
	attachment := Attachment{
		Protocol: ProtocolISCSI,
		ISCSI: &ISCSIAttachment{
			ISCSIReference: ISCSIReference{Portal: "10.0.0.1:3260", Target: "iqn.x", LUN: 1},
			Username:       "kouja-r",
			Credential:     "secret",
		},
	}
	if err := attachment.ValidateComplete(); err != nil {
		t.Fatalf("ValidateComplete() = %v", err)
	}
	encoded, err := json.Marshal(attachment)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret") {
		t.Fatalf("attachment JSON contains credential: %s", encoded)
	}
	reference := attachment.Reference()
	if reference.ISCSI == nil || reference.ISCSI.Portal != "10.0.0.1:3260" {
		t.Fatalf("Reference() = %+v", reference)
	}
}

func TestReferenceJSONRoundTrip(t *testing.T) {
	original := Reference{
		Protocol: ProtocolRBD,
		RBD: &RBDReference{
			Monitors: []string{"mon1:6789"},
			Pool:     "rbd",
			Image:    "disk",
			Keyring:  "/etc/ceph/ceph.client.rookery.keyring",
		},
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Reference
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.ValidateComplete(); err != nil {
		t.Fatalf("decoded ValidateComplete() = %v", err)
	}
	if decoded.RBD == nil || decoded.RBD.Monitors[0] != "mon1:6789" {
		t.Fatalf("decoded = %+v", decoded)
	}
}
