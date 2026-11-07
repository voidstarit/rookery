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

func TestAttachmentReferenceVariants(t *testing.T) {
	nvme := Attachment{
		Protocol: ProtocolNVMeTCP,
		NVMeTCP: &NVMeTCPAttachment{
			NVMeTCPReference: NVMeTCPReference{
				Address: "10.0.0.1:4420", NQN: "nqn.x", HostNQN: "nqn.host", NSID: 1,
			},
			PSK: "secret",
		},
	}
	ref := nvme.Reference()
	if ref.NVMeTCP == nil || ref.NVMeTCP.NQN != "nqn.x" {
		t.Fatalf("nvme reference = %+v", ref)
	}
	rbd := Attachment{
		Protocol: ProtocolRBD,
		RBD: &RBDAttachment{RBDReference: RBDReference{
			Monitors: []string{"mon1:6789"}, Pool: "rbd", Image: "disk", Keyring: "/keyring",
		}},
	}
	ref = rbd.Reference()
	if ref.RBD == nil || ref.RBD.Pool != "rbd" {
		t.Fatalf("rbd reference = %+v", ref)
	}
}

func TestProtocolNormalize(t *testing.T) {
	if Protocol("").Normalize() != ProtocolLocal {
		t.Fatal("empty protocol did not normalize to local")
	}
	if ProtocolISCSI.Normalize() != ProtocolISCSI {
		t.Fatal("iSCSI protocol changed")
	}
}

func TestReferenceValidationBranches(t *testing.T) {
	tests := []Reference{
		{Protocol: ProtocolLocal, ISCSI: &ISCSIReference{}},
		{Protocol: ProtocolNVMeTCP, ISCSI: &ISCSIReference{}},
		{Protocol: ProtocolRBD, ISCSI: &ISCSIReference{}},
		{Protocol: ProtocolISCSI, ISCSI: &ISCSIReference{Portal: "p:3260"}},
		{Protocol: ProtocolISCSI, ISCSI: &ISCSIReference{Portal: "p:3260", Target: "t", LUN: -1}},
		{Protocol: ProtocolNVMeTCP, NVMeTCP: &NVMeTCPReference{NQN: "nqn.x"}},
		{Protocol: ProtocolNVMeTCP, NVMeTCP: &NVMeTCPReference{Address: "a:4420"}},
		{Protocol: ProtocolNVMeTCP, NVMeTCP: &NVMeTCPReference{Address: "a:4420", NQN: "nqn.x", NSID: -1}},
		{Protocol: ProtocolRBD, RBD: &RBDReference{Image: "disk"}},
		{Protocol: ProtocolRBD, RBD: &RBDReference{Pool: "rbd"}},
	}
	for _, ref := range tests {
		if err := ref.Validate(); err == nil {
			t.Fatalf("Validate() accepted %+v", ref)
		}
	}
}

func TestReferenceValidateCompleteMissingBodies(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolISCSI, ProtocolNVMeTCP, ProtocolRBD} {
		if err := (Reference{Protocol: protocol}).ValidateComplete(); err == nil {
			t.Fatalf("ValidateComplete() accepted missing %s body", protocol)
		}
	}
	if err := (Reference{Protocol: ProtocolLocal}).ValidateComplete(); err != nil {
		t.Fatalf("local ValidateComplete() = %v", err)
	}
}

func TestLocatorMissingBodies(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolISCSI, ProtocolNVMeTCP, ProtocolRBD} {
		if got := (Reference{Protocol: protocol}).Locator(); got != "" {
			t.Fatalf("Locator() = %q for missing %s body", got, protocol)
		}
	}
}

func TestAttachmentValidateCompleteBranches(t *testing.T) {
	tests := []Attachment{
		{Protocol: ProtocolISCSI},
		{Protocol: ProtocolISCSI, ISCSI: &ISCSIAttachment{
			ISCSIReference: ISCSIReference{Portal: "p", Target: "t"}, Credential: "secret",
		}},
		{Protocol: ProtocolISCSI, ISCSI: &ISCSIAttachment{
			ISCSIReference: ISCSIReference{Portal: "p", Target: "t"}, Username: "user",
		}},
		{Protocol: ProtocolNVMeTCP},
		{Protocol: ProtocolRBD},
		{Protocol: "fc"},
	}
	for _, attachment := range tests {
		if err := attachment.ValidateComplete(); err == nil {
			t.Fatalf("ValidateComplete() accepted %+v", attachment)
		}
	}
	if err := (Attachment{Protocol: ProtocolNVMeTCP, NVMeTCP: &NVMeTCPAttachment{
		NVMeTCPReference: NVMeTCPReference{Address: "a", NQN: "nqn"},
	}}).ValidateComplete(); err != nil {
		t.Fatalf("valid NVMe ValidateComplete() = %v", err)
	}
	if err := (Attachment{Protocol: ProtocolRBD, RBD: &RBDAttachment{
		RBDReference: RBDReference{Pool: "rbd", Image: "disk"},
	}}).ValidateComplete(); err != nil {
		t.Fatalf("valid RBD ValidateComplete() = %v", err)
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
