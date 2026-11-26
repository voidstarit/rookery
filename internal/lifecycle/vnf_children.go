package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// reloadAfterChildWrite reloads an enabled parent's runtime after a child
// row write. A stopped or unreachable runtime keeps the row and leaves the VNF
// degraded; the daemon's desired spec is updated best-effort so a later start
// uses the new configuration. On a reload failure it runs rollback, reloads the
// restored configuration, and disables the parent when the restore fails.
func (s *Service) reloadAfterChildWrite(ctx context.Context, vnfID int64, rollback func() error) error {
	vnf, err := s.store.GetVNF(ctx, vnfID)
	if err != nil {
		return storeError(err)
	}
	if !vnf.Enabled {
		return nil
	}
	if !s.backend.Running(ctx, vnf) {
		// The daemon persists the prepared spec even for a stopped runtime, so
		// supervision and a manual restart pick up the child row. If the daemon
		// is unreachable, the row still stays committed and the VNF stays
		// degraded until it reconnects.
		if reloadErr := s.backend.ReloadVNF(ctx, vnf); reloadErr != nil {
			s.backend.QueueVNFResync(vnf)
			log.Debug().
				Int64("vnf_id", vnf.ID).
				Str("type", vnf.Type).
				Err(reloadErr).
				Msg("VNF daemon is unreachable; queued a resync for the committed child row")
		}
		return nil
	}
	if err := s.backend.ReloadVNF(ctx, vnf); err != nil {
		rollbackErr := rollback()
		restored, getErr := s.store.GetVNF(ctx, vnfID)
		var restoreReloadErr error
		if getErr == nil {
			restoreReloadErr = s.backend.ReloadVNF(ctx, restored)
		}
		if rollbackErr != nil || getErr != nil || restoreReloadErr != nil {
			var disableErr error
			if getErr == nil {
				restored.Enabled = false
				_, disableErr = s.store.UpdateVNFIdentity(ctx, restored)
			}
			log.Error().
				Int64("vnf_id", vnf.ID).
				Str("type", vnf.Type).
				Err(errors.Join(err, rollbackErr, getErr, restoreReloadErr, disableErr)).
				Msg("VNF child write reload failed and the previous configuration could not be restored")
			return &VNFError{Status: 500, Message: describeChildReloadFailure(
				vnf, err, rollbackErr, getErr, restoreReloadErr, disableErr)}
		}
		return internalError(fmt.Errorf("reload failed: %w", err))
	}
	return nil
}

// describeChildReloadFailure names every failure that left a child write and
// its parent runtime inconsistent. The API exposes it for 500 responses.
func describeChildReloadFailure(vnf store.VNF, reloadErr, rollbackErr, getErr, restoreErr, disableErr error) string {
	parts := []string{fmt.Sprintf("could not reload the %s VNF %s: %v", vnf.Type, vnf.Name, reloadErr)}
	if rollbackErr != nil {
		parts = append(parts, fmt.Sprintf("could not restore the previous row: %v", rollbackErr))
	}
	if getErr != nil {
		parts = append(parts, fmt.Sprintf("could not read the restored row: %v", getErr))
	}
	if restoreErr != nil {
		parts = append(parts, fmt.Sprintf("could not reload the previous configuration: %v", restoreErr))
	}
	if disableErr != nil {
		parts = append(parts, fmt.Sprintf("could not disable the VNF: %v", disableErr))
	} else {
		parts = append(parts, "the VNF was disabled")
	}
	return strings.Join(parts, "; ")
}

