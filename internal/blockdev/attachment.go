// Package blockdev describes block-device attachments independently of the
// storage driver that produced them. A Reference is safe to persist; an
// Attachment is created at VM start and may contain credentials.
package blockdev

import (
	"errors"
	"fmt"
	"strings"
)

// Protocol identifies how a block device is reached.
type Protocol string

const (
	ProtocolLocal   Protocol = "local"
	ProtocolISCSI   Protocol = "iscsi"
	ProtocolNVMeTCP Protocol = "nvme-tcp"
	ProtocolRBD     Protocol = "rbd"
)

// Reference is the secret-free, persisted description of an attachment.
type Reference struct {
	Protocol Protocol          `json:"protocol"`
	ISCSI    *ISCSIReference   `json:"iscsi,omitempty"`
	NVMeTCP  *NVMeTCPReference `json:"nvme_tcp,omitempty"`
	RBD      *RBDReference     `json:"rbd,omitempty"`
}

// ISCSIReference describes an iSCSI target. Portal is the portal QEMU should
// use; ExportPortal is the raw portal advertised by the storage agent.
type ISCSIReference struct {
	Portal       string `json:"portal"`
	ExportPortal string `json:"export_portal,omitempty"`
	Target       string `json:"target"`
	LUN          int    `json:"lun"`
	Initiator    string `json:"initiator,omitempty"`
}

// NVMeTCPReference describes an NVMe/TCP target. Address is the address QEMU
// should use; ExportAddress is the raw address advertised by the agent.
type NVMeTCPReference struct {
	Address       string `json:"address"`
	ExportAddress string `json:"export_address,omitempty"`
	NQN           string `json:"nqn"`
	HostNQN       string `json:"host_nqn,omitempty"`
	NSID          int    `json:"nsid"`
}

// RBDReference describes a Ceph RBD image. Keyring is a path, never key
// material.
type RBDReference struct {
	Cluster  string   `json:"cluster,omitempty"`
	Monitors []string `json:"monitors,omitempty"`
	Pool     string   `json:"pool"`
	Image    string   `json:"image"`
	Snapshot string   `json:"snapshot,omitempty"`
	User     string   `json:"user,omitempty"`
	Conf     string   `json:"conf,omitempty"`
	Keyring  string   `json:"keyring,omitempty"`
}

// Attachment is the runtime description passed to QEMU. It may contain
// credentials and must never be persisted.
type Attachment struct {
	Protocol Protocol           `json:"protocol"`
	ISCSI    *ISCSIAttachment   `json:"iscsi,omitempty"`
	NVMeTCP  *NVMeTCPAttachment `json:"nvme_tcp,omitempty"`
	RBD      *RBDAttachment     `json:"rbd,omitempty"`
}

// ISCSIAttachment is an iSCSI reference plus its CHAP credential.
type ISCSIAttachment struct {
	ISCSIReference
	Username   string `json:"-"`
	Credential string `json:"-"`
}

// NVMeTCPAttachment is an NVMe/TCP reference plus its PSK.
type NVMeTCPAttachment struct {
	NVMeTCPReference
	PSK string `json:"-"`
}

// RBDAttachment is an RBD reference. The keyring is a path, not key material.
type RBDAttachment struct {
	RBDReference
}

// Reference returns the secret-free persisted form of the attachment.
func (a Attachment) Reference() Reference {
	reference := Reference{Protocol: a.Protocol}
	if a.ISCSI != nil {
		value := a.ISCSI.ISCSIReference
		reference.ISCSI = &value
	}
	if a.NVMeTCP != nil {
		value := a.NVMeTCP.NVMeTCPReference
		reference.NVMeTCP = &value
	}
	if a.RBD != nil {
		value := a.RBD.RBDReference
		reference.RBD = &value
	}
	return reference
}

// Normalize maps an empty protocol to local.
func (p Protocol) Normalize() Protocol {
	if p == "" {
		return ProtocolLocal
	}
	return p
}

