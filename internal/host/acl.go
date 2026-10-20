package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.fd.io/govpp/binapi/acl"
	"go.fd.io/govpp/binapi/acl_types"
	"go.fd.io/govpp/binapi/interface_types"
	"go.fd.io/govpp/binapi/ip_types"

	"github.com/voidstarit/rookery/internal/store"
)

const aclTagPrefix = "nerds-acl:"

// EnsureVPPACL creates or replaces the VPP ACL for an ACL database record.
// The returned index is suitable for assigning to interfaces.
func (v *VPPNetwork) EnsureVPPACL(ctx context.Context, aclID int64, rules []store.ACLRule) (uint32, error) {
	if aclID < 1 {
		return 0, errors.New("invalid ACL id")
	}
	vppRules, err := buildVPPACLRules(rules)
	if err != nil {
		return 0, err
	}
	conn, err := v.connect()
	if err != nil {
		return 0, err
	}
	defer conn.Disconnect()

	client := acl.NewServiceClient(conn)
	tag := fmt.Sprintf("%s%d", aclTagPrefix, aclID)
	index := ^uint32(0)
	stream, err := client.ACLDump(ctx, &acl.ACLDump{ACLIndex: ^uint32(0)})
	if err != nil {
		return 0, fmt.Errorf("dump VPP ACLs: %w", err)
	}
	for {
		details, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return 0, fmt.Errorf("read VPP ACL dump: %w", recvErr)
		}
		if details.Tag == tag {
			index = details.ACLIndex
			break
		}
	}

	reply, err := client.ACLAddReplace(ctx, &acl.ACLAddReplace{
		ACLIndex: index,
		Tag:      tag,
		R:        vppRules,
	})
	if err != nil {
		return 0, fmt.Errorf("configure VPP ACL %d: %w", aclID, err)
	}
	if err := checkVPPRetval("configure VPP ACL", reply.Retval); err != nil {
		return 0, err
	}
	return reply.ACLIndex, nil
}

// DeleteVPPACL removes a VPP ACL if it still exists.
func (v *VPPNetwork) DeleteVPPACL(ctx context.Context, index uint32) error {
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()
	reply, err := acl.NewServiceClient(conn).ACLDel(ctx, &acl.ACLDel{ACLIndex: index})
	if err != nil {
		if isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete VPP ACL %d: %w", index, err)
	}
	return checkVPPRetval("delete VPP ACL", reply.Retval)
}

// SetInterfaceACLs applies ordered ingress and egress ACL index lists to a VPP
// interface. References are one-based so that VPP ACL index 0 remains usable;
// an empty list clears that direction.
func (v *VPPNetwork) SetInterfaceACLs(ctx context.Context, swIfIndex uint32, ingressRefs, egressRefs []uint32) error {
	if swIfIndex == 0 {
		return errors.New("interface index is required")
	}
	if len(ingressRefs) > 255 || len(egressRefs) > 255 {
		return errors.New("too many interface ACLs")
	}
	acls := make([]uint32, 0, len(ingressRefs)+len(egressRefs))
	for _, ref := range ingressRefs {
		if ref != 0 {
			acls = append(acls, ref-1)
		}
	}
	nInput := uint8(len(acls))
	for _, ref := range egressRefs {
		if ref != 0 {
			acls = append(acls, ref-1)
		}
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()
	reply, err := acl.NewServiceClient(conn).ACLInterfaceSetACLList(ctx, &acl.ACLInterfaceSetACLList{
		SwIfIndex: interface_types.InterfaceIndex(swIfIndex),
		NInput:    nInput,
		Acls:      acls,
	})
	if err != nil {
		return fmt.Errorf("set interface ACLs: %w", err)
	}
	return checkVPPRetval("set interface ACLs", reply.Retval)
}

func buildVPPACLRules(rules []store.ACLRule) ([]acl_types.ACLRule, error) {
	result := make([]acl_types.ACLRule, 0, len(rules))
	for i, rule := range rules {
		src, err := ip_types.ParsePrefix(rule.Source)
		if err != nil {
			return nil, fmt.Errorf("rule %d: invalid source prefix: %w", i+1, err)
		}
		dst, err := ip_types.ParsePrefix(rule.Destination)
		if err != nil {
			return nil, fmt.Errorf("rule %d: invalid destination prefix: %w", i+1, err)
		}
		if src.Address.Af != dst.Address.Af {
			return nil, fmt.Errorf("rule %d: source and destination address families differ", i+1)
		}
		action := acl_types.ACL_ACTION_API_DENY
		if strings.EqualFold(rule.Action, "permit") {
			action = acl_types.ACL_ACTION_API_PERMIT
		}
		proto := ip_types.IP_API_PROTO_HOPOPT
		switch strings.ToLower(rule.Protocol) {
		case "", "any":
			proto = ip_types.IP_API_PROTO_HOPOPT
		case "tcp":
			proto = ip_types.IP_API_PROTO_TCP
		case "udp":
			proto = ip_types.IP_API_PROTO_UDP
		case "icmp":
			proto = ip_types.IP_API_PROTO_ICMP
		case "icmpv6":
			proto = ip_types.IP_API_PROTO_ICMP6
		default:
			return nil, fmt.Errorf("rule %d: unsupported protocol %q", i+1, rule.Protocol)
		}
		srcFirst, srcLast := uint16(0), uint16(0)
		dstFirst, dstLast := uint16(0), uint16(0)
		if proto == ip_types.IP_API_PROTO_TCP || proto == ip_types.IP_API_PROTO_UDP {
			srcFirst = uint16(rule.SourcePortStart)
			srcLast = uint16(rule.SourcePortEnd)
			dstFirst = uint16(rule.DestinationPortStart)
			dstLast = uint16(rule.DestinationPortEnd)
			if srcFirst == 0 && srcLast == 0 {
				srcLast = 65535
			}
			if dstFirst == 0 && dstLast == 0 {
				dstLast = 65535
			}
		} else if proto == ip_types.IP_API_PROTO_ICMP || proto == ip_types.IP_API_PROTO_ICMP6 {
			srcLast = 255
			dstLast = 255
		}
		result = append(result, acl_types.ACLRule{
			IsPermit:               action,
			SrcPrefix:              src,
			DstPrefix:              dst,
			Proto:                  proto,
			SrcportOrIcmptypeFirst: srcFirst,
			SrcportOrIcmptypeLast:  srcLast,
			DstportOrIcmpcodeFirst: dstFirst,
			DstportOrIcmpcodeLast:  dstLast,
		})
	}
	return result, nil
}