// applyNAT44ChildChange re-applies a NAT44 VNF's static mappings after a DNAT
// rule write. A skipped apply (disabled or degraded NAT44) keeps the row. A VPP
// failure restores the previous row and mapping set; a failed restore disables
// the parent.
func (s *Service) applyNAT44ChildChange(ctx context.Context, vnfID int64, rollback func() error) error {
	vnf, err := s.store.GetVNF(ctx, vnfID)
	if err != nil {
		return storeError(err)
	}
	if !vnf.Enabled || !s.backend.Running(ctx, vnf) {
		return nil
	}
	if applyErr := s.backend.ApplyNAT44DNATRules(ctx, vnf); applyErr != nil {
		rollbackErr := rollback()
		restored, getErr := s.store.GetVNF(ctx, vnfID)
		var restoreErr error
		if getErr == nil {
			restoreErr = s.backend.ApplyNAT44DNATRules(ctx, restored)
		}
		if rollbackErr != nil || getErr != nil || restoreErr != nil {
			var disableErr error
			if getErr == nil {
				restored.Enabled = false
				_, disableErr = s.store.UpdateVNFIdentity(ctx, restored)
			}
			log.Error().
				Int64("vnf_id", vnf.ID).
				Err(errors.Join(applyErr, rollbackErr, getErr, restoreErr, disableErr)).
				Msg("NAT44 mapping update failed and the previous state could not be restored")
			return &VNFError{Status: 500, Message: describeNAT44ChildFailure(
				vnf, applyErr, rollbackErr, getErr, restoreErr, disableErr)}
		}
		return internalError(fmt.Errorf("NAT44 mapping update failed: %w", applyErr))
	}
	return nil
}

// describeNAT44ChildFailure names every failure from an in-place mapping write.
func describeNAT44ChildFailure(vnf store.VNF, applyErr, rollbackErr, getErr, restoreErr, disableErr error) string {
	parts := []string{fmt.Sprintf("could not apply NAT44 mappings for %s: %v", vnf.Name, applyErr)}
	if rollbackErr != nil {
		parts = append(parts, fmt.Sprintf("could not restore the previous row: %v", rollbackErr))
	}
	if getErr != nil {
		parts = append(parts, fmt.Sprintf("could not read the restored row: %v", getErr))
	}
	if restoreErr != nil {
		parts = append(parts, fmt.Sprintf("could not restore the previous mappings: %v", restoreErr))
	}
	if disableErr != nil {
		parts = append(parts, fmt.Sprintf("could not disable the VNF: %v", disableErr))
	} else {
		parts = append(parts, "the VNF was disabled")
	}
	return strings.Join(parts, "; ")
}

// CreateDHCPStaticAssignment adds one DHCPv4 static assignment and reloads the
// running runtime.
func (s *Service) CreateDHCPStaticAssignment(ctx context.Context, request *rookeryv1.CreateDHCPStaticAssignmentRequest) (store.DHCPStaticAssignment, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeDHCPv4)
	if err != nil {
		return store.DHCPStaticAssignment{}, err
	}
	item, err := s.backend.NormalizeDHCPStaticAssignment(ctx, vnf, store.DHCPStaticAssignment{
		MAC: request.GetMac(), IP: request.GetIp(),
	})
	if err != nil {
		return store.DHCPStaticAssignment{}, inputError(err.Error())
	}
	created, err := s.store.CreateDHCPStaticAssignment(ctx, vnf.ID, item)
	if err != nil {
		return store.DHCPStaticAssignment{}, storeError(err)
	}
	if err := s.reloadAfterChildWrite(ctx, vnf.ID, func() error {
		return s.store.DeleteDHCPStaticAssignment(ctx, vnf.ID, created.ID)
	}); err != nil {
		return store.DHCPStaticAssignment{}, err
	}
	return created, nil
}