// Validate checks a reference without requiring its protocol-specific body.
// It is suitable for a provisioning row that records only the protocol.
func (r Reference) Validate() error {
	protocol := r.Protocol.Normalize()
	switch protocol {
	case ProtocolLocal:
		if r.ISCSI != nil || r.NVMeTCP != nil || r.RBD != nil {
			return errors.New("local attachment must not have a protocol body")
		}
	case ProtocolISCSI:
		if r.NVMeTCP != nil || r.RBD != nil {
			return errors.New("iSCSI attachment has a mismatched protocol body")
		}
		if r.ISCSI != nil {
			return r.ISCSI.validate()
		}
	case ProtocolNVMeTCP:
		if r.ISCSI != nil || r.RBD != nil {
			return errors.New("NVMe/TCP attachment has a mismatched protocol body")
		}
		if r.NVMeTCP != nil {
			return r.NVMeTCP.validate()
		}
	case ProtocolRBD:
		if r.ISCSI != nil || r.NVMeTCP != nil {
			return errors.New("RBD attachment has a mismatched protocol body")
		}
		if r.RBD != nil {
			return r.RBD.validate()
		}
	default:
		return fmt.Errorf("unknown attachment protocol %q", r.Protocol)
	}
	return nil
}

// ValidateComplete requires the protocol-specific body and is used for disks
// that are ready to attach to a VM.
func (r Reference) ValidateComplete() error {
	if err := r.Validate(); err != nil {
		return err
	}
	switch r.Protocol.Normalize() {
	case ProtocolLocal:
		return nil
	case ProtocolISCSI:
		if r.ISCSI == nil {
			return errors.New("iSCSI attachment reference is missing")
		}
	case ProtocolNVMeTCP:
		if r.NVMeTCP == nil {
			return errors.New("NVMe/TCP attachment reference is missing")
		}
	case ProtocolRBD:
		if r.RBD == nil {
			return errors.New("RBD attachment reference is missing")
		}
	}
	return nil
}

// Locator returns a display-only description of the attachment.
func (r Reference) Locator() string {
	switch r.Protocol.Normalize() {
	case ProtocolISCSI:
		if r.ISCSI == nil {
			return ""
		}
		return fmt.Sprintf("iscsi %s %s lun %d", r.ISCSI.Portal, r.ISCSI.Target, r.ISCSI.LUN)
	case ProtocolNVMeTCP:
		if r.NVMeTCP == nil {
			return ""
		}
		return fmt.Sprintf("nvme-tcp %s %s nsid %d", r.NVMeTCP.Address, r.NVMeTCP.NQN, r.NVMeTCP.NSID)
	case ProtocolRBD:
		if r.RBD == nil {
			return ""
		}
		image := r.RBD.Pool + "/" + r.RBD.Image
		if r.RBD.Snapshot != "" {
			image += "@" + r.RBD.Snapshot
		}
		return "rbd " + image
	default:
		return ""
	}
}

func (r ISCSIReference) validate() error {
	if strings.TrimSpace(r.Portal) == "" {
		return errors.New("iSCSI portal is required")
	}
	if strings.TrimSpace(r.Target) == "" {
		return errors.New("iSCSI target is required")
	}
	if r.LUN < 0 {
		return errors.New("iSCSI LUN must not be negative")
	}
	return nil
}

func (r NVMeTCPReference) validate() error {
	if strings.TrimSpace(r.Address) == "" {
		return errors.New("NVMe/TCP address is required")
	}
	if strings.TrimSpace(r.NQN) == "" {
		return errors.New("NVMe/TCP NQN is required")
	}
	if r.NSID < 0 {
		return errors.New("NVMe/TCP NSID must not be negative")
	}
	return nil
}

func (r RBDReference) validate() error {
	if strings.TrimSpace(r.Pool) == "" {
		return errors.New("RBD pool is required")
	}
	if strings.TrimSpace(r.Image) == "" {
		return errors.New("RBD image is required")
	}
	return nil
}

// ValidateComplete checks that a runtime attachment has the body required by
// its protocol.
func (a Attachment) ValidateComplete() error {
	if err := a.Reference().ValidateComplete(); err != nil {
		return err
	}
	switch a.Protocol.Normalize() {
	case ProtocolISCSI:
		if a.ISCSI == nil {
			return errors.New("iSCSI attachment is missing")
		}
		if strings.TrimSpace(a.ISCSI.Username) == "" {
			return errors.New("iSCSI username is required")
		}
		if a.ISCSI.Credential == "" {
			return errors.New("iSCSI credential is required")
		}
	case ProtocolNVMeTCP:
		if a.NVMeTCP == nil {
			return errors.New("NVMe/TCP attachment is missing")
		}
	case ProtocolRBD:
		if a.RBD == nil {
			return errors.New("RBD attachment is missing")
		}
	}
	return nil
}