// UpdateDHCPStaticAssignment replaces one DHCPv4 static assignment and reloads
// the running runtime.
func (s *Service) UpdateDHCPStaticAssignment(ctx context.Context, request *rookeryv1.UpdateDHCPStaticAssignmentRequest) (store.DHCPStaticAssignment, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeDHCPv4)
	if err != nil {
		return store.DHCPStaticAssignment{}, err
	}
	previous, err := s.store.GetDHCPStaticAssignment(ctx, vnf.ID, request.GetAssignmentId())
	if err != nil {
		return store.DHCPStaticAssignment{}, storeError(err)
	}
	item, err := s.backend.NormalizeDHCPStaticAssignment(ctx, vnf, store.DHCPStaticAssignment{
		MAC: request.GetMac(), IP: request.GetIp(),
	})
	if err != nil {
		return store.DHCPStaticAssignment{}, inputError(err.Error())
	}
	updated, err := s.store.UpdateDHCPStaticAssignment(ctx, vnf.ID, previous.ID, item)
	if err != nil {
		return store.DHCPStaticAssignment{}, storeError(err)
	}
	if err := s.reloadAfterChildWrite(ctx, vnf.ID, func() error {
		_, restoreErr := s.store.UpdateDHCPStaticAssignment(ctx, vnf.ID, previous.ID, previous)
		return restoreErr
	}); err != nil {
		return store.DHCPStaticAssignment{}, err
	}
	return updated, nil
}

// DeleteDHCPStaticAssignment removes one DHCPv4 static assignment and reloads
// the running runtime.
func (s *Service) DeleteDHCPStaticAssignment(ctx context.Context, request *rookeryv1.DeleteDHCPStaticAssignmentRequest) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeDHCPv4)
	if err != nil {
		return err
	}
	previous, err := s.store.GetDHCPStaticAssignment(ctx, vnf.ID, request.GetAssignmentId())
	if err != nil {
		return storeError(err)
	}
	if err := s.store.DeleteDHCPStaticAssignment(ctx, vnf.ID, previous.ID); err != nil {
		return storeError(err)
	}
	return s.reloadAfterChildWrite(ctx, vnf.ID, func() error {
		_, restoreErr := s.store.CreateDHCPStaticAssignment(ctx, vnf.ID, previous)
		return restoreErr
	})
}

// CreateDHCPOption adds one DHCP option and reloads the running runtime.
func (s *Service) CreateDHCPOption(ctx context.Context, request *rookeryv1.CreateDHCPOptionRequest) (store.DHCPOption, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeDHCPv4, store.VNFTypeDHCPv6)
	if err != nil {
		return store.DHCPOption{}, err
	}
	item, err := s.backend.NormalizeDHCPOption(ctx, vnf, store.DHCPOption{
		Code: uint16(request.GetCode()), Format: request.GetFormat(), Value: request.GetValue(),
	})
	if err != nil {
		return store.DHCPOption{}, inputError(err.Error())
	}
	created, err := s.store.CreateDHCPOption(ctx, vnf.ID, item)
	if err != nil {
		return store.DHCPOption{}, storeError(err)
	}
	if err := s.reloadAfterChildWrite(ctx, vnf.ID, func() error {
		return s.store.DeleteDHCPOption(ctx, vnf.ID, created.ID)
	}); err != nil {
		return store.DHCPOption{}, err
	}
	return created, nil
}

// UpdateDHCPOption replaces one DHCP option and reloads the running runtime.
func (s *Service) UpdateDHCPOption(ctx context.Context, request *rookeryv1.UpdateDHCPOptionRequest) (store.DHCPOption, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeDHCPv4, store.VNFTypeDHCPv6)
	if err != nil {
		return store.DHCPOption{}, err
	}
	previous, err := s.store.GetDHCPOption(ctx, vnf.ID, request.GetOptionId())
	if err != nil {
		return store.DHCPOption{}, storeError(err)
	}
	item, err := s.backend.NormalizeDHCPOption(ctx, vnf, store.DHCPOption{
		Code: uint16(request.GetCode()), Format: request.GetFormat(), Value: request.GetValue(),
	})
	if err != nil {
		return store.DHCPOption{}, inputError(err.Error())
	}
	updated, err := s.store.UpdateDHCPOption(ctx, vnf.ID, previous.ID, item)
	if err != nil {
		return store.DHCPOption{}, storeError(err)
	}
	if err := s.reloadAfterChildWrite(ctx, vnf.ID, func() error {
		_, restoreErr := s.store.UpdateDHCPOption(ctx, vnf.ID, previous.ID, previous)
		return restoreErr
	}); err != nil {
		return store.DHCPOption{}, err
	}
	return updated, nil
}

// DeleteDHCPOption removes one DHCP option and reloads the running runtime.
func (s *Service) DeleteDHCPOption(ctx context.Context, request *rookeryv1.DeleteDHCPOptionRequest) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeDHCPv4, store.VNFTypeDHCPv6)
	if err != nil {
		return err
	}
	previous, err := s.store.GetDHCPOption(ctx, vnf.ID, request.GetOptionId())
	if err != nil {
		return storeError(err)
	}
	if err := s.store.DeleteDHCPOption(ctx, vnf.ID, previous.ID); err != nil {
		return storeError(err)
	}
	return s.reloadAfterChildWrite(ctx, vnf.ID, func() error {
		_, restoreErr := s.store.CreateDHCPOption(ctx, vnf.ID, previous)
		return restoreErr
	})
}

// CreateDNSVNFRecord adds one static DNS record and reloads the resolver.
func (s *Service) CreateDNSVNFRecord(ctx context.Context, request *rookeryv1.CreateDNSVNFRecordRequest) (store.DNSRecord, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeDNS)
	if err != nil {
		return store.DNSRecord{}, err
	}
	siblings, err := s.store.ListDNSVNFRecords(ctx, vnf.ID)
	if err != nil {
		return store.DNSRecord{}, internalError(err)
	}
	item, err := s.backend.NormalizeDNSRecord(ctx, vnf, siblings, store.DNSRecord{
		Name: request.GetName(), Type: request.GetType(), Value: request.GetValue(), TTL: request.GetTtl(),
	})
	if err != nil {
		return store.DNSRecord{}, inputError(err.Error())
	}
	created, err := s.store.CreateDNSVNFRecord(ctx, vnf.ID, item)
	if err != nil {
		return store.DNSRecord{}, storeError(err)
	}
	if err := s.reloadAfterChildWrite(ctx, vnf.ID, func() error {
		return s.store.DeleteDNSVNFRecord(ctx, vnf.ID, created.ID)
	}); err != nil {
		return store.DNSRecord{}, err
	}
	return created, nil
}

// UpdateDNSVNFRecord replaces one static DNS record and reloads the resolver.
func (s *Service) UpdateDNSVNFRecord(ctx context.Context, request *rookeryv1.UpdateDNSVNFRecordRequest) (store.DNSRecord, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeDNS)
	if err != nil {
		return store.DNSRecord{}, err
	}
	previous, err := s.store.GetDNSVNFRecord(ctx, vnf.ID, request.GetRecordId())
	if err != nil {
		return store.DNSRecord{}, storeError(err)
	}
	siblings, err := s.store.ListDNSVNFRecords(ctx, vnf.ID)
	if err != nil {
		return store.DNSRecord{}, internalError(err)
	}
	others := make([]store.DNSRecord, 0, len(siblings))
	for _, sibling := range siblings {
		if sibling.ID != previous.ID {
			others = append(others, sibling)
		}
	}
	item, err := s.backend.NormalizeDNSRecord(ctx, vnf, others, store.DNSRecord{
		Name: request.GetName(), Type: request.GetType(), Value: request.GetValue(), TTL: request.GetTtl(),
	})
	if err != nil {
		return store.DNSRecord{}, inputError(err.Error())
	}
	updated, err := s.store.UpdateDNSVNFRecord(ctx, vnf.ID, previous.ID, item)
	if err != nil {
		return store.DNSRecord{}, storeError(err)
	}
	if err := s.reloadAfterChildWrite(ctx, vnf.ID, func() error {
		_, restoreErr := s.store.UpdateDNSVNFRecord(ctx, vnf.ID, previous.ID, previous)
		return restoreErr
	}); err != nil {
		return store.DNSRecord{}, err
	}
	return updated, nil
}

// DeleteDNSVNFRecord removes one static DNS record and reloads the resolver.
func (s *Service) DeleteDNSVNFRecord(ctx context.Context, request *rookeryv1.DeleteDNSVNFRecordRequest) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeDNS)
	if err != nil {
		return err
	}
	previous, err := s.store.GetDNSVNFRecord(ctx, vnf.ID, request.GetRecordId())
	if err != nil {
		return storeError(err)
	}
	if err := s.store.DeleteDNSVNFRecord(ctx, vnf.ID, previous.ID); err != nil {
		return storeError(err)
	}
	return s.reloadAfterChildWrite(ctx, vnf.ID, func() error {
		_, restoreErr := s.store.CreateDNSVNFRecord(ctx, vnf.ID, previous)
		return restoreErr
	})
}

// CreateDNSVNFForwarder adds one forwarder and reloads the resolver.
func (s *Service) CreateDNSVNFForwarder(ctx context.Context, request *rookeryv1.CreateDNSVNFForwarderRequest) (store.DNSForwarder, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeDNS)
	if err != nil {
		return store.DNSForwarder{}, err
	}
	siblings, err := s.store.ListDNSVNFForwarders(ctx, vnf.ID)
	if err != nil {
		return store.DNSForwarder{}, internalError(err)
	}
	item, err := s.backend.NormalizeDNSForwarder(ctx, vnf, siblings, store.DNSForwarder{
		Address: request.GetAddress(), Position: request.GetPosition(),
	})
	if err != nil {
		return store.DNSForwarder{}, inputError(err.Error())
	}
	created, err := s.store.CreateDNSVNFForwarder(ctx, vnf.ID, item)
	if err != nil {
		return store.DNSForwarder{}, storeError(err)
	}
	if err := s.reloadAfterChildWrite(ctx, vnf.ID, func() error {
		return s.store.DeleteDNSVNFForwarder(ctx, vnf.ID, created.ID)
	}); err != nil {
		return store.DNSForwarder{}, err
	}
	return created, nil
}

// UpdateDNSVNFForwarder replaces one forwarder and reloads the resolver.
func (s *Service) UpdateDNSVNFForwarder(ctx context.Context, request *rookeryv1.UpdateDNSVNFForwarderRequest) (store.DNSForwarder, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeDNS)
	if err != nil {
		return store.DNSForwarder{}, err
	}
	previous, err := s.store.GetDNSVNFForwarder(ctx, vnf.ID, request.GetForwarderId())
	if err != nil {
		return store.DNSForwarder{}, storeError(err)
	}
	siblings, err := s.store.ListDNSVNFForwarders(ctx, vnf.ID)
	if err != nil {
		return store.DNSForwarder{}, internalError(err)
	}
	others := make([]store.DNSForwarder, 0, len(siblings))
	for _, sibling := range siblings {
		if sibling.ID != previous.ID {
			others = append(others, sibling)
		}
	}
	item, err := s.backend.NormalizeDNSForwarder(ctx, vnf, others, store.DNSForwarder{
		Address: request.GetAddress(), Position: request.GetPosition(),
	})
	if err != nil {
		return store.DNSForwarder{}, inputError(err.Error())
	}
	updated, err := s.store.UpdateDNSVNFForwarder(ctx, vnf.ID, previous.ID, item)
	if err != nil {
		return store.DNSForwarder{}, storeError(err)
	}
	if err := s.reloadAfterChildWrite(ctx, vnf.ID, func() error {
		_, restoreErr := s.store.UpdateDNSVNFForwarder(ctx, vnf.ID, previous.ID, previous)
		return restoreErr
	}); err != nil {
		return store.DNSForwarder{}, err
	}
	return updated, nil
}

// DeleteDNSVNFForwarder removes one forwarder and reloads the resolver.
func (s *Service) DeleteDNSVNFForwarder(ctx context.Context, request *rookeryv1.DeleteDNSVNFForwarderRequest) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeDNS)
	if err != nil {
		return err
	}
	previous, err := s.store.GetDNSVNFForwarder(ctx, vnf.ID, request.GetForwarderId())
	if err != nil {
		return storeError(err)
	}
	if err := s.store.DeleteDNSVNFForwarder(ctx, vnf.ID, previous.ID); err != nil {
		return storeError(err)
	}
	return s.reloadAfterChildWrite(ctx, vnf.ID, func() error {
		_, restoreErr := s.store.CreateDNSVNFForwarder(ctx, vnf.ID, previous)
		return restoreErr
	})
}

// CreateNAT44DNATRule adds one DNAT rule and replaces the VNF's VPP mappings.
func (s *Service) CreateNAT44DNATRule(ctx context.Context, request *rookeryv1.CreateNAT44DNATRuleRequest) (store.DNATRule, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeNAT44)
	if err != nil {
		return store.DNATRule{}, err
	}
	siblings, err := s.store.ListNAT44DNATRules(ctx, vnf.ID)
	if err != nil {
		return store.DNATRule{}, internalError(err)
	}
	item, err := s.backend.NormalizeDNATRule(ctx, vnf, siblings, store.DNATRule{
		Protocol: request.GetProtocol(), ExternalPort: int(request.GetExternalPort()),
		LocalIP: request.GetLocalIp(), LocalPort: int(request.GetLocalPort()),
	})
	if err != nil {
		return store.DNATRule{}, inputError(err.Error())
	}
	created, err := s.store.CreateNAT44DNATRule(ctx, vnf.ID, item)
	if err != nil {
		return store.DNATRule{}, storeError(err)
	}
	if err := s.applyNAT44ChildChange(ctx, vnf.ID, func() error {
		return s.store.DeleteNAT44DNATRule(ctx, vnf.ID, created.ID)
	}); err != nil {
		return store.DNATRule{}, err
	}
	return created, nil
}

// UpdateNAT44DNATRule replaces one DNAT rule and replaces the VPP mappings.
func (s *Service) UpdateNAT44DNATRule(ctx context.Context, request *rookeryv1.UpdateNAT44DNATRuleRequest) (store.DNATRule, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeNAT44)
	if err != nil {
		return store.DNATRule{}, err
	}
	previous, err := s.store.GetNAT44DNATRule(ctx, vnf.ID, request.GetRuleId())
	if err != nil {
		return store.DNATRule{}, storeError(err)
	}
	siblings, err := s.store.ListNAT44DNATRules(ctx, vnf.ID)
	if err != nil {
		return store.DNATRule{}, internalError(err)
	}
	others := make([]store.DNATRule, 0, len(siblings))
	for _, sibling := range siblings {
		if sibling.ID != previous.ID {
			others = append(others, sibling)
		}
	}
	item, err := s.backend.NormalizeDNATRule(ctx, vnf, others, store.DNATRule{
		Protocol: request.GetProtocol(), ExternalPort: int(request.GetExternalPort()),
		LocalIP: request.GetLocalIp(), LocalPort: int(request.GetLocalPort()),
	})
	if err != nil {
		return store.DNATRule{}, inputError(err.Error())
	}
	updated, err := s.store.UpdateNAT44DNATRule(ctx, vnf.ID, previous.ID, item)
	if err != nil {
		return store.DNATRule{}, storeError(err)
	}
	if err := s.applyNAT44ChildChange(ctx, vnf.ID, func() error {
		_, restoreErr := s.store.UpdateNAT44DNATRule(ctx, vnf.ID, previous.ID, previous)
		return restoreErr
	}); err != nil {
		return store.DNATRule{}, err
	}
	return updated, nil
}

// DeleteNAT44DNATRule removes one DNAT rule and replaces the VPP mappings.
func (s *Service) DeleteNAT44DNATRule(ctx context.Context, request *rookeryv1.DeleteNAT44DNATRuleRequest) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeNAT44)
	if err != nil {
		return err
	}
	previous, err := s.store.GetNAT44DNATRule(ctx, vnf.ID, request.GetRuleId())
	if err != nil {
		return storeError(err)
	}
	if err := s.store.DeleteNAT44DNATRule(ctx, vnf.ID, previous.ID); err != nil {
		return storeError(err)
	}
	return s.applyNAT44ChildChange(ctx, vnf.ID, func() error {
		_, restoreErr := s.store.CreateNAT44DNATRule(ctx, vnf.ID, previous)
		return restoreErr
	})
}

// CreateBGPVNFAdvertisedPrefix adds one advertised prefix and reloads the
// running BGP runtime.
func (s *Service) CreateBGPVNFAdvertisedPrefix(ctx context.Context, request *rookeryv1.CreateBGPVNFAdvertisedPrefixRequest) (store.BGPPrefix, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeBGP)
	if err != nil {
		return store.BGPPrefix{}, err
	}
	item, err := s.backend.NormalizeBGPPrefix(ctx, vnf, request.GetPrefix())
	if err != nil {
		return store.BGPPrefix{}, inputError(err.Error())
	}
	created, err := s.store.CreateBGPVNFAdvertisedPrefix(ctx, vnf.ID, item)
	if err != nil {
		return store.BGPPrefix{}, storeError(err)
	}
	if err := s.reloadAfterChildWrite(ctx, vnf.ID, func() error {
		return s.store.DeleteBGPVNFAdvertisedPrefix(ctx, vnf.ID, created.ID)
	}); err != nil {
		return store.BGPPrefix{}, err
	}
	return created, nil
}

// UpdateBGPVNFAdvertisedPrefix replaces one advertised prefix and reloads BGP.
func (s *Service) UpdateBGPVNFAdvertisedPrefix(ctx context.Context, request *rookeryv1.UpdateBGPVNFAdvertisedPrefixRequest) (store.BGPPrefix, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeBGP)
	if err != nil {
		return store.BGPPrefix{}, err
	}
	previous, err := s.store.GetBGPVNFAdvertisedPrefix(ctx, vnf.ID, request.GetPrefixId())
	if err != nil {
		return store.BGPPrefix{}, storeError(err)
	}
	item, err := s.backend.NormalizeBGPPrefix(ctx, vnf, request.GetPrefix())
	if err != nil {
		return store.BGPPrefix{}, inputError(err.Error())
	}
	updated, err := s.store.UpdateBGPVNFAdvertisedPrefix(ctx, vnf.ID, previous.ID, item)
	if err != nil {
		return store.BGPPrefix{}, storeError(err)
	}
	if err := s.reloadAfterChildWrite(ctx, vnf.ID, func() error {
		_, restoreErr := s.store.UpdateBGPVNFAdvertisedPrefix(ctx, vnf.ID, previous.ID, previous)
		return restoreErr
	}); err != nil {
		return store.BGPPrefix{}, err
	}
	return updated, nil
}

// DeleteBGPVNFAdvertisedPrefix removes one advertised prefix and reloads BGP.
func (s *Service) DeleteBGPVNFAdvertisedPrefix(ctx context.Context, request *rookeryv1.DeleteBGPVNFAdvertisedPrefixRequest) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, request.GetVnfId(), store.VNFTypeBGP)
	if err != nil {
		return err
	}
	previous, err := s.store.GetBGPVNFAdvertisedPrefix(ctx, vnf.ID, request.GetPrefixId())
	if err != nil {
		return storeError(err)
	}
	if err := s.store.DeleteBGPVNFAdvertisedPrefix(ctx, vnf.ID, previous.ID); err != nil {
		return storeError(err)
	}
	return s.reloadAfterChildWrite(ctx, vnf.ID, func() error {
		_, restoreErr := s.store.CreateBGPVNFAdvertisedPrefix(ctx, vnf.ID, previous)
		return restoreErr
	})
}
